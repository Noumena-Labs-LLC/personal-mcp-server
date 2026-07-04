package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
)

func TestGuideCatalogIncludesSections(t *testing.T) {
	catalog := guideCatalog()
	if len(catalog) == 0 {
		t.Fatal("expected guide catalog entries")
	}
	var found bool
	for _, entry := range catalog {
		if entry["name"] != "project-config-guide" {
			continue
		}
		found = true
		sections, ok := entry["sections"].([]any)
		if ok && len(sections) > 0 {
			return
		}
		if typed, ok := entry["sections"].([]fsx.MarkdownSection); ok && len(typed) > 0 {
			return
		}
		t.Fatalf("project config guide has no sections: %#v", entry)
	}
	if !found {
		t.Fatal("project config guide not found in catalog")
	}
}

func TestGuideReadToolFullSectionAndErrors(t *testing.T) {
	full, err := guideReadTool(json.RawMessage(`{"name":"project-config"}`))
	if err != nil {
		t.Fatalf("read full guide: %v", err)
	}
	fullMap := mainResultMap(t, full)
	if !strings.Contains(mainResultString(t, fullMap, "content"), ".personal-mcp-server.toml") {
		t.Fatalf("full guide missing project config content: %#v", fullMap)
	}

	section, err := guideReadTool(json.RawMessage(`{"name":"project-config","section":"named-commands"}`))
	if err != nil {
		t.Fatalf("read guide section: %v", err)
	}
	sectionMap := mainResultMap(t, section)
	if !strings.Contains(mainResultString(t, sectionMap, "content"), "command") {
		t.Fatalf("section missing expected command guidance: %#v", sectionMap)
	}
	if _, ok := sectionMap["section"]; !ok {
		t.Fatalf("section metadata missing: %#v", sectionMap)
	}

	if _, err := guideReadTool(json.RawMessage(`{"name":"missing-guide"}`)); err == nil {
		t.Fatal("expected unknown guide error")
	}
	if _, err := guideReadTool(json.RawMessage(`{"name":"project-config","section":"missing-section"}`)); err == nil {
		t.Fatal("expected unknown section error")
	}
}

func mainResultMap(t *testing.T, out any) map[string]any {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", out)
	}
	return m
}

func mainResultString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("expected %q to be string, got %T", key, m[key])
	}
	return v
}
