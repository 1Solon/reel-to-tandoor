"""Settings, read from environment variables."""

import json
from functools import cached_property

from pydantic import BaseModel, Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class User(BaseModel):
    name: str
    tandoor_token: str


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    # Where the service talks to Tandoor. In the cluster this is the Service, not the public URL.
    tandoor_url: str = "http://tandoor.tandoor.svc.cluster.local"
    # Tandoor's ALLOWED_HOSTS rejects requests that don't name the public host.
    tandoor_host_header: str | None = None
    # Used to build the link the Shortcut opens. Defaults to tandoor_url.
    tandoor_public_url: str | None = None
    tandoor_ai_provider_id: int = 34
    tandoor_timeout: float = 120.0

    # JSON object mapping each API key to the Tandoor user it saves for:
    # {"<api key>": {"name": "mum", "tandoor_token": "tda_..."}}
    users: str = Field(default="{}", alias="REEL_USERS")

    allow_tiktok: bool = True
    # Optional Netscape-format cookies file for when Instagram stops serving anonymous downloads.
    cookies_file: str | None = None
    max_duration_seconds: int = 600
    max_download_bytes: int = 100 * 1024 * 1024

    whisper_model: str = "small"
    whisper_model_dir: str | None = None
    whisper_cpu_threads: int = 4
    whisper_compute_type: str = "int8"

    @cached_property
    def user_map(self) -> dict[str, User]:
        raw = json.loads(self.users or "{}")
        return {key: User.model_validate(value) for key, value in raw.items() if key}

    @property
    def public_url(self) -> str:
        return (self.tandoor_public_url or self.tandoor_url).rstrip("/")
