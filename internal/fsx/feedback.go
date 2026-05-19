package fsx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FeedbackSubmitArgs struct {
	Kind     string         `json:"kind"`
	Summary  string         `json:"summary"`
	Details  string         `json:"details"`
	Tool     string         `json:"tool"`
	Severity string         `json:"severity"`
	Context  map[string]any `json:"context"`
}

func (t *Tools) FeedbackSubmit(raw json.RawMessage) (any, error) {
	var a FeedbackSubmitArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if !t.Cfg.Feedback.Enabled {
		return nil, errors.New("feedback collection is disabled")
	}
	a.Kind = normalizeFeedbackEnum(a.Kind, "other", []string{"tool_gap", "tool_confusing", "tool_error", "docs_gap", "safety_limit", "workflow_friction", "feature_request", "other"})
	a.Severity = normalizeFeedbackEnum(a.Severity, "medium", []string{"low", "medium", "high"})
	a.Summary = strings.TrimSpace(a.Summary)
	a.Details = strings.TrimSpace(a.Details)
	if a.Summary == "" {
		return nil, errors.New("summary is required")
	}
	maxSummary := t.Cfg.Feedback.MaxSummaryBytes
	if maxSummary <= 0 {
		maxSummary = 500
	}
	maxDetails := t.Cfg.Feedback.MaxDetailsBytes
	if maxDetails <= 0 {
		maxDetails = 4000
	}
	maxContext := t.Cfg.Feedback.MaxContextBytes
	if maxContext <= 0 {
		maxContext = 12000
	}
	if len([]byte(a.Summary)) > maxSummary {
		return nil, fmt.Errorf("summary exceeds max_summary_bytes %d", maxSummary)
	}
	if len([]byte(a.Details)) > maxDetails {
		return nil, fmt.Errorf("details exceeds max_details_bytes %d", maxDetails)
	}
	contextBytes, err := json.Marshal(a.Context)
	if err != nil {
		return nil, fmt.Errorf("context must be JSON-serializable: %w", err)
	}
	if len(contextBytes) > maxContext {
		return nil, fmt.Errorf("context exceeds max_context_bytes %d", maxContext)
	}
	path := t.Cfg.Feedback.Path
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("feedback.path is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	record := map[string]any{
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
		"kind":     a.Kind,
		"severity": a.Severity,
		"summary":  a.Summary,
		"details":  a.Details,
		"tool":     strings.TrimSpace(a.Tool),
		"context":  a.Context,
		"source":   "mcp_tool",
	}
	line, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	line = append(line, '\n')
	if err := appendJSONLLine(path, line); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(line)
	return map[string]any{"ok": true, "path": path, "bytes_appended": len(line), "record_sha256": hex.EncodeToString(sum[:])}, nil
}

func normalizeFeedbackEnum(value, fallback string, allowed []string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return fallback
}

func appendJSONLLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is trusted local config, not tool input.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		buf := make([]byte, 1)
		if _, err := f.ReadAt(buf, info.Size()-1); err != nil {
			return err
		}
		if !bytes.Equal(buf, []byte("\n")) {
			if _, err := f.Seek(0, 2); err != nil {
				return err
			}
			if _, err := f.WriteString("\n"); err != nil {
				return err
			}
		}
	}
	if _, err := f.Seek(0, 2); err != nil {
		return err
	}
	_, err = f.Write(line)
	return err
}
