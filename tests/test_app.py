import json
from pathlib import Path

import httpx
import numpy as np
import pytest
import respx
from fastapi.testclient import TestClient

from reel_to_tandoor import app as app_mod
from reel_to_tandoor.config import Settings
from reel_to_tandoor.reel import Reel, ReelDownloadError

TANDOOR = "http://tandoor.test"
REEL_URL = "https://www.instagram.com/reel/DfXD-34Bf40/"
FIXTURE = json.loads((Path(__file__).parent / "fixtures" / "ai_import_lentil_bread.json").read_text())


class FakeTranscriber:
    def load(self):
        pass

    def transcribe(self, audio):
        return "Soak two cups of red lentils overnight. Bake at 350 for 45 minutes."


@pytest.fixture
def settings():
    users = {"mum-key": {"name": "mum", "tandoor_token": "tda_mum"}}
    return Settings(
        tandoor_url=TANDOOR,
        tandoor_host_header="recipes.example.com",
        tandoor_public_url="https://recipes.example.com",
        REEL_USERS=json.dumps(users),
    )


@pytest.fixture
def client(settings, monkeypatch, tmp_path):
    audio = tmp_path / "a.m4a"
    audio.write_bytes(b"")
    reel = Reel(REEL_URL, "Comment yes! #food", "cameronjames.wellness", "https://cdn.test/thumb.jpg", audio)
    monkeypatch.setattr(app_mod.reel_mod, "download", lambda url, workdir, **kw: reel)
    monkeypatch.setattr(app_mod.reel_mod, "decode_audio", lambda path: np.zeros(16000, np.float32))
    monkeypatch.setattr(app_mod.reel_mod, "fetch_thumbnail", lambda url: (b"\xff\xd8jpeg", "image/jpeg"))
    with TestClient(app_mod.create_app(settings, FakeTranscriber())) as c:
        yield c


def post(client, url=REEL_URL, key="mum-key"):
    headers = {"X-API-Key": key} if key else {}
    return client.post("/save", json={"url": url}, headers=headers)


@pytest.mark.parametrize("key", [None, "", "wrong"])
def test_rejects_bad_api_key(client, key):
    response = post(client, key=key)
    assert response.status_code == 401
    assert response.json()["ok"] is False


def test_rejects_non_instagram_link(client):
    response = post(client, url="https://example.com/video")
    assert response.status_code == 400
    assert response.json() == {"ok": False, "message": "Only Instagram and TikTok links are supported."}


@respx.mock
def test_saves_recipe(client):
    ai = respx.post(f"{TANDOOR}/api/ai-import/").respond(200, json=FIXTURE)
    create = respx.post(f"{TANDOOR}/api/recipe/").respond(201, json={"id": 42, "name": "Flourless Lentil Bread"})
    image = respx.put(f"{TANDOOR}/api/recipe/42/image/").respond(200, json={})

    response = post(client, url=REEL_URL + "?igsh=abc")

    assert response.status_code == 200
    assert response.json() == {
        "ok": True,
        "message": "Saved: Flourless Lentil Bread",
        "recipe_id": 42,
        "name": "Flourless Lentil Bread",
        "recipe_url": "https://recipes.example.com/recipe/42",
    }

    ai_request = ai.calls.last.request
    assert ai_request.headers["Authorization"] == "Bearer tda_mum"
    assert ai_request.headers["Host"] == "recipes.example.com"
    body = ai_request.content.decode()
    assert 'name="ai_provider_id"\r\n\r\n34' in body
    assert "Soak two cups of red lentils" in body
    assert "#food" not in body

    saved = json.loads(create.calls.last.request.content)
    assert saved["source_url"] == REEL_URL
    assert saved["name"] == "Flourless Lentil Bread"
    assert b'filename="cover.jpg"' in image.calls.last.request.content


@respx.mock
def test_empty_ai_recipe_is_reported(client):
    empty = {"recipe": {"name": "", "steps": [], "keywords": []}, "error": False, "msg": ""}
    respx.post(f"{TANDOOR}/api/ai-import/").respond(200, json=empty)
    create = respx.post(f"{TANDOOR}/api/recipe/")

    response = post(client)

    assert response.status_code == 422
    assert response.json() == {"ok": False, "message": "No recipe found in this reel."}
    assert not create.called


@respx.mock
def test_ai_error_is_reported(client):
    respx.post(f"{TANDOOR}/api/ai-import/").respond(400, json={"error": True, "msg": "Error parsing AI results"})
    response = post(client)
    assert response.status_code == 502
    assert "couldn't read a recipe" in response.json()["message"]


@respx.mock
def test_bad_token_is_reported(client):
    respx.post(f"{TANDOOR}/api/ai-import/").respond(401, json={"detail": "Invalid token."})
    response = post(client)
    assert response.status_code == 502
    assert "rejected this user's token" in response.json()["message"]


@respx.mock
def test_image_failure_still_saves(client):
    respx.post(f"{TANDOOR}/api/ai-import/").respond(200, json=FIXTURE)
    respx.post(f"{TANDOOR}/api/recipe/").respond(201, json={"id": 7, "name": "Bread"})
    respx.put(f"{TANDOOR}/api/recipe/7/image/").mock(side_effect=httpx.ConnectError("boom"))
    assert post(client).status_code == 200


def test_download_failure_is_reported(client, monkeypatch):
    def fail(url, workdir, **kw):
        raise ReelDownloadError("Couldn't download that reel. Check the link and try again.")

    monkeypatch.setattr(app_mod.reel_mod, "download", fail)
    response = post(client)
    assert response.status_code == 502
    assert response.json()["message"].startswith("Couldn't download")


def test_healthz(client):
    assert client.get("/healthz").json() == {"ok": True}
