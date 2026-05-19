package main

import (
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func TestToolCatalogProgressiveReveal(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.SearchText.Enabled = true
	cfg.Tools.ReadFile.Enabled = true
	cfg.Tools.RunCommand.Enabled = true

	summary := toolCatalogCategories(cfg)
	if got, ok := summary["count"].(int); !ok || got == 0 {
		t.Fatalf("expected category count, got %#v", summary["count"])
	}

	category, err := buildToolCatalogCategory(cfg, toolCatalogCategoryArgs{Category: "project_workflow"})
	if err != nil {
		t.Fatalf("toolCatalogCategory returned error: %v", err)
	}
	if category["category"] != "project_workflow" {
		t.Fatalf("unexpected category %#v", category["category"])
	}
	if got, ok := category["count"].(int); !ok || got == 0 {
		t.Fatalf("expected project workflow tools, got %#v", category["count"])
	}
}

func TestToolCatalogCategoryFiltersDisabledTools(t *testing.T) {
	cfg := &config.Config{}
	withoutDisabled, err := buildToolCatalogCategory(cfg, toolCatalogCategoryArgs{Category: "filesystem_read"})
	if err != nil {
		t.Fatalf("toolCatalogCategory returned error: %v", err)
	}
	withoutDisabledTools, ok := withoutDisabled["tools"].([]toolCatalogEntry)
	if !ok {
		t.Fatalf("expected []toolCatalogEntry, got %#v", withoutDisabled["tools"])
	}
	for _, tool := range withoutDisabledTools {
		if !tool.Enabled {
			t.Fatalf("disabled tool returned without include_disabled: %#v", tool)
		}
	}

	withDisabled, err := buildToolCatalogCategory(cfg, toolCatalogCategoryArgs{Category: "filesystem_read", IncludeDisabled: true, Query: "fs_find"})
	if err != nil {
		t.Fatalf("toolCatalogCategory returned error: %v", err)
	}
	tools, ok := withDisabled["tools"].([]toolCatalogEntry)
	if !ok {
		t.Fatalf("expected []toolCatalogEntry, got %#v", withDisabled["tools"])
	}
	if len(tools) != 1 || tools[0].Name != "fs_find" || tools[0].RequiresFeature != "native_find" {
		t.Fatalf("expected disabled fs_find with native_find requirement, got %#v", tools)
	}
}
