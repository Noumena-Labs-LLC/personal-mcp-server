package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/logwriter"
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
		rotating, err := logwriter.NewRotatingFile(path, maxBytes, maxBackups)
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
