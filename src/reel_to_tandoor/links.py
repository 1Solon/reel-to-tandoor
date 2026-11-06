"""Find, check and tidy the reel link the Shortcut sends."""

import re
from urllib.parse import urlsplit

_URL_RE = re.compile(r"https?://[^\s<>\"']+", re.IGNORECASE)

INSTAGRAM_HOSTS = {"instagram.com", "www.instagram.com", "m.instagram.com", "instagr.am", "www.instagr.am"}
TIKTOK_HOSTS = {"tiktok.com", "www.tiktok.com", "m.tiktok.com", "vm.tiktok.com", "vt.tiktok.com"}

# /reel/<code>, /reels/<code>, /p/<code>, /tv/<code>, optionally after a /<username>/ prefix.
_INSTAGRAM_PATH_RE = re.compile(r"^/(?:[A-Za-z0-9._]+/)?(reels?|p|tv)/([A-Za-z0-9_-]+)")


class InvalidLink(ValueError):
    """The text didn't contain a link we're willing to download."""


def extract_url(text: str) -> str:
    """Return the first http(s) URL in ``text``.

    The share sheet sometimes hands over "Check out this reel https://..." rather than a bare link.
    """
    match = _URL_RE.search(text or "")
    if not match:
        raise InvalidLink("That doesn't look like a link. Share a reel from Instagram.")
    return match.group(0).rstrip(".,;:!?)")


def normalize_url(text: str, allow_tiktok: bool = True) -> str:
    """Pull the link out of ``text``, check it's an Instagram (or TikTok) post and return a clean URL.

    Instagram links lose their tracking query (``?igsh=...``) and become
    ``https://www.instagram.com/reel/<code>/``, which is what gets stored as the recipe's source.
    """
    url = extract_url(text)
    parts = urlsplit(url)
    host = (parts.hostname or "").lower()

    # The returned URL is rebuilt from scratch, so userinfo, ports and plain http never reach yt-dlp.
    if host in INSTAGRAM_HOSTS:
        match = _INSTAGRAM_PATH_RE.match(parts.path)
        if not match:
            raise InvalidLink("That Instagram link isn't a reel or post.")
        kind, code = match.groups()
        kind = "reel" if kind == "reels" else kind
        return f"https://www.instagram.com/{kind}/{code}/"

    if host in TIKTOK_HOSTS and allow_tiktok:
        if parts.path.strip("/") == "":
            raise InvalidLink("That TikTok link isn't a video.")
        # Short links (vm./vt.) are redirects that yt-dlp follows; keep them as they are.
        return f"https://{host}{parts.path}"

    if allow_tiktok:
        raise InvalidLink("Only Instagram and TikTok links are supported.")
    raise InvalidLink("Only Instagram links are supported.")
