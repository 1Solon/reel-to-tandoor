// Package reel downloads a reel's audio, caption and cover image with yt-dlp, and decodes the
// audio with ffmpeg.
package reel

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SampleRate is what whisper expects.
const SampleRate = 16000

const (
	thumbnailMaxBytes = 10 << 20
	browserUA         = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"
)

// DownloadError means a reel couldn't be fetched or read. Its message is safe to show to the user.
type DownloadError struct{ msg string }

func (e *DownloadError) Error() string { return e.msg }

// Reel is a downloaded reel.
type Reel struct {
	URL          string
	Caption      string
	Uploader     string
	ThumbnailURL string
	AudioPath    string
}

// Image is a fetched cover image.
type Image struct {
	Data        []byte
	ContentType string
}

// Tools runs yt-dlp and ffmpeg.
type Tools struct {
	YtDlp       string
	FFmpeg      string
	CookiesFile string
	MaxDuration int
	MaxBytes    int64
	HTTP        *http.Client
}

// FriendlyError turns yt-dlp's stderr into a message for the user.
func FriendlyError(stderr string) string {
	lowered := strings.ToLower(stderr)
	switch {
	case strings.Contains(lowered, "no video") || strings.Contains(lowered, "no formats"):
		return "This post has no video to listen to."
	case strings.Contains(lowered, "duration") || strings.Contains(lowered, "filesize") || strings.Contains(lowered, "larger than"):
		return "That video is too long to import."
	case strings.Contains(lowered, "login") || strings.Contains(lowered, "private") ||
		strings.Contains(lowered, "rate-limit") || strings.Contains(lowered, "cookies"):
		return "Instagram wouldn't let the server download that reel. It may be private, or downloads are blocked."
	}
	return "Couldn't download that reel. Check the link and try again."
}

// info is the part of yt-dlp's JSON the service uses.
type info struct {
	Description        string  `json:"description"`
	Channel            string  `json:"channel"`
	UploaderID         string  `json:"uploader_id"`
	Uploader           string  `json:"uploader"`
	Thumbnail          string  `json:"thumbnail"`
	Duration           float64 `json:"duration"`
	RequestedDownloads []struct {
		Filepath string `json:"filepath"`
	} `json:"requested_downloads"`
	Entries []*info `json:"entries"`
}

// ParseInfo reads yt-dlp's --dump-single-json output.
func ParseInfo(data []byte, url string, maxDuration int) (Reel, error) {
	var top info
	if err := json.Unmarshal(data, &top); err != nil {
		return Reel{}, &DownloadError{"Couldn't download that reel. Check the link and try again."}
	}
	// Carousel posts come back as a playlist; the caption lives on the playlist, the media on the entry.
	entry := &top
	for _, e := range top.Entries {
		if e != nil {
			entry = e
			break
		}
	}
	var path string
	for _, d := range entry.RequestedDownloads {
		if d.Filepath != "" {
			path = d.Filepath
			break
		}
	}
	if path == "" {
		if maxDuration > 0 && entry.Duration > float64(maxDuration) {
			// --match-filter rejections exit 0 without downloading anything.
			return Reel{}, &DownloadError{"That video is too long to import."}
		}
		return Reel{}, &DownloadError{"This post has no video to listen to."}
	}
	return Reel{
		URL:          url,
		Caption:      firstNonEmpty(top.Description, entry.Description),
		Uploader:     firstNonEmpty(entry.Channel, entry.UploaderID, entry.Uploader),
		ThumbnailURL: firstNonEmpty(entry.Thumbnail, top.Thumbnail),
		AudioPath:    path,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Download fetches the audio track (or the whole video if there's no separate audio) into dir.
func (t *Tools) Download(ctx context.Context, url, dir string) (Reel, error) {
	args := []string{
		// Download and print one JSON document with the caption and file path.
		"--dump-single-json", "--no-simulate",
		// Instagram serves a separate ~300 KB audio-only DASH stream; TikTok usually doesn't, so fall back.
		"--format", "bestaudio/best",
		"--output", filepath.Join(dir, "%(id)s.%(ext)s"),
		"--playlist-items", "1",
		"--max-filesize", fmt.Sprint(t.MaxBytes),
		"--match-filter", fmt.Sprintf("duration <=? %d", t.MaxDuration),
		"--ffmpeg-location", t.FFmpeg,
		"--socket-timeout", "20",
		"--retries", "2",
		"--no-progress", "--no-warnings", "--no-cache-dir",
	}
	if t.CookiesFile != "" {
		args = append(args, "--cookies", t.CookiesFile)
	}
	args = append(args, "--", url)

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, t.YtDlp, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		slog.Warn("yt-dlp failed", "url", url, "err", err, "stderr", tail(stderr.String(), 1000))
		return Reel{}, &DownloadError{FriendlyError(stderr.String())}
	}
	r, err := ParseInfo(stdout.Bytes(), url, t.MaxDuration)
	if err != nil {
		return Reel{}, err
	}
	if _, err := os.Stat(r.AudioPath); err != nil {
		return Reel{}, &DownloadError{"This post has no video to listen to."}
	}
	return r, nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// DecodeAudio decodes a file to 16 kHz mono float32 samples.
func (t *Tools) DecodeAudio(ctx context.Context, path string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, t.FFmpeg, "-nostdin", "-loglevel", "error", "-i", path,
		"-vn", "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-f", "f32le", "-")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		slog.Warn("ffmpeg failed", "path", path, "err", err, "stderr", tail(stderr.String(), 500))
		return nil, &DownloadError{"Couldn't read the audio from that reel."}
	}
	return BytesToSamples(stdout.Bytes()), nil
}

// BytesToSamples converts little-endian float32 PCM to samples.
func BytesToSamples(raw []byte) []float32 {
	samples := make([]float32, len(raw)/4)
	for i := range samples {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return samples
}

// FetchThumbnail returns the reel's cover image, or nil if it can't be fetched. A missing image
// shouldn't stop the recipe being saved, so failures are only logged.
func (t *Tools) FetchThumbnail(ctx context.Context, url string) *Image {
	if url == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", browserUA)
	client := t.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("thumbnail download failed", "err", err)
		return nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, thumbnailMaxBytes+1))
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch {
	case err != nil || resp.StatusCode != http.StatusOK:
		slog.Warn("thumbnail download failed", "status", resp.StatusCode, "err", err)
		return nil
	case !strings.HasPrefix(contentType, "image/") || len(data) > thumbnailMaxBytes:
		slog.Warn("thumbnail rejected", "content_type", contentType, "bytes", len(data))
		return nil
	}
	return &Image{Data: data, ContentType: contentType}
}

// IsDownloadError reports whether err is a DownloadError.
func IsDownloadError(err error) bool {
	var d *DownloadError
	return errors.As(err, &d)
}
