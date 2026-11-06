// Package server is the HTTP API the iOS Shortcut calls.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/1Solon/reel-to-tandoor/internal/config"
	"github.com/1Solon/reel-to-tandoor/internal/links"
	"github.com/1Solon/reel-to-tandoor/internal/recipe"
	"github.com/1Solon/reel-to-tandoor/internal/reel"
	"github.com/1Solon/reel-to-tandoor/internal/tandoor"
)

// pipelineTimeout bounds one save, so a stuck download or AI call can't hold the whisper lock forever.
const pipelineTimeout = 4 * time.Minute

// Media downloads and decodes reels. reel.Tools is the real one.
type Media interface {
	Download(ctx context.Context, url, dir string) (reel.Reel, error)
	DecodeAudio(ctx context.Context, path string) ([]float32, error)
	FetchThumbnail(ctx context.Context, url string) *reel.Image
}

// Transcriber turns 16 kHz mono samples into text. transcribe.Whisper is the real one.
type Transcriber interface {
	Transcribe(ctx context.Context, samples []float32) (string, error)
}

// Server handles requests.
type Server struct {
	cfg         config.Config
	media       Media
	transcriber Transcriber
}

// New returns a server.
func New(cfg config.Config, media Media, transcriber Transcriber) *Server {
	return &Server{cfg: cfg, media: media, transcriber: transcriber}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /save", s.save)
	return mux
}

// Result is a saved recipe.
type Result struct {
	RecipeID  int    `json:"recipe_id"`
	Name      string `json:"name"`
	RecipeURL string `json:"recipe_url"`
}

// reply is the body of every /save response. Every reply has ok and message so the Shortcut can
// show message whatever happened.
type reply struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	*Result
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, reply{OK: false, Message: message})
}

func (s *Server) findUser(apiKey string) (config.User, bool) {
	if apiKey == "" {
		return config.User{}, false
	}
	var found config.User
	ok := false
	// Compare against every key so the time taken doesn't reveal which prefix matched.
	for key, user := range s.cfg.Users {
		if subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) == 1 {
			found, ok = user, true
		}
	}
	return found, ok
}

func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	user, ok := s.findUser(r.Header.Get("X-API-Key"))
	if !ok {
		fail(w, http.StatusUnauthorized, "This Shortcut's API key isn't valid. Ask Saul for a new one.")
		return
	}

	var body struct {
		// Whatever the share sheet produced: a bare link or text with a link in it.
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "The Shortcut sent a request the server couldn't read.")
		return
	}
	url, err := links.Normalize(body.URL, s.cfg.AllowTikTok)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("saving reel", "user", user.Name, "url", url)
	// Keep going if the phone gives up waiting, so the recipe still lands in Tandoor.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), pipelineTimeout)
	defer cancel()
	result, err := s.SaveReel(ctx, url, user)

	var (
		noRecipe    *recipe.NoRecipeError
		download    *reel.DownloadError
		tandoorFail *tandoor.Error
	)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, reply{OK: true, Message: "Saved: " + result.Name, Result: &result})
	case errors.As(err, &noRecipe):
		fail(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &download), errors.As(err, &tandoorFail):
		fail(w, http.StatusBadGateway, err.Error())
	default:
		slog.Error("unexpected failure saving reel", "url", url, "err", err)
		fail(w, http.StatusInternalServerError, "Something went wrong saving that reel. Try again later.")
	}
}

// SaveReel downloads, transcribes, AI-imports and saves one reel.
func (s *Server) SaveReel(ctx context.Context, url string, user config.User) (Result, error) {
	start := time.Now()
	dir, err := os.MkdirTemp("", "reel-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	clip, err := s.media.Download(ctx, url, dir)
	if err != nil {
		return Result{}, err
	}
	samples, err := s.media.DecodeAudio(ctx, clip.AudioPath)
	if err != nil {
		return Result{}, err
	}
	transcript, err := s.transcriber.Transcribe(ctx, samples)
	if err != nil {
		return Result{}, fmt.Errorf("transcribe: %w", err)
	}
	slog.Info("downloaded and transcribed", "url", url, "took", time.Since(start).Round(100*time.Millisecond))

	text, err := recipe.ComposeAIText(clip.Caption, transcript)
	if err != nil {
		return Result{}, err
	}
	client := tandoor.New(s.cfg.TandoorURL, user.TandoorToken, s.cfg.TandoorHostHeader, s.cfg.TandoorTimeout)
	ai, err := client.AIImport(ctx, text, s.cfg.TandoorAIProviderID)
	if err != nil {
		return Result{}, err
	}
	payload, err := recipe.BuildPayload(ai, url, clip.Caption, clip.Uploader)
	if err != nil {
		return Result{}, err
	}
	saved, err := client.CreateRecipe(ctx, payload)
	if err != nil {
		return Result{}, err
	}

	if image := s.media.FetchThumbnail(ctx, clip.ThumbnailURL); image != nil {
		if err := client.UploadImage(ctx, saved.ID, image.Data, image.ContentType); err != nil {
			slog.Warn("saved recipe without an image", "recipe_id", saved.ID, "err", err)
		}
	}

	slog.Info("saved recipe", "url", url, "recipe_id", saved.ID, "user", user.Name,
		"took", time.Since(start).Round(100*time.Millisecond))
	return Result{
		RecipeID:  saved.ID,
		Name:      saved.Name,
		RecipeURL: fmt.Sprintf("%s/recipe/%d", s.cfg.TandoorPublicURL, saved.ID),
	}, nil
}
