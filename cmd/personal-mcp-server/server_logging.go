package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func setupServerLogging(cfg *config.Config, levelOverride, pathOverride string, maxBytesOverride int64, maxBackupsOverride int) (func(), error) {
	levelName := strings.ToLower(strings.TrimSpace(firstNonEmpty(levelOverride, cfg.ServerLogging.Level, "info")))
	var level slog.Level
	switch levelName {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", levelName)
	}
	path := strings.TrimSpace(firstNonEmpty(pathOverride, cfg.ServerLogging.Path))
	maxBytes := cfg.ServerLogging.MaxBytes
	if maxBytesOverride > 0 {
		maxBytes = maxBytesOverride
	}
	maxBackups := cfg.ServerLogging.MaxBackups
	if maxBackupsOverride >= 0 {
		maxBackups = maxBackupsOverride
	}
	writer := io.Writer(os.Stderr)
	closeFn := func() {}
	duplicateErrors := false
	if path != "" {
		rotating, err := newRotatingLogWriter(path, maxBytes, maxBackups)
		if err != nil {
			return nil, err
		}
		writer = rotating
		closeFn = func() { _ = rotating.Close() }
		duplicateErrors = true
	}
	primaryHandler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})
	var handler slog.Handler = primaryHandler
	if duplicateErrors {
		handler = duplicateErrorHandler{
			primary: primaryHandler,
			stderr:  slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}),
		}
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	log.SetOutput(writer)
	log.SetFlags(log.LstdFlags)
	slog.Debug("server logging configured", "level", levelName, "path", path, "max_bytes", maxBytes, "max_backups", maxBackups, "duplicate_errors_to_stderr", duplicateErrors)
	return closeFn, nil
}

type rotatingLogWriter struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	maxBytes   int64
	maxBackups int
	sizeBytes  int64
	queue      chan []byte
	done       chan struct{}
	closed     bool
	dropped    uint64
}

func newRotatingLogWriter(path string, maxBytes int64, maxBackups int) (*rotatingLogWriter, error) {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	if maxBackups < 0 {
		maxBackups = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // local user-selected diagnostic log path.
	if err != nil {
		return nil, err
	}
	sizeBytes := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		sizeBytes = info.Size()
	}
	w := &rotatingLogWriter{file: f, path: path, maxBytes: maxBytes, maxBackups: maxBackups, sizeBytes: sizeBytes, queue: make(chan []byte, 1024), done: make(chan struct{})}
	go w.writeLoop()
	return w, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	entry := append([]byte(nil), p...)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return 0, os.ErrClosed
	}
	select {
	case w.queue <- entry:
	default:
		w.dropped++
	}
	w.mu.Unlock()
	return len(p), nil
}

func (w *rotatingLogWriter) writeLoop() {
	defer close(w.done)
	for p := range w.queue {
		_ = w.writeSync(p)
	}
}

func (w *rotatingLogWriter) writeSync(p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	incoming := int64(len(p))
	if err := w.rotateIfNeeded(incoming); err != nil {
		return err
	}
	n, err := w.file.Write(p)
	w.sizeBytes += int64(n)
	return err
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.queue)
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingLogWriter) rotateIfNeeded(incoming int64) error {
	if w.file == nil || w.path == "" || w.maxBytes <= 0 {
		return nil
	}
	if w.sizeBytes+incoming <= w.maxBytes {
		return nil
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	if w.maxBackups > 0 {
		oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
		_ = os.Remove(oldest)
		for i := w.maxBackups - 1; i >= 1; i-- {
			old := fmt.Sprintf("%s.%d", w.path, i)
			rotated := fmt.Sprintf("%s.%d", w.path, i+1)
			_ = os.Rename(old, rotated)
		}
		_ = os.Rename(w.path, w.path+".1")
	} else {
		_ = os.Remove(w.path)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // local user-selected diagnostic log path.
	if err != nil {
		return err
	}
	w.file = f
	w.sizeBytes = 0
	return nil
}

type duplicateErrorHandler struct {
	primary slog.Handler
	stderr  slog.Handler
}

func (h duplicateErrorHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.primary.Enabled(ctx, level)
}

func (h duplicateErrorHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.primary.Handle(ctx, record); err != nil {
		return err
	}
	if record.Level >= slog.LevelError {
		return h.stderr.Handle(ctx, record)
	}
	return nil
}

func (h duplicateErrorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return duplicateErrorHandler{primary: h.primary.WithAttrs(attrs), stderr: h.stderr.WithAttrs(attrs)}
}

func (h duplicateErrorHandler) WithGroup(name string) slog.Handler {
	return duplicateErrorHandler{primary: h.primary.WithGroup(name), stderr: h.stderr.WithGroup(name)}
}
