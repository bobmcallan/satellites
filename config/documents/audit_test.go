package documents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSeedArtifactsAuditClean enforces the block-level static rules from
// the satellites-audit-agent-prose skill against every markdown
// artifact in this directory. CI runs this via `go test ./...` so
// verbose drift in agent-facing prose surfaces at PR time rather
// than after the binary ships. The reviewer-gated equivalent is the
// substrate-primitive defense; this test is the belt-and-braces line.
func TestSeedArtifactsAuditClean(t *testing.T) {
	const artifactsDir = "."
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		t.Fatalf("read %s: %v", artifactsDir, err)
	}

	type rule struct {
		name    string
		pattern *regexp.Regexp
	}
	rules := []rule{
		{
			name:    "host-coupling",
			pattern: regexp.MustCompile(`(?i)\b(this\s+(repo|codebase|project)|our\s+(repo|codebase)|in\s+the\s+satellites\s+repo|here\s+we)\b`),
		},
		{
			name:    "implementation-status-narrative",
			pattern: regexp.MustCompile(`(?i)\b(currently|in\s+progress|for\s+now|coming\s+soon|not\s+yet\s+wired|tracked\s+under|stub\s+until|until\s+they\s+land|TODO)\b`),
		},
		{
			// Reject concrete substrate IDs in agent-facing prose;
			// angle-bracket placeholders (<sty_id>, <proj_id>) use
			// alphabetic suffixes and never match this pattern.
			name:    "rotting-identifiers",
			pattern: regexp.MustCompile(`\b(sty|proj|doc|apk|wksp|usr)_[0-9a-f]{6,}\b`),
		},
	}

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		path := filepath.Join(artifactsDir, ent.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := stripFencedCode(string(raw))
		for _, r := range rules {
			if locs := r.pattern.FindAllStringIndex(body, -1); len(locs) > 0 {
				for _, loc := range locs {
					t.Errorf("%s · rule %q · match %q at byte %d — fix prose or move to a code fence",
						ent.Name(), r.name, body[loc[0]:loc[1]], loc[0])
				}
			}
		}
	}
}

// TestMCPStartupArtifactsUnderLineBudget caps any artifact tagged
// `kind:mcp-startup` at 150 lines — the limit the audit-prose skill
// specifies for load-time directives. The byte-level cap on the
// assembled MCP initialize payload lives in internal/mcpserver and is
// pinned by TestOrientationInstructionsUnderBudget; this test catches
// the same regression at the artifact source.
func TestMCPStartupArtifactsUnderLineBudget(t *testing.T) {
	const (
		artifactsDir = "."
		startupTag   = "kind:mcp-startup"
		lineBudget   = 150
	)
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		t.Fatalf("read %s: %v", artifactsDir, err)
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		path := filepath.Join(artifactsDir, ent.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), startupTag) {
			continue
		}
		lines := strings.Count(string(raw), "\n") + 1
		if lines > lineBudget {
			t.Errorf("%s · %d lines exceeds %s budget of %d — split reference material into a separate document fetched on demand",
				ent.Name(), lines, startupTag, lineBudget)
		}
	}
}

// stripFencedCode returns body with every ```...``` block replaced by
// equal-length whitespace. Preserves byte offsets so violation
// locations still line up with the original file, while keeping the
// audit rules from firing on intentional placeholder shapes inside
// example code blocks.
func stripFencedCode(body string) string {
	out := []byte(body)
	const fence = "```"
	for i := 0; i < len(out); {
		start := strings.Index(string(out[i:]), fence)
		if start < 0 {
			break
		}
		start += i
		end := strings.Index(string(out[start+len(fence):]), fence)
		if end < 0 {
			break
		}
		end = start + len(fence) + end + len(fence)
		for j := start; j < end && j < len(out); j++ {
			if out[j] != '\n' {
				out[j] = ' '
			}
		}
		i = end
	}
	return string(out)
}
