"""Download a reel's audio, caption and thumbnail with yt-dlp."""

import logging
import subprocess
from dataclasses import dataclass
from pathlib import Path

import httpx
import imageio_ffmpeg
import numpy as np
import yt_dlp

log = logging.getLogger(__name__)

SAMPLE_RATE = 16000
_THUMBNAIL_MAX_BYTES = 10 * 1024 * 1024
_BROWSER_UA = (
    "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"
)


class ReelDownloadError(Exception):
    """yt-dlp couldn't fetch the reel. The message is safe to show to the user."""


@dataclass
class Reel:
    url: str
    caption: str
    uploader: str | None
    thumbnail_url: str | None
    audio_path: Path


def ffmpeg_exe() -> str:
    return imageio_ffmpeg.get_ffmpeg_exe()


def _friendly_error(message: str) -> str:
    lowered = message.lower()
    if "no video" in lowered or "no formats" in lowered:
        return "This post has no video to listen to."
    if "duration" in lowered or "filesize" in lowered or "larger than" in lowered:
        return "That video is too long to import."
    if "login" in lowered or "private" in lowered or "rate-limit" in lowered or "cookies" in lowered:
        return "Instagram wouldn't let the server download that reel. It may be private, or downloads are blocked."
    return "Couldn't download that reel. Check the link and try again."


def download(
    url: str,
    workdir: Path,
    *,
    cookies_file: str | None = None,
    max_duration: int = 600,
    max_bytes: int = 100 * 1024 * 1024,
) -> Reel:
    """Download the audio track (or the whole video if there's no separate audio) into ``workdir``."""
    opts = {
        # Instagram serves a separate ~300 KB audio-only DASH stream; TikTok usually doesn't, so fall back.
        "format": "bestaudio/best",
        "outtmpl": str(workdir / "%(id)s.%(ext)s"),
        "playlist_items": "1",
        "max_filesize": max_bytes,
        "match_filter": yt_dlp.utils.match_filter_func(f"duration <=? {max_duration}"),
        "ffmpeg_location": ffmpeg_exe(),
        "socket_timeout": 20,
        "retries": 2,
        "quiet": True,
        "no_warnings": True,
        "noprogress": True,
    }
    if cookies_file:
        opts["cookiefile"] = cookies_file

    try:
        with yt_dlp.YoutubeDL(opts) as ydl:
            info = ydl.extract_info(url, download=True)
    except yt_dlp.utils.DownloadError as exc:
        log.warning("yt-dlp failed for %s: %s", url, exc)
        raise ReelDownloadError(_friendly_error(str(exc))) from exc

    if info is None:
        # match_filter rejections return None rather than raising.
        raise ReelDownloadError("That video is too long to import.")

    # Carousel posts come back as a playlist; the caption lives on the playlist, the media on the entry.
    entry = info
    if info.get("entries"):
        entry = next((e for e in info["entries"] if e), info)

    downloads = entry.get("requested_downloads") or []
    paths = [Path(d["filepath"]) for d in downloads if d.get("filepath")]
    if not paths or not paths[0].exists():
        raise ReelDownloadError("This post has no video to listen to.")

    return Reel(
        url=url,
        caption=info.get("description") or entry.get("description") or "",
        uploader=entry.get("channel") or entry.get("uploader_id") or entry.get("uploader"),
        thumbnail_url=entry.get("thumbnail") or info.get("thumbnail"),
        audio_path=paths[0],
    )


def decode_audio(path: Path, timeout: float = 120) -> np.ndarray:
    """Decode to 16 kHz mono float32 with ffmpeg.

    faster-whisper's own decode_audio breaks on PyAV 15+ (``open() got an unexpected keyword argument
    'metadata_errors'``), so we hand it a numpy array instead.
    """
    cmd = [ffmpeg_exe(), "-nostdin", "-loglevel", "error", "-i", str(path)]
    cmd += ["-vn", "-ac", "1", "-ar", str(SAMPLE_RATE), "-f", "f32le", "-"]
    result = subprocess.run(cmd, capture_output=True, check=False, timeout=timeout)
    if result.returncode != 0:
        log.warning("ffmpeg failed on %s: %s", path, result.stderr.decode(errors="replace")[-500:])
        raise ReelDownloadError("Couldn't read the audio from that reel.")
    return np.frombuffer(result.stdout, dtype=np.float32)


def fetch_thumbnail(url: str | None) -> tuple[bytes, str] | None:
    """Return ``(bytes, content_type)`` for the reel's cover image, or None if it can't be fetched.

    A missing image shouldn't stop the recipe being saved, so failures are only logged.
    """
    if not url:
        return None
    try:
        with httpx.Client(timeout=15, follow_redirects=True, headers={"User-Agent": _BROWSER_UA}) as client:
            response = client.get(url)
            response.raise_for_status()
    except httpx.HTTPError as exc:
        log.warning("Thumbnail download failed: %s", exc)
        return None
    content_type = response.headers.get("content-type", "").split(";")[0].strip()
    if not content_type.startswith("image/") or len(response.content) > _THUMBNAIL_MAX_BYTES:
        log.warning("Thumbnail rejected: %s, %d bytes", content_type, len(response.content))
        return None
    return response.content, content_type
