package configseed

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/document"
)

// LoadDir walks dir/<kindSubdir>/*.md, parses each file, and returns
// the upsert inputs ready for Run to apply. Errors per file go into
// the second return; one bad file does not abort the others. Caller
// supplies workspaceID + actor for the resulting Documents.
func LoadDir(rootDir string, kind Kind, workspaceID, actor string) ([]document.UpsertInput, []ErrorEntry) {
	subdir := filepath.Join(rootDir, kindSubdir(kind))
	entries, err := os.ReadDir(subdir)
	if err != nil {
		// Missing subdir is fine — the seed simply has no files of
		// this kind today. Other errors surface as one entry.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []ErrorEntry{{Path: subdir, Reason: fmt.Sprintf("read dir: %v", err)}}
	}
	// Stable order for deterministic boot logs / tests.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	out := make([]document.UpsertInput, 0, len(entries))
	errs := make([]ErrorEntry, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(subdir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, ErrorEntry{Path: path, Reason: fmt.Sprintf("read: %v", err)})
			continue
		}
		fm, body, err := Parse(content)
		if err != nil {
			errs = append(errs, ErrorEntry{Path: path, Reason: fmt.Sprintf("parse: %v", err)})
			continue
		}
		input, err := buildInput(kind, fm, body, workspaceID, actor)
		if err != nil {
			errs = append(errs, ErrorEntry{Path: path, Reason: err.Error()})
			continue
		}
		out = append(out, input)
	}
	return out, errs
}

// kindSubdir returns the canonical subdirectory name for a kind.
// The plural form mirrors the directory layout users expect.
func kindSubdir(kind Kind) string {
	switch kind {
	case KindAgent:
		return "agents"
	case KindContract:
		return "contracts"
	case KindWorkflow:
		return "workflows"
	case KindHelp:
		// Help docs live at the seed-dir root rather than under a
		// subdirectory — see HelpDir wiring in runner.go.
		return ""
	}
	return string(kind)
}

// buildInput dispatches to the per-kind parser.
func buildInput(kind Kind, fm Frontmatter, body []byte, workspaceID, actor string) (document.UpsertInput, error) {
	switch kind {
	case KindAgent:
		return agentToInput(fm, body, workspaceID, actor)
	case KindContract:
		return contractToInput(fm, body, workspaceID, actor)
	case KindWorkflow:
		return workflowToInput(fm, body, workspaceID, actor)
	case KindHelp:
		return helpToInput(fm, body, workspaceID, actor)
	}
	return document.UpsertInput{}, fmt.Errorf("configseed: unknown kind %q", kind)
}
