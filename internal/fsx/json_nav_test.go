package fsx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func TestJSONNavigationHandlesRootTypesAndPointers(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"items":[{"name":"alpha"},{"name":"beta"}],"a/b":{"~key":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.JSONGet(json.RawMessage(`{"path":"data.json","pointer":"/a~1b/~0key"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, out)["type"] != "boolean" {
		t.Fatalf("expected boolean get result: %#v", out)
	}
	keys, err := tools.JSONKeys(json.RawMessage(`{"path":"data.json","pointer":"/items","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, keys)["total"] != 2 {
		t.Fatalf("expected array length metadata: %#v", keys)
	}
	slice, err := tools.JSONSlice(json.RawMessage(`{"path":"data.json","pointer":"/items","offset":1,"limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, slice)["returned"] != nil {
		t.Fatalf("json_slice should not invent returned field: %#v", slice)
	}

	if err := os.WriteFile(filepath.Join(root, "scalar.json"), []byte(`"hello"`), 0o600); err != nil {
		t.Fatal(err)
	}
	scalar, err := tools.JSONGet(json.RawMessage(`{"path":"scalar.json","pointer":""}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, scalar)
	if m["type"] != "string" || m["value"] != "hello" {
		t.Fatalf("expected scalar root get: %#v", m)
	}
}

func TestJSONOutlineAndSearch(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`[{"name":"alpha"},{"name":"beta"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	outline, err := tools.JSONOutline(json.RawMessage(`{"path":"data.json","max_depth":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, outline)["root_type"] != "array" {
		t.Fatalf("expected array root outline: %#v", outline)
	}
	search, err := tools.JSONSearch(json.RawMessage(`{"path":"data.json","query":"beta","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, search)["returned"] != 1 {
		t.Fatalf("expected one search match: %#v", search)
	}
}

func TestJSONLNavigationAndValidation(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	content := "{\"ts\":\"2026-05-17T00:00:00Z\",\"tool\":\"a\",\"ok\":true}\n\nnot-json\n{\"ts\":\"2026-05-18T00:00:00Z\",\"tool\":\"b\",\"ok\":false,\"error\":\"boom\"}\n"
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := tools.JSONLInfo(json.RawMessage(`{"path":"events.jsonl","sample":10}`))
	if err != nil {
		t.Fatal(err)
	}
	infoMap := resultMap(t, info)
	if infoMap["sampled_valid_records"] != 2 {
		t.Fatalf("expected two valid sampled records: %#v", info)
	}
	if counts := resultIntMap(t, infoMap["ignored_counts"], "ignored_counts"); counts["empty"] != 1 || counts["malformed"] != 1 {
		t.Fatalf("expected empty/malformed ignored counts: %#v", counts)
	}
	filtered, err := tools.JSONLFilter(json.RawMessage(`{"path":"events.jsonl","where":{"ok":false},"exists":["error"],"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	filteredMap := resultMap(t, filtered)
	if filteredMap["returned"] != 1 {
		t.Fatalf("expected one filtered record: %#v", filtered)
	}
	if counts := resultIntMap(t, filteredMap["ignored_counts"], "ignored_counts"); counts["empty"] != 1 || counts["malformed"] != 1 {
		t.Fatalf("expected empty/malformed ignored counts for filter: %#v", counts)
	}
	validation, err := tools.JSONLValidate(json.RawMessage(`{"path":"events.jsonl"}`))
	if err != nil {
		t.Fatal(err)
	}
	validationMap := resultMap(t, validation)
	if validationMap["malformed_lines"] != 1 {
		t.Fatalf("expected one malformed line: %#v", validation)
	}
	if counts := resultIntMap(t, validationMap["ignored_counts"], "ignored_counts"); counts["empty"] != 1 || counts["malformed"] != 1 {
		t.Fatalf("expected empty/malformed ignored counts for validation: %#v", counts)
	}
}

func TestFeedbackSubmitWritesConfiguredJSONL(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Feedback = config.FeedbackConfig{Enabled: true, Path: filepath.Join(root, "feedback", "feedback.jsonl"), MaxSummaryBytes: 100, MaxDetailsBytes: 1000, MaxContextBytes: 1000}
	cfg.Tools.FeedbackSubmit.Enabled = true
	tools := NewTools(cfg, nil)
	if _, err := tools.FeedbackSubmit(json.RawMessage(`{"kind":"tool_gap","summary":"Need exists filter","tool":"jsonl_filter","context":{"workflow":"audit"}}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfg.Feedback.Path) //nolint:gosec // test path is inside temp dir.
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(b[:len(b)-1], &record); err != nil {
		t.Fatal(err)
	}
	if record["kind"] != "tool_gap" || record["summary"] != "Need exists filter" {
		t.Fatalf("unexpected feedback record: %#v", record)
	}
}

func TestJSONSearchTypeFilterAndPointerPrefix(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"items":[{"name":"alpha","count":2},{"name":"beta","count":3}],"other":"beta"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	search, err := tools.JSONSearch(json.RawMessage(`{"path":"data.json","query":"beta","pointer_prefix":"/items","type_filter":["string"],"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, search)["returned"] != 1 {
		t.Fatalf("expected one prefixed string match: %#v", search)
	}
}

func TestJSONLFilterNestedAndNumericRanges(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	content := "{\"event\":{\"kind\":\"slow\"},\"duration_ms\":4000}\n{\"event\":{\"kind\":\"fast\"},\"duration_ms\":25}\n"
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	filtered, err := tools.JSONLFilter(json.RawMessage(`{"path":"events.jsonl","where":{"event.kind":"slow"},"numeric_gte":{"duration_ms":3000},"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, filtered)["returned"] != 1 {
		t.Fatalf("expected one nested numeric match: %#v", filtered)
	}
}

func TestJSONSearchTypeFilterExcludesNonMatchingScalars(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"items":[{"name":"beta","count":3}],"count":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	search, err := tools.JSONSearch(json.RawMessage(`{"path":"data.json","query":"3","type_filter":["string"],"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, search)["returned"] != 0 {
		t.Fatalf("expected numeric matches to be excluded by string type_filter: %#v", search)
	}
}

func TestJSONLFilterNestedContainsMissingAndNumericLTE(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	content := "{\"event\":{\"kind\":\"slow-read\"},\"duration_ms\":2999}\n{\"event\":{\"kind\":\"slow-write\"},\"duration_ms\":5000,\"error\":\"timeout\"}\n"
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	filtered, err := tools.JSONLFilter(json.RawMessage(`{"path":"events.jsonl","contains":{"event.kind":"slow"},"missing":["error"],"numeric_lte":{"duration_ms":3000},"limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultMap(t, filtered)["returned"] != 1 {
		t.Fatalf("expected one nested contains/missing/lte match: %#v", filtered)
	}
}
