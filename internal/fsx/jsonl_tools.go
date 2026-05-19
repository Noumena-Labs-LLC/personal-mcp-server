package fsx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type JSONLInfoArgs struct {
	Path      string `json:"path"`
	Cwd       string `json:"cwd"`
	PathMode  string `json:"path_mode"`
	Sample    int    `json:"sample"`
	MaxFields int    `json:"max_fields"`
}

type JSONLReadArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

type JSONLTailArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Records  int    `json:"records"`
}

type JSONLFilterArgs struct {
	Path           string             `json:"path"`
	Cwd            string             `json:"cwd"`
	PathMode       string             `json:"path_mode"`
	Where          map[string]any     `json:"where"`
	Contains       map[string]string  `json:"contains"`
	Exists         []string           `json:"exists"`
	Missing        []string           `json:"missing"`
	NumericGTE     map[string]float64 `json:"numeric_gte"`
	NumericLTE     map[string]float64 `json:"numeric_lte"`
	TimestampField string             `json:"timestamp_field"`
	TSGTE          string             `json:"ts_gte"`
	TSLTE          string             `json:"ts_lte"`
	Limit          int                `json:"limit"`
	Reverse        bool               `json:"reverse"`
}

type JSONLValidateArgs struct {
	Path        string `json:"path"`
	Cwd         string `json:"cwd"`
	PathMode    string `json:"path_mode"`
	LimitErrors int    `json:"limit_errors"`
}

func (t *Tools) loadJSON(path, cwd, pathMode string) (root any, displayPath string, size int64, err error) {
	p, displayPath, err := t.resolvePath(path, cwd, pathMode)
	if err != nil {
		return nil, "", 0, err
	}
	if err := t.enforceFilePolicy("read", displayPath, p, map[string]any{"cwd": cwd, "structured": "json"}); err != nil {
		return nil, "", 0, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, "", 0, err
	}
	if info.IsDir() {
		return nil, "", 0, errors.New("cannot read a directory")
	}
	if info.Size() > t.Cfg.Limits.MaxReadBytes {
		return nil, "", 0, fmt.Errorf("file too large: %d bytes", info.Size())
	}
	b, err := os.ReadFile(p) //nolint:gosec // path is resolved and policy-checked before reading.
	if err != nil {
		return nil, "", 0, err
	}
	if bytes.ContainsRune(b, '\x00') {
		return nil, "", 0, errors.New("refusing binary file")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, "", 0, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, "", 0, errors.New("invalid JSON: multiple top-level values")
	}
	return v, displayPath, info.Size(), nil
}

func (t *Tools) JSONGet(raw json.RawMessage) (any, error) {
	var a jsonPathArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	root, displayPath, size, err := t.loadJSON(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	v, err := resolveJSONPointer(root, a.Pointer)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "pointer": a.Pointer, "type": jsonType(v), "value": v, "bytes_read": size}, nil
}

func (t *Tools) openJSONL(path, cwd, pathMode string) (file *os.File, displayPath string, size int64, err error) {
	p, displayPath, err := t.resolvePath(path, cwd, pathMode)
	if err != nil {
		return nil, "", 0, err
	}
	if err := t.enforceFilePolicy("read", displayPath, p, map[string]any{"cwd": cwd, "structured": "jsonl"}); err != nil {
		return nil, "", 0, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, "", 0, err
	}
	if info.IsDir() {
		return nil, "", 0, errors.New("cannot read a directory")
	}
	if info.Size() > t.Cfg.Limits.MaxSearchFileBytes {
		return nil, "", 0, fmt.Errorf("file too large for structured scan: %d bytes", info.Size())
	}
	f, err := os.Open(p) //nolint:gosec // path is resolved and policy-checked before opening.
	if err != nil {
		return nil, "", 0, err
	}
	return f, displayPath, info.Size(), nil
}

func (t *Tools) JSONLRead(raw json.RawMessage) (any, error) {
	var a JSONLReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	if a.Limit <= 0 || a.Limit > 500 {
		a.Limit = 100
	}
	f, displayPath, size, err := t.openJSONL(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	records, scanned, malformed, empty, err := readJSONLPage(f, a.Offset, a.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "offset": a.Offset, "limit": a.Limit, "returned": len(records), "scanned_lines": scanned, "empty_skipped": empty, "malformed_skipped": malformed, "records": records, "bytes_read": size}, nil
}

func (t *Tools) JSONLTail(raw json.RawMessage) (any, error) {
	var a JSONLTailArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Records <= 0 || a.Records > 500 {
		a.Records = 50
	}
	f, displayPath, size, err := t.openJSONL(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), int(t.Cfg.Limits.MaxReadBytes))
	records := []any{}
	malformed, empty, scanned := 0, 0, 0
	for scanner.Scan() {
		scanned++
		rec, status := parseJSONLLine(scanner.Text())
		switch status {
		case "empty":
			empty++
		case "malformed":
			malformed++
		default:
			records = append(records, rec)
			if len(records) > a.Records {
				records = records[1:]
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "records_requested": a.Records, "records_returned": len(records), "scanned_lines": scanned, "empty_skipped": empty, "malformed_skipped": malformed, "records": records, "bytes_read": size}, nil
}

func (t *Tools) JSONLInfo(raw json.RawMessage) (any, error) {
	var a JSONLInfoArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Sample <= 0 || a.Sample > 10000 {
		a.Sample = 1000
	}
	if a.MaxFields <= 0 || a.MaxFields > 1000 {
		a.MaxFields = 200
	}
	f, displayPath, size, err := t.openJSONL(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	type fieldInfo struct {
		Types   map[string]bool
		Present int
	}
	fields := map[string]*fieldInfo{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), int(t.Cfg.Limits.MaxReadBytes))
	valid, malformed, empty, lines := 0, 0, 0, 0
	for scanner.Scan() {
		lines++
		rec, status := parseJSONLLine(scanner.Text())
		if status == "empty" {
			empty++
			continue
		}
		if status == "malformed" {
			malformed++
			continue
		}
		valid++
		if obj, ok := rec.(map[string]any); ok {
			for k, v := range obj {
				if _, ok := fields[k]; !ok && len(fields) < a.MaxFields {
					fields[k] = &fieldInfo{Types: map[string]bool{}}
				}
				if fi := fields[k]; fi != nil {
					fi.Present++
					fi.Types[jsonType(v)] = true
				}
			}
		}
		if valid >= a.Sample {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	outFields := map[string]any{}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		types := []string{}
		for typ := range fields[k].Types {
			types = append(types, typ)
		}
		sort.Strings(types)
		outFields[k] = map[string]any{"types": types, "present": fields[k].Present}
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "bytes": size, "sample_limit": a.Sample, "sampled_valid_records": valid, "scanned_lines": lines, "empty_sampled": empty, "malformed_sampled": malformed, "fields": outFields, "fields_truncated": len(fields) >= a.MaxFields}, nil
}

func (t *Tools) JSONLFilter(raw json.RawMessage) (any, error) {
	var a JSONLFilterArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Limit <= 0 || a.Limit > t.Cfg.Limits.MaxSearchResults {
		a.Limit = t.Cfg.Limits.MaxSearchResults
	}
	if a.TimestampField == "" {
		a.TimestampField = "ts"
	}
	var gte, lte time.Time
	var err error
	if a.TSGTE != "" {
		gte, err = time.Parse(time.RFC3339, a.TSGTE)
		if err != nil {
			return nil, fmt.Errorf("ts_gte must be RFC3339: %w", err)
		}
	}
	if a.TSLTE != "" {
		lte, err = time.Parse(time.RFC3339, a.TSLTE)
		if err != nil {
			return nil, fmt.Errorf("ts_lte must be RFC3339: %w", err)
		}
	}
	f, displayPath, size, err := t.openJSONL(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), int(t.Cfg.Limits.MaxReadBytes))
	records := []any{}
	scanned, valid, matched, malformed, empty := 0, 0, 0, 0, 0
	for scanner.Scan() {
		scanned++
		rec, status := parseJSONLLine(scanner.Text())
		if status == "empty" {
			empty++
			continue
		}
		if status == "malformed" {
			malformed++
			continue
		}
		valid++
		obj, _ := rec.(map[string]any)
		if jsonlRecordMatches(obj, a, gte, lte) {
			matched++
			if a.Reverse {
				records = append(records, rec)
				if len(records) > a.Limit {
					records = records[1:]
				}
			} else if len(records) < a.Limit {
				records = append(records, rec)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if a.Reverse {
		for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
			records[i], records[j] = records[j], records[i]
		}
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "scanned_lines": scanned, "valid_records": valid, "matched": matched, "returned": len(records), "limit": a.Limit, "reverse": a.Reverse, "empty_skipped": empty, "malformed_skipped": malformed, "records": records, "bytes_read": size}, nil
}

func (t *Tools) JSONLValidate(raw json.RawMessage) (any, error) {
	var a JSONLValidateArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.LimitErrors <= 0 || a.LimitErrors > 50 {
		a.LimitErrors = 10
	}
	f, displayPath, size, err := t.openJSONL(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), int(t.Cfg.Limits.MaxReadBytes))
	lines, valid, malformed, empty := 0, 0, 0, 0
	samples := []map[string]any{}
	for scanner.Scan() {
		lines++
		_, status := parseJSONLLine(scanner.Text())
		switch status {
		case "empty":
			empty++
		case "malformed":
			malformed++
			if len(samples) < a.LimitErrors {
				samples = append(samples, map[string]any{"line": lines})
			}
		default:
			valid++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "path": displayPath, "cwd": a.Cwd, "valid": malformed == 0, "lines": lines, "valid_records": valid, "empty_lines": empty, "malformed_lines": malformed, "error_samples": samples, "bytes_read": size}, nil
}

func readJSONLPage(r io.Reader, offset, limit int) (records []any, scanned, malformed, empty int, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	records = []any{}
	validSeen := 0
	for scanner.Scan() {
		scanned++
		rec, status := parseJSONLLine(scanner.Text())
		if status == "empty" {
			empty++
			continue
		}
		if status == "malformed" {
			malformed++
			continue
		}
		if validSeen >= offset && len(records) < limit {
			records = append(records, rec)
		}
		validSeen++
		if len(records) >= limit {
			break
		}
	}
	return records, scanned, malformed, empty, scanner.Err()
}

func parseJSONLLine(line string) (record any, status string) {
	if strings.TrimSpace(line) == "" {
		return nil, "empty"
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, "malformed"
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, "malformed"
	}
	return v, "valid"
}

func jsonlRecordMatches(obj map[string]any, a JSONLFilterArgs, gte, lte time.Time) bool {
	if obj == nil {
		return false
	}
	for k, want := range a.Where {
		got, ok := getJSONPathValue(obj, k)
		if !ok || !jsonEqualSimple(got, want) {
			return false
		}
	}
	for k, needle := range a.Contains {
		got, ok := getJSONPathValue(obj, k)
		if !ok || !strings.Contains(fmt.Sprint(got), needle) {
			return false
		}
	}
	for _, k := range a.Exists {
		if _, ok := getJSONPathValue(obj, k); !ok {
			return false
		}
	}
	for _, k := range a.Missing {
		if _, ok := getJSONPathValue(obj, k); ok {
			return false
		}
	}
	for k, minValue := range a.NumericGTE {
		got, ok := jsonNumberAsFloat(getJSONPathValue(obj, k))
		if !ok || got < minValue {
			return false
		}
	}
	for k, maxValue := range a.NumericLTE {
		got, ok := jsonNumberAsFloat(getJSONPathValue(obj, k))
		if !ok || got > maxValue {
			return false
		}
	}
	if !gte.IsZero() || !lte.IsZero() {
		raw, ok := getJSONPathValue(obj, a.TimestampField)
		if !ok {
			return false
		}
		ts, err := time.Parse(time.RFC3339, fmt.Sprint(raw))
		if err != nil {
			return false
		}
		if !gte.IsZero() && ts.Before(gte) {
			return false
		}
		if !lte.IsZero() && ts.After(lte) {
			return false
		}
	}
	return true
}
