from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Engine runtime configuration.

    Defaults bind to loopback only: the engine is internal and must never be
    reachable from a public interface.
    """

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    host: str = "127.0.0.1"
    port: int = 8000
    internal_token: str = ""


settings = Settings()
