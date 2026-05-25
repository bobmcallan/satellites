package reviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Run renders the reviewer's prompt against a story, calls the
// LLM client, and parses the response into a list of findings.
// Returns the findings even when zero — an empty slice is a
// successful no-finding review.
//
// The "story" argument is opaque to the runner: any JSON-marshallable
// value works. Story rows from internal/story are the canonical
// caller, but a project, a workspace, or a synthetic envelope can
// also be reviewed by the same machinery.
func Run(ctx context.Context, def Definition, client Client, story any) ([]Finding, error) {
	if !def.Enabled {
		return nil, nil
	}
	if client == nil {
		return nil, fmt.Errorf("reviewer: %s: client not configured", def.Name)
	}
	prompt, err := renderPrompt(def, story)
	if err != nil {
		return nil, fmt.Errorf("reviewer: %s: render: %w", def.Name, err)
	}
	raw, err := client.Complete(ctx, def.Model, def.MaxTokens, prompt)
	if err != nil {
		return nil, fmt.Errorf("reviewer: %s: complete: %w", def.Name, err)
	}
	out, err := parseOutput(raw)
	if err != nil {
		return nil, fmt.Errorf("reviewer: %s: parse: %w", def.Name, err)
	}
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	return out.Findings, nil
}

func renderPrompt(def Definition, story any) (string, error) {
	storyJSON, err := json.MarshalIndent(story, "", "  ")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(def.Body)
	if !strings.HasSuffix(def.Body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\n```json\n")
	b.Write(storyJSON)
	b.WriteString("\n```\n")
	return b.String(), nil
}

// parseOutput is lenient by design — LLMs occasionally wrap JSON
// in ```json fences or prepend an explanation despite explicit
// instructions otherwise. Strip the fences, find the first `{`,
// and Unmarshal from there.
func parseOutput(raw string) (Output, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		// Drop opening fence (with optional language tag).
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		// Drop trailing fence.
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:]
	}
	var out Output
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return Output{}, fmt.Errorf("unmarshal: %w (raw=%q)", err, raw)
	}
	return out, nil
}
