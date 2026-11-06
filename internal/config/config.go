// Package config reads settings from environment variables.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// User is someone allowed to save recipes, and the Tandoor token their recipes are saved with.
type User struct {
	Name         string `json:"name"`
	TandoorToken string `json:"tandoor_token"`
}

// Config is the service's settings.
type Config struct {
	ListenAddr string

	// Where the service talks to Tandoor. In the cluster this is the Service, not the public URL.
	TandoorURL string
	// Sent as Host; Tandoor's ALLOWED_HOSTS rejects requests that don't name the public host.
	TandoorHostHeader string
	// Base of the link the Shortcut opens. Defaults to TandoorURL.
	TandoorPublicURL    string
	TandoorAIProviderID int
	TandoorTimeout      time.Duration

	// Users maps each API key to the Tandoor user it saves for.
	Users map[string]User

	AllowTikTok bool
	YtDlpPath   string
	FFmpegPath  string
	// Optional Netscape-format cookies file for when Instagram stops serving anonymous downloads.
	CookiesFile      string
	MaxDuration      int
	MaxDownloadBytes int64

	WhisperModel    string
	WhisperVADModel string
	WhisperThreads  int
	WhisperBeamSize int
}

// FromEnv reads the config from the environment.
func FromEnv() (Config, error) {
	return Load(os.LookupEnv)
}

// Load reads the config using lookup, which has os.LookupEnv's signature.
func Load(lookup func(string) (string, bool)) (Config, error) {
	var errs []string
	str := func(key, fallback string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		return fallback
	}
	num := func(key string, fallback int) int {
		v := str(key, "")
		if v == "" {
			return fallback
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %q isn't a whole number", key, v))
		}
		return n
	}
	boolean := func(key string, fallback bool) bool {
		v := str(key, "")
		if v == "" {
			return fallback
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %q isn't true or false", key, v))
		}
		return b
	}

	c := Config{
		ListenAddr:          str("LISTEN_ADDR", ":8080"),
		TandoorURL:          strings.TrimRight(str("TANDOOR_URL", "http://tandoor.tandoor.svc.cluster.local"), "/"),
		TandoorHostHeader:   str("TANDOOR_HOST_HEADER", ""),
		TandoorAIProviderID: num("TANDOOR_AI_PROVIDER_ID", 34),
		TandoorTimeout:      time.Duration(num("TANDOOR_TIMEOUT", 120)) * time.Second,
		AllowTikTok:         boolean("ALLOW_TIKTOK", true),
		YtDlpPath:           str("YTDLP_PATH", "yt-dlp"),
		FFmpegPath:          str("FFMPEG_PATH", "ffmpeg"),
		CookiesFile:         str("COOKIES_FILE", ""),
		MaxDuration:         num("MAX_DURATION_SECONDS", 600),
		MaxDownloadBytes:    int64(num("MAX_DOWNLOAD_MB", 100)) << 20,
		WhisperModel:        str("WHISPER_MODEL", "/models/ggml-small.bin"),
		WhisperVADModel:     str("WHISPER_VAD_MODEL", ""),
		WhisperThreads:      num("WHISPER_CPU_THREADS", 4),
		WhisperBeamSize:     num("WHISPER_BEAM_SIZE", 5),
	}
	c.TandoorPublicURL = strings.TrimRight(str("TANDOOR_PUBLIC_URL", c.TandoorURL), "/")

	c.Users = map[string]User{}
	if err := json.Unmarshal([]byte(str("REEL_USERS", "{}")), &c.Users); err != nil {
		errs = append(errs, "REEL_USERS: "+err.Error())
	}
	for key, user := range c.Users {
		if key == "" || user.TandoorToken == "" {
			errs = append(errs, fmt.Sprintf("REEL_USERS: entry %q needs an API key and a tandoor_token", user.Name))
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid config: %s", strings.Join(errs, "; "))
	}
	return c, nil
}
