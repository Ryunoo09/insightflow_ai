"""Namespaced Redis access for ephemeral Engine state.

Redis holds only soft state (the deterministic result cache). Dataset files and
metadata live in storage and PostgreSQL.
"""

from __future__ import annotations

import redis.asyncio as redis

PREFIX_RESULT_CACHE = "rc"
DEFAULT_RESULT_TTL_SECONDS = 24 * 60 * 60


def build_key(prefix: str, *parts: str) -> str:
    """Build a namespaced key: prefix:part:part."""
    return ":".join((prefix, *parts))


class CacheClient:
    """Thin Redis wrapper that refuses keys without a TTL."""

    def __init__(self, url: str) -> None:
        self._redis = redis.from_url(url, decode_responses=True)

    async def close(self) -> None:
        await self._redis.aclose()

    async def set_with_ttl(self, key: str, value: str, ttl_seconds: int) -> None:
        if ttl_seconds <= 0:
            raise ValueError(f"ttl_seconds must be positive, got {ttl_seconds}")
        await self._redis.set(key, value, ex=ttl_seconds)

    async def get(self, key: str) -> str | None:
        return await self._redis.get(key)

    async def delete(self, key: str) -> None:
        await self._redis.delete(key)
