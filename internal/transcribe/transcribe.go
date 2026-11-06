// Package transcribe does speech to text with whisper.cpp on the CPU.
package transcribe

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	whisper "github.com/ggerganov/whisper.cpp/bindings/go"
)

// Options configure the transcriber.
type Options struct {
	ModelPath string
	// VADModelPath, when set, skips music and silence with Silero VAD before decoding.
	VADModelPath string
	Threads      int
	// BeamSize 1 decodes greedily; more uses beam search.
	BeamSize int
}

// Whisper loads the model once and runs one transcription at a time. Whisper already uses
// every thread it's given, so running two at once only makes both slower.
//
// It uses the low-level bindings because the high-level pkg/whisper API always decodes
// greedily: its SetBeamSize stores a beam size that greedy sampling never reads.
type Whisper struct {
	ctx    *whisper.Context
	params whisper.Params
	mu     sync.Mutex
}

// New loads the model.
func New(opts Options) (*Whisper, error) {
	start := time.Now()
	ctx := whisper.Whisper_init(opts.ModelPath)
	if ctx == nil {
		return nil, errors.New("couldn't load whisper model " + opts.ModelPath)
	}

	strategy := whisper.SAMPLING_GREEDY
	if opts.BeamSize > 1 {
		strategy = whisper.SAMPLING_BEAM_SEARCH
	}
	params := ctx.Whisper_full_default_params(strategy)
	if opts.BeamSize > 1 {
		params.SetBeamSize(opts.BeamSize)
	}
	// -1 means detect the language; the default is English.
	if err := params.SetLanguage(-1); err != nil {
		ctx.Whisper_free()
		return nil, err
	}
	params.SetThreads(opts.Threads)
	params.SetTranslate(false)
	params.SetNoContext(true)
	params.SetPrintProgress(false)
	params.SetPrintRealtime(false)
	params.SetPrintSpecial(false)
	params.SetPrintTimestamps(false)
	if opts.VADModelPath != "" {
		params.SetVAD(true)
		params.SetVADModelPath(opts.VADModelPath)
	}

	slog.Info("loaded whisper model", "path", opts.ModelPath, "beam_size", max(opts.BeamSize, 1),
		"threads", opts.Threads, "vad", opts.VADModelPath != "", "took", time.Since(start).Round(time.Millisecond))
	return &Whisper{ctx: ctx, params: params}, nil
}

// Close frees the model.
func (w *Whisper) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ctx.Whisper_free()
}

// Transcribe returns the text spoken in 16 kHz mono samples, in the language it detects.
func (w *Whisper) Transcribe(ctx context.Context, samples []float32) (string, error) {
	if len(samples) == 0 {
		return "", nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	start := time.Now()
	// Stops before the next encoder pass if the request times out.
	keepGoing := func() bool { return ctx.Err() == nil }
	if err := w.ctx.Whisper_full(w.params, samples, keepGoing, nil, nil); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var parts []string
	for i := range w.ctx.Whisper_full_n_segments() {
		if text := strings.TrimSpace(w.ctx.Whisper_full_get_segment_text(i)); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, " ")
	slog.Info("transcribed audio",
		"seconds", len(samples)/16000, "language", whisper.Whisper_lang_str(w.ctx.Whisper_full_lang_id()),
		"chars", len(text), "took", time.Since(start).Round(time.Millisecond))
	return text, nil
}
