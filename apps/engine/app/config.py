from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Engine runtime configuration.

    Defaults bind to loopback only: the engine is internal and must never be
    reachable from a public interface.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    host: str = "127.0.0.1"
    port: int = 8000
    # Shared secret with the Go API (same env var name on both sides).
    internal_token: str = Field(default="", validation_alias="ENGINE_INTERNAL_TOKEN")

    # Shared with the Go API: sources and Parquet files live under this root.
    storage_root: str = "storage"

    # Profiling resource caps (PRD 26.7 / TASK-405).
    profile_memory_limit: str = "1GB"
    profile_threads: int = 2
    profile_timeout_seconds: int = 30

    # Explorer query caps (Plan 05 / TASK-501): a slow scan is aborted and the
    # reader connection is memory-bounded.
    explorer_timeout_seconds: int = 5
    explorer_memory_limit: str = "1GB"
    explorer_threads: int = 2

    # LLM provider (Plan 06 / TASK-603). Only the engine may hold these; the Go
    # API never talks to a provider directly.
    llm_api_key: str = Field(default="", validation_alias="LLM_API_KEY")
    llm_base_url: str = Field(default="https://api.openai.com/v1", validation_alias="LLM_BASE_URL")
    llm_model: str = Field(default="", validation_alias="LLM_MODEL")
    llm_timeout_seconds: int = 30
    # Narrator output is capped so a runaway model cannot stream forever.
    llm_max_narrative_tokens: int = 700

    # Analysis run caps (Plan 06).
    analysis_max_tool_rows: int = 1000
    analysis_result_cache_ttl_seconds: int = 24 * 60 * 60
    redis_url: str = Field(default="redis://127.0.0.1:6379/0", validation_alias="REDIS_URL")
    env: str = Field(default="development", validation_alias="APP_ENV")


settings = Settings()
