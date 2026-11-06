"""HTTP API the iOS Shortcut calls."""

import hmac
import logging
import tempfile
import time
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Header
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from . import reel as reel_mod
from .config import Settings, User
from .links import InvalidLink, normalize_url
from .recipe import NoRecipeFound, build_recipe_payload, compose_ai_text
from .tandoor import TandoorClient, TandoorError
from .transcribe import Transcriber

log = logging.getLogger("reel_to_tandoor")


class SaveRequest(BaseModel):
    # Whatever the share sheet produced: a bare link or text with a link in it.
    url: str


def _reply(status: int, message: str, **extra) -> JSONResponse:
    """Every reply has ``ok`` and ``message`` so the Shortcut can show ``message`` whatever happened."""
    return JSONResponse(status_code=status, content={"ok": 200 <= status < 300, "message": message, **extra})


def find_user(settings: Settings, api_key: str | None) -> User | None:
    if not api_key:
        return None
    for key, user in settings.user_map.items():
        if hmac.compare_digest(key.encode(), api_key.encode()):
            return user
    return None


def save_reel(url: str, user: User, settings: Settings, transcriber: Transcriber) -> dict:
    """Download, transcribe, AI-import and save one reel. Returns the saved recipe's id, name and link."""
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="reel-") as tmp:
        reel = reel_mod.download(
            url,
            Path(tmp),
            cookies_file=settings.cookies_file,
            max_duration=settings.max_duration_seconds,
            max_bytes=settings.max_download_bytes,
        )
        transcript = transcriber.transcribe(reel_mod.decode_audio(reel.audio_path))
    log.info("Downloaded and transcribed %s in %.1f s", url, time.monotonic() - started)

    text = compose_ai_text(reel.caption, transcript)
    with TandoorClient(
        settings.tandoor_url, user.tandoor_token, settings.tandoor_host_header, settings.tandoor_timeout
    ) as tandoor:
        ai_recipe = tandoor.ai_import(text, settings.tandoor_ai_provider_id)
        payload = build_recipe_payload(ai_recipe, url, caption=reel.caption, uploader=reel.uploader)
        recipe = tandoor.create_recipe(payload)

        thumbnail = reel_mod.fetch_thumbnail(reel.thumbnail_url)
        if thumbnail:
            try:
                tandoor.upload_image(recipe["id"], *thumbnail)
            except TandoorError as exc:
                log.warning("Saved recipe %s without an image: %s", recipe["id"], exc)

    log.info("Saved %s as recipe %s for %s in %.1f s", url, recipe["id"], user.name, time.monotonic() - started)
    return {
        "recipe_id": recipe["id"],
        "name": recipe["name"],
        "recipe_url": f"{settings.public_url}/recipe/{recipe['id']}",
    }


def create_app(settings: Settings | None = None, transcriber: Transcriber | None = None) -> FastAPI:
    settings = settings or Settings()
    transcriber = transcriber or Transcriber(
        settings.whisper_model,
        settings.whisper_model_dir,
        settings.whisper_cpu_threads,
        settings.whisper_compute_type,
    )

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        if not settings.user_map:
            log.warning("REEL_USERS is empty; every request will be rejected")
        # Load the model before taking traffic so the first share isn't the slow one.
        transcriber.load()
        yield

    app = FastAPI(title="reel-to-tandoor", lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)

    @app.get("/healthz")
    def healthz():
        return {"ok": True}

    # A plain ``def`` so FastAPI runs the blocking pipeline in its thread pool.
    @app.post("/save")
    def save(body: SaveRequest, x_api_key: str | None = Header(default=None)):
        user = find_user(settings, x_api_key)
        if user is None:
            return _reply(401, "This Shortcut's API key isn't valid. Ask Saul for a new one.")
        try:
            url = normalize_url(body.url, allow_tiktok=settings.allow_tiktok)
        except InvalidLink as exc:
            return _reply(400, str(exc))

        log.info("%s is saving %s", user.name, url)
        try:
            result = save_reel(url, user, settings, transcriber)
        except NoRecipeFound as exc:
            return _reply(422, str(exc))
        except (reel_mod.ReelDownloadError, TandoorError) as exc:
            return _reply(502, str(exc))
        except Exception:
            log.exception("Unexpected failure saving %s", url)
            return _reply(500, "Something went wrong saving that reel. Try again later.")
        return _reply(200, f"Saved: {result['name']}", **result)

    return app


def main():
    import uvicorn

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    uvicorn.run(create_app(), host="0.0.0.0", port=8080, log_level="info")
