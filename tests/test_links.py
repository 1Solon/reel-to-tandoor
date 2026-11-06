import pytest

from reel_to_tandoor.links import InvalidLink, extract_url, normalize_url


@pytest.mark.parametrize(
    ("text", "expected"),
    [
        ("https://www.instagram.com/reel/DfXD-34Bf40/", "https://www.instagram.com/reel/DfXD-34Bf40/"),
        (
            "https://www.instagram.com/reel/DfXD-34Bf40/?igsh=MWx0cG1zZ2Rr",
            "https://www.instagram.com/reel/DfXD-34Bf40/",
        ),
        ("https://instagram.com/reels/DfXD-34Bf40", "https://www.instagram.com/reel/DfXD-34Bf40/"),
        ("http://m.instagram.com/p/ABC_123/", "https://www.instagram.com/p/ABC_123/"),
        (
            "https://www.instagram.com/cameronjames.wellness/reel/DfXD-34Bf40/",
            "https://www.instagram.com/reel/DfXD-34Bf40/",
        ),
        ("https://www.instagram.com/tv/XYZ/", "https://www.instagram.com/tv/XYZ/"),
        (
            "Look at this! https://www.instagram.com/reel/DfXD-34Bf40/?igsh=x yum",
            "https://www.instagram.com/reel/DfXD-34Bf40/",
        ),
        (
            "https://www.tiktok.com/@chef/video/7300000000000000000?lang=en",
            "https://www.tiktok.com/@chef/video/7300000000000000000",
        ),
        ("https://vm.tiktok.com/ZMabc123/", "https://vm.tiktok.com/ZMabc123/"),
    ],
)
def test_normalize_accepts_supported_links(text, expected):
    assert normalize_url(text) == expected


@pytest.mark.parametrize(
    "text",
    [
        "",
        "no link here",
        "https://example.com/reel/DfXD-34Bf40/",
        "https://www.instagram.com.evil.example/reel/DfXD-34Bf40/",
        "https://evil.example/?u=https://www.instagram.com/reel/x/",
        "https://www.instagram.com/cameronjames.wellness/",
        "https://www.instagram.com/",
        "https://www.tiktok.com/",
        "file:///etc/passwd",
        "https://127.0.0.1/reel/x/",
    ],
)
def test_normalize_rejects_other_links(text):
    with pytest.raises(InvalidLink):
        normalize_url(text)


def test_tiktok_can_be_disabled():
    with pytest.raises(InvalidLink, match="Only Instagram"):
        normalize_url("https://www.tiktok.com/@chef/video/1", allow_tiktok=False)


def test_extract_url_strips_trailing_punctuation():
    assert extract_url("see https://www.instagram.com/reel/abc/.") == "https://www.instagram.com/reel/abc/"
