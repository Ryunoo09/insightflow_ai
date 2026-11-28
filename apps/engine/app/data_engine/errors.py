"""Structured dataset processing failures.

Each error carries a stable code from the PRD error catalog, the HTTP status
the API should surface, and whether the failure is worth retrying. Data
problems are never retryable; they need a different file or an override.
"""

from __future__ import annotations


class DatasetError(Exception):
    def __init__(
        self,
        code: str,
        message: str,
        *,
        http_status: int = 422,
        retryable: bool = False,
        details: dict | None = None,
    ) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.http_status = http_status
        self.retryable = retryable
        self.details = details or {}


class EngineUnavailable(DatasetError):
    def __init__(self, message: str = "pemrosesan sementara tidak tersedia") -> None:
        super().__init__("ENGINE_UNAVAILABLE", message, http_status=503, retryable=True)
