"""OpenAI-compatible LLM client (Plan 06).

Three provider realities are handled explicitly, because all of them were
measured during the pre-implementation probe (Plan 06 §9.1):

* Only ``response_format: json_schema`` with ``strict: true`` is used. The
  ``json_object`` mode was never observed to respect enums.
* A provider may answer with an HTML body (WAF rejection) or an empty body. That
  is a controlled failure, never a JSON parse error and never a 500.
* Providers do not expose quota reliably, so usage is tracked by the caller.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any

import httpx

from app.config import settings
from app.data_engine.errors import DatasetError


class LLMError(DatasetError):
    """A provider failure that is safe to surface to the API layer."""

    def __init__(self, code: str, message: str, *, http_status: int = 502, retryable: bool = True) -> None:
        super().__init__(code, message, http_status=http_status, retryable=retryable)


@dataclass
class LLMResponse:
    content: str
    prompt_tokens: int = 0
    completion_tokens: int = 0


@dataclass
class Usage:
    """Token usage accumulated across every LLM call in one run."""

    prompt_tokens: int = 0
    completion_tokens: int = 0
    calls: int = 0

    def add(self, response: LLMResponse) -> None:
        self.prompt_tokens += response.prompt_tokens
        self.completion_tokens += response.completion_tokens
        self.calls += 1

    def as_dict(self) -> dict[str, int]:
        return {
            "prompt_tokens": self.prompt_tokens,
            "completion_tokens": self.completion_tokens,
            "calls": self.calls,
        }


class LLMClient:
    """Minimal chat-completions client with strict JSON output."""

    def __init__(
        self,
        *,
        api_key: str | None = None,
        base_url: str | None = None,
        model: str | None = None,
        timeout_seconds: int | None = None,
    ) -> None:
        self._api_key = settings.llm_api_key if api_key is None else api_key
        self._base_url = (settings.llm_base_url if base_url is None else base_url).rstrip("/")
        self._model = settings.llm_model if model is None else model
        self._timeout = settings.llm_timeout_seconds if timeout_seconds is None else timeout_seconds

    @property
    def model(self) -> str:
        return self._model

    def complete_json(
        self,
        *,
        system: str,
        user: str,
        schema: dict[str, Any],
        schema_name: str = "analysis_plan",
        temperature: float = 0.0,
    ) -> LLMResponse:
        """Ask for one JSON object that validates against schema."""
        payload = {
            "model": self._model,
            "temperature": temperature,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
            "response_format": {
                "type": "json_schema",
                "json_schema": {"name": schema_name, "strict": True, "schema": schema},
            },
        }
        return self._post(payload)

    def complete_text(self, *, system: str, user: str, temperature: float = 0.2) -> LLMResponse:
        """Plain completion for the narrator. Output is never trusted blindly."""
        payload = {
            "model": self._model,
            "temperature": temperature,
            "max_tokens": settings.llm_max_narrative_tokens,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
        }
        return self._post(payload)

    def _post(self, payload: dict[str, Any]) -> LLMResponse:
        if not self._api_key or not self._model:
            raise LLMError(
                "LLM_NOT_CONFIGURED",
                "provider LLM belum dikonfigurasi.",
                http_status=503,
                retryable=False,
            )
        try:
            response = httpx.post(
                f"{self._base_url}/chat/completions",
                headers={
                    "Authorization": f"Bearer {self._api_key}",
                    "Content-Type": "application/json",
                },
                json=payload,
                timeout=self._timeout,
            )
        except httpx.TimeoutException as exc:
            raise LLMError("LLM_TIMEOUT", "provider LLM melebihi batas waktu.") from exc
        except httpx.HTTPError as exc:
            raise LLMError("LLM_UNAVAILABLE", "provider LLM tidak dapat dihubungi.") from exc

        if response.status_code == 403:
            # Observed as a Cloudflare HTML page when a prompt contains SQL
            # keywords. It is a blocked request, not a server fault.
            raise LLMError(
                "LLM_REQUEST_BLOCKED",
                "permintaan diblokir oleh penyedia LLM.",
                http_status=422,
                retryable=False,
            )
        if response.status_code == 429:
            raise LLMError("LLM_RATE_LIMITED", "kuota provider LLM habis, coba lagi nanti.")
        if response.status_code >= 400:
            raise LLMError("LLM_UNAVAILABLE", f"provider LLM menolak permintaan ({response.status_code}).")

        try:
            body = response.json()
        except (json.JSONDecodeError, ValueError) as exc:
            # An HTML or empty body must never become a parse traceback.
            raise LLMError("LLM_UNAVAILABLE", "respons provider LLM tidak dapat dibaca.") from exc

        try:
            choice = body["choices"][0]
            content = choice["message"]["content"] or ""
        except (KeyError, IndexError, TypeError) as exc:
            raise LLMError("LLM_UNAVAILABLE", "respons provider LLM tidak lengkap.") from exc

        usage = body.get("usage") or {}
        return LLMResponse(
            content=content,
            prompt_tokens=int(usage.get("prompt_tokens") or 0),
            completion_tokens=int(usage.get("completion_tokens") or 0),
        )
