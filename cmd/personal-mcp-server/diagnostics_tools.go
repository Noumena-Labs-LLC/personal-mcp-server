package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

type slowToolRecord struct {
	Line          string `json:"line"`
	Event         string `json:"event,omitempty"`
	Tool          string `json:"tool,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	ThresholdMS   int64  `json:"threshold_ms,omitempty"`
	OK            *bool  `json:"ok,omitempty"`
	RequestBytes  int64  `json:"request_bytes,omitempty"`
	ResponseBytes int64  `json:"response_bytes,omitempty"`
	Error         string `json:"error,omitempty"`
}

func diagnosticsRecentSlowToolsTool(cfg *config.Config) func(json.RawMessage) (any, error) {
	return func(raw json.RawMessage) (any, error) {
		var a struct {
			Limit int    `json:"limit"`
			Since string `json:"since"`
			Path  string `json:"path"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
		}
		if a.Limit <= 0 || a.Limit > 200 {
			a.Limit = 20
		}
		path := strings.TrimSpace(a.Path)
		if path == "" {
			path = cfg.ServerLogging.Path
		}
		if strings.TrimSpace(path) == "" {
			return map[string]any{"ok": false, "error": "server_logging.path is not configured", "records": []any{}}, nil
		}
		records, scanned, err := readRecentSlowToolRecords(path, a.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "path": path, "limit": a.Limit, "since": a.Since, "scanned_lines": scanned, "records": records}, nil
	}
}

func readRecentSlowToolRecords(path string, limit int) ([]slowToolRecord, int, error) {
	b, err := os.ReadFile(path) //nolint:gosec // local configured diagnostic log path.
	if err != nil {
		return nil, 0, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	records := []slowToolRecord{}
	for i := len(lines) - 1; i >= 0 && len(records) < limit; i-- {
		line := lines[i]
		if !strings.Contains(line, "tool_call_slow") && !strings.Contains(line, "tool_call_very_slow") {
			continue
		}
		records = append(records, parseSlowToolLogLine(line))
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, len(lines), nil
}

func parseSlowToolLogLine(line string) slowToolRecord {
	fields := parseSlogTextFields(line)
	rec := slowToolRecord{Line: line, Event: fields["event"], Tool: fields["tool"], Error: fields["error"]}
	rec.DurationMS = parseInt64Field(fields["duration_ms"])
	rec.ThresholdMS = parseInt64Field(fields["threshold_ms"])
	rec.RequestBytes = parseInt64Field(fields["request_bytes"])
	rec.ResponseBytes = parseInt64Field(fields["response_bytes"])
	if v, ok := fields["ok"]; ok {
		b := v == "true"
		rec.OK = &b
	}
	return rec
}

func parseSlogTextFields(line string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Fields(line) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		out[k] = v
	}
	return out
}

func parseInt64Field(value string) int64 {
	if value == "" {
		return 0
	}
	n, _ := strconv.ParseInt(value, 10, 64)
	return n
}
