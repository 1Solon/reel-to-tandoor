// Command reel-to-tandoor serves the API the "Save to Tandoor" iOS Shortcut calls.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/1Solon/reel-to-tandoor/internal/config"
	"github.com/1Solon/reel-to-tandoor/internal/reel"
	"github.com/1Solon/reel-to-tandoor/internal/server"
	"github.com/1Solon/reel-to-tandoor/internal/transcribe"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	if len(cfg.Users) == 0 {
		slog.Warn("REEL_USERS is empty; every request will be rejected")
	}

	// Load the model before taking traffic so the first share isn't the slow one.
	whisper, err := transcribe.New(transcribe.Options{
		ModelPath:    cfg.WhisperModel,
		VADModelPath: cfg.WhisperVADModel,
		Threads:      cfg.WhisperThreads,
		BeamSize:     cfg.WhisperBeamSize,
	})
	if err != nil {
		return err
	}
	defer whisper.Close()

	media := &reel.Tools{
		YtDlp:       cfg.YtDlpPath,
		FFmpeg:      cfg.FFmpegPath,
		CookiesFile: cfg.CookiesFile,
		MaxDuration: cfg.MaxDuration,
		MaxBytes:    cfg.MaxDownloadBytes,
		HTTP:        &http.Client{Timeout: 15 * time.Second},
	}
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(cfg, media, whisper).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		// Let an in-flight save finish; a reel takes under a minute.
		shutdown, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	slog.Info("listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
