package reel

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

const url = "https://www.instagram.com/reel/DfXD-34Bf40/"

func TestParseInfoVideo(t *testing.T) {
	data := `{"_type": "video", "description": "Lentil bread #vegan", "channel": "cameronjames.wellness",
		"uploader_id": "68480604768", "thumbnail": "https://cdn/t.jpg", "duration": null,
		"requested_downloads": [{"filepath": "/tmp/x/DfXD-34Bf40.m4a"}]}`
	r, err := ParseInfo([]byte(data), url, 600)
	if err != nil {
		t.Fatal(err)
	}
	want := Reel{URL: url, Caption: "Lentil bread #vegan", Uploader: "cameronjames.wellness",
		ThumbnailURL: "https://cdn/t.jpg", AudioPath: "/tmp/x/DfXD-34Bf40.m4a"}
	if r != want {
		t.Errorf("got %+v", r)
	}
}

func TestParseInfoCarouselTakesCaptionFromPlaylist(t *testing.T) {
	data := `{"_type": "playlist", "description": "Two-ingredient pancakes", "entries": [
		null,
		{"uploader": "Chef", "thumbnail": "https://cdn/1.jpg", "requested_downloads": [{"filepath": "/tmp/x/1.mp4"}]}
	]}`
	r, err := ParseInfo([]byte(data), url, 600)
	if err != nil {
		t.Fatal(err)
	}
	if r.Caption != "Two-ingredient pancakes" || r.Uploader != "Chef" || r.AudioPath != "/tmp/x/1.mp4" {
		t.Errorf("got %+v", r)
	}
}

func TestParseInfoErrors(t *testing.T) {
	cases := map[string]string{
		`{"duration": 900}`:   "That video is too long to import.",
		`{"duration": 30}`:    "This post has no video to listen to.",
		`{}`:                  "This post has no video to listen to.",
		`not json`:            "Couldn't download that reel. Check the link and try again.",
		`{"entries": [null]}`: "This post has no video to listen to.",
	}
	for data, want := range cases {
		_, err := ParseInfo([]byte(data), url, 600)
		var d *DownloadError
		if !errors.As(err, &d) || err.Error() != want {
			t.Errorf("ParseInfo(%s) error = %v, want %q", data, err, want)
		}
	}
}

func TestFriendlyError(t *testing.T) {
	cases := map[string]string{
		"ERROR: [Instagram] X: There is no video in this post":                                           "This post has no video to listen to.",
		"ERROR: [Instagram] X: Requested content is not available, rate-limit reached or login required": "Instagram wouldn't let the server download that reel. It may be private, or downloads are blocked.",
		"ERROR: Unable to download webpage: HTTP Error 404":                                              "Couldn't download that reel. Check the link and try again.",
	}
	for stderr, want := range cases {
		if got := FriendlyError(stderr); got != want {
			t.Errorf("FriendlyError(%q) = %q", stderr, got)
		}
	}
}

func TestBytesToSamples(t *testing.T) {
	raw := make([]byte, 8)
	binary.LittleEndian.PutUint32(raw, math.Float32bits(0.5))
	binary.LittleEndian.PutUint32(raw[4:], math.Float32bits(-1))
	got := BytesToSamples(append(raw, 0xff)) // a stray trailing byte is ignored
	if len(got) != 2 || got[0] != 0.5 || got[1] != -1 {
		t.Errorf("got %v", got)
	}
}
