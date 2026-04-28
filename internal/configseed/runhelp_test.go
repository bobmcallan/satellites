package configseed

import (
	"context"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

const sampleHelpMD = `---
title: Agents
slug: agents
order: 20
tags: [help]
---
# Agents

Body content for the agents help page.
`

// TestRunHelp_LoadsAndUpserts covers AC4 + AC6: the help loader walks
// helpDir, parses each markdown, and upserts each as
// (scope=system, type=help, name=slug). story_cc5c67a9.
func TestRunHelp_LoadsAndUpserts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents.md", sampleHelpMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	summary, err := RunHelp(context.Background(), docs, dir, "wksp_sys", "system", now)
	if err != nil {
		t.Fatalf("RunHelp: %v", err)
	}
	if summary.Loaded != 1 || summary.Created != 1 {
		t.Errorf("summary loaded=%d created=%d, want 1/1", summary.Loaded, summary.Created)
	}

	got, err := docs.GetByName(context.Background(), "", "agents", nil)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Type != document.TypeHelp {
		t.Errorf("type = %q, want %q", got.Type, document.TypeHelp)
	}
	if got.Scope != document.ScopeSystem {
		t.Errorf("scope = %q, want system", got.Scope)
	}
	if got.Body == "" {
		t.Errorf("body empty")
	}
}

// TestRunHelp_Idempotent covers AC6: a second pass with unchanged files
// produces zero creates/updates.
func TestRunHelp_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents.md", sampleHelpMD)

	docs := document.NewMemoryStore()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	if _, err := RunHelp(context.Background(), docs, dir, "wksp_sys", "system", now); err != nil {
		t.Fatalf("first RunHelp: %v", err)
	}
	summary, err := RunHelp(context.Background(), docs, dir, "wksp_sys", "system", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second RunHelp: %v", err)
	}
	if summary.Created != 0 || summary.Updated != 0 {
		t.Errorf("second RunHelp created=%d updated=%d, want 0/0", summary.Created, summary.Updated)
	}
	if summary.Skipped != 1 {
		t.Errorf("second RunHelp skipped=%d, want 1", summary.Skipped)
	}
}

// TestRunHelp_RejectsWithoutTitle covers help-doc validation: a
// frontmatter without a title is recorded as an error entry; sibling
// files keep loading.
func TestRunHelp_RejectsWithoutTitle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "good.md", sampleHelpMD)
	writeFile(t, dir, "bad.md", "---\nslug: bad\n---\nbody but no title\n")

	docs := document.NewMemoryStore()
	summary, err := RunHelp(context.Background(), docs, dir, "wksp_sys", "system", time.Now().UTC())
	if err != nil {
		t.Fatalf("RunHelp: %v", err)
	}
	if summary.Created != 1 {
		t.Errorf("created = %d, want 1 (only good.md)", summary.Created)
	}
	if len(summary.Errors) != 1 {
		t.Errorf("errors = %d, want 1", len(summary.Errors))
	}
}
