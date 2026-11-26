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


settings = Settings()
