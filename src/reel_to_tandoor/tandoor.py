"""The few Tandoor API calls the service needs."""

import logging
from typing import Any

import httpx

log = logging.getLogger(__name__)


class TandoorError(Exception):
    """A Tandoor call failed. The message is safe to show to the user."""


class TandoorClient:
    def __init__(self, base_url: str, token: str, host_header: str | None = None, timeout: float = 120.0):
        headers = {"Authorization": f"Bearer {token}", "Accept": "application/json"}
        if host_header:
            headers["Host"] = host_header
        self._client = httpx.Client(base_url=base_url.rstrip("/"), headers=headers, timeout=timeout)

    def close(self):
        self._client.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()

    def _check(self, response: httpx.Response, action: str) -> None:
        if response.status_code in (401, 403):
            raise TandoorError("Tandoor rejected this user's token. Ask Saul to update it.")
        if response.status_code == 429:
            raise TandoorError("Too many recipe imports this hour. Try again later.")
        if response.is_error:
            log.warning("Tandoor %s failed: %s %s", action, response.status_code, response.text[:1000])
            raise TandoorError(f"Tandoor couldn't {action} (HTTP {response.status_code}).")

    def ai_import(self, text: str, ai_provider_id: int) -> dict[str, Any]:
        """Send text to ``/api/ai-import/`` and return the unsaved, RecipeFromSource-shaped recipe."""
        # The endpoint only takes multipart; the empty recipe_id and file fields match what the web UI sends.
        form = {"recipe_id": "", "file": "", "text": text, "ai_provider_id": str(ai_provider_id)}
        try:
            response = self._client.post("/api/ai-import/", files={k: (None, v) for k, v in form.items()})
        except httpx.TimeoutException as exc:
            raise TandoorError("Tandoor's AI took too long to answer. Try again in a minute.") from exc
        except httpx.HTTPError as exc:
            log.warning("Tandoor AI import request failed: %s", exc)
            raise TandoorError("Couldn't reach Tandoor.") from exc

        try:
            body = response.json()
        except ValueError:
            body = {}
        if isinstance(body, dict) and body.get("error"):
            log.warning("Tandoor AI import error: %s", str(body.get("msg"))[:1000])
            raise TandoorError("Tandoor's AI couldn't read a recipe from this reel.")
        self._check(response, "run the AI import")
        recipe = body.get("recipe") if isinstance(body, dict) else None
        if not isinstance(recipe, dict):
            raise TandoorError("Tandoor's AI couldn't read a recipe from this reel.")
        return recipe

    def create_recipe(self, payload: dict[str, Any]) -> dict[str, Any]:
        try:
            response = self._client.post("/api/recipe/", json=payload)
        except httpx.HTTPError as exc:
            log.warning("Tandoor create recipe request failed: %s", exc)
            raise TandoorError("Couldn't reach Tandoor.") from exc
        self._check(response, "save the recipe")
        return response.json()

    def upload_image(self, recipe_id: int, image: bytes, content_type: str) -> None:
        """Attach an image with ``PUT /api/recipe/{id}/image/``.

        Tandoor checks the file name's extension, so it has to match the content type.
        """
        extension = {"image/png": "png", "image/webp": "webp"}.get(content_type, "jpg")
        files = {"image": (f"cover.{extension}", image, content_type)}
        try:
            response = self._client.put(f"/api/recipe/{recipe_id}/image/", files=files)
        except httpx.HTTPError as exc:
            log.warning("Tandoor image upload request failed: %s", exc)
            raise TandoorError("Couldn't upload the recipe image.") from exc
        self._check(response, "save the recipe image")
