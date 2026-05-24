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

func TestToolCatalogBatchReturnsSelectedCategoriesAndSummaries(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.SearchText.Enabled = true
	cfg.Tools.RunCommand.Enabled = true

	batch, err := buildToolCatalogBatch(cfg, toolCatalogBatchArgs{
		Categories:       []string{"filesystem_read", "project_workflow"},
		IncludeSummaries: true,
	})
	if err != nil {
		t.Fatalf("toolCatalogBatch returned error: %v", err)
	}
	if got, ok := batch["count"].(int); !ok || got != 2 {
		t.Fatalf("expected count=2, got %#v", batch["count"])
	}
	categories, ok := batch["categories"].([]map[string]any)
	if !ok || len(categories) != 2 {
		t.Fatalf("expected two categories, got %#v", batch["categories"])
	}
	if categories[0]["category"] != "filesystem_read" || categories[1]["category"] != "project_workflow" {
		t.Fatalf("unexpected categories %#v", categories)
	}
	summaries, ok := batch["summaries"].(map[string]any)
	if !ok {
		t.Fatalf("expected summaries map, got %#v", batch["summaries"])
	}
	if got, ok := summaries["count"].(int); !ok || got == 0 {
		t.Fatalf("expected non-empty summaries, got %#v", summaries["count"])
	}
}
