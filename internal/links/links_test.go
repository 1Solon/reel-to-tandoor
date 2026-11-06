package links

import (
	"errors"
	"testing"
)

func TestNormalizeAcceptsSupportedLinks(t *testing.T) {
	cases := map[string]string{
		"https://www.instagram.com/reel/DfXD-34Bf40/":                          "https://www.instagram.com/reel/DfXD-34Bf40/",
		"https://www.instagram.com/reel/DfXD-34Bf40/?igsh=MWx0cG1zZ2Rr":        "https://www.instagram.com/reel/DfXD-34Bf40/",
		"https://instagram.com/reels/DfXD-34Bf40":                              "https://www.instagram.com/reel/DfXD-34Bf40/",
		"http://m.instagram.com/p/ABC_123/":                                    "https://www.instagram.com/p/ABC_123/",
		"https://www.instagram.com/cameronjames.wellness/reel/DfXD-34Bf40/":    "https://www.instagram.com/reel/DfXD-34Bf40/",
		"https://www.instagram.com/tv/XYZ/":                                    "https://www.instagram.com/tv/XYZ/",
		"Look at this! https://www.instagram.com/reel/DfXD-34Bf40/?igsh=x yum": "https://www.instagram.com/reel/DfXD-34Bf40/",
		"https://www.tiktok.com/@chef/video/7300000000000000000?lang=en":       "https://www.tiktok.com/@chef/video/7300000000000000000",
		"https://vm.tiktok.com/ZMabc123/":                                      "https://vm.tiktok.com/ZMabc123/",
		"see https://www.instagram.com/reel/abc/.":                             "https://www.instagram.com/reel/abc/",
		"https://user:pass@www.instagram.com:8443/reel/DfXD-34Bf40/":           "https://www.instagram.com/reel/DfXD-34Bf40/",
	}
	for in, want := range cases {
		got, err := Normalize(in, true)
		if err != nil {
			t.Errorf("Normalize(%q) error: %v", in, err)
		} else if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRejectsOtherLinks(t *testing.T) {
	for _, in := range []string{
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
	} {
		_, err := Normalize(in, true)
		var invalid *InvalidLinkError
		if !errors.As(err, &invalid) {
			t.Errorf("Normalize(%q) error = %v, want InvalidLinkError", in, err)
		}
	}
}

func TestTikTokCanBeDisabled(t *testing.T) {
	_, err := Normalize("https://www.tiktok.com/@chef/video/1", false)
	if err == nil || err.Error() != "Only Instagram links are supported." {
		t.Fatalf("error = %v", err)
	}
}
