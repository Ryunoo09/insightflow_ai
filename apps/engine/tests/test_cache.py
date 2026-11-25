import asyncio
import os

import pytest

from app.cache.redis import CacheClient, build_key

pytestmark = pytest.mark.skipif(
    not os.getenv("TEST_REDIS_URL"), reason="TEST_REDIS_URL not set"
)


def _run(coro):
    return asyncio.run(coro)


def test_set_get_delete_with_ttl():
    async def scenario():
        client = CacheClient(os.environ["TEST_REDIS_URL"])
        key = build_key("test", "engine-cache", "1")
        try:
            await client.set_with_ttl(key, "v", 30)
            assert await client.get(key) == "v"
            await client.delete(key)
            assert await client.get(key) is None
        finally:
            await client.close()

    _run(scenario())


def test_set_with_ttl_rejects_non_positive():
    async def scenario():
        client = CacheClient(os.environ["TEST_REDIS_URL"])
        try:
            with pytest.raises(ValueError):
                await client.set_with_ttl(build_key("test", "bad"), "v", 0)
        finally:
            await client.close()

    _run(scenario())
