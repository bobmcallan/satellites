package substrate

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mdArtifacts returns every embedded .md artifact path under config/,
// recursing the by-type subdirectories (documents/ principles/ skills/).
// The directory-walking audits below run over this set so a violation in
// any subdir surfaces at PR time.
func mdArtifacts(t *testing.T) []string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		t.Fatalf("walk config artifacts: %v", err)
	}
	return paths
}

// TestSeedArtifactsAuditClean enforces the block-level static rules from
// the satellites-audit-agent-prose skill against every markdown
// artifact in this directory tree. CI runs this via `go test ./...` so
// verbose drift in agent-facing prose surfaces at PR time rather
// than after the binary ships. The reviewer-gated equivalent is the
// substrate-primitive defense; this test is the belt-and-braces line.
func TestSeedArtifactsAuditClean(t *testing.T) {
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

	for _, path := range mdArtifacts(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := stripFencedCode(string(raw))
		for _, r := range rules {
			if locs := r.pattern.FindAllStringIndex(body, -1); len(locs) > 0 {
				for _, loc := range locs {
					t.Errorf("%s · rule %q · match %q at byte %d — fix prose or move to a code fence",
						path, r.name, body[loc[0]:loc[1]], loc[0])
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
		startupTag = "kind:mcp-startup"
		lineBudget = 150
	)
	for _, path := range mdArtifacts(t) {
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
				path, lines, startupTag, lineBudget)
		}
	}
}

// TestSkillSeedsCarrySatellitesPrefix enforces the satellites-skill-naming
// convention at the source: every embedded skill seed (type:skill) must declare
// a frontmatter name AND a filename stem that begin with `satellites-`. The
// review-family seeds (skill-review/principle-review/document-review) and
// project-setup historically lacked it, masked at materialise time by
// skill sync's localSkillName auto-prefix (sty_705214ee). This guard fails the
// build if an unprefixed skill seed reappears, so the inconsistency cannot
// silently return. Non-skill artifacts (documents, reference prose) are exempt —
// the prefix marks substrate-owned SKILLS.
func TestSkillSeedsCarrySatellitesPrefix(t *testing.T) {
	typeRe := regexp.MustCompile(`(?m)^type:\s*(\S+)`)
	nameRe := regexp.MustCompile(`(?m)^name:\s*(\S+)`)
	for _, path := range mdArtifacts(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		tm := typeRe.FindStringSubmatch(string(raw))
		if tm == nil || tm[1] != "skill" {
			continue // only type:skill seeds are governed by the naming convention
		}
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "satellites-") {
			t.Errorf("%s · skill seed filename must start with `satellites-` (satellites-skill-naming)", path)
		}
		nm := nameRe.FindStringSubmatch(string(raw))
		if nm == nil {
			t.Errorf("%s · skill seed missing frontmatter `name:`", path)
			continue
		}
		if !strings.HasPrefix(nm[1], "satellites-") {
			t.Errorf("%s · skill seed frontmatter name %q must start with `satellites-` (satellites-skill-naming)", path, nm[1])
		}
	}
}

// TestNamingConventionByType asserts the substrate naming convention
// (constitution > "Substrate naming") over the embedded set, keyed off the
// type subdir and frontmatter kind — no hardcoded name list. A principle
// carries NO satellites prefix; a skill carries satellites-; an MCP/install
// SCHEMA document (kind:install-schema|mcp-startup|mcp-reference|contract)
// carries satellites_, while a standalone taxonomy/config document may be bare.
func TestNamingConventionByType(t *testing.T) {
	schemaKind := regexp.MustCompile(`kind:(install-schema|mcp-startup|mcp-reference|contract)\b`)
	for _, path := range mdArtifacts(t) {
		stem := strings.TrimSuffix(filepath.Base(path), ".md")
		switch filepath.Dir(path) {
		case "principles":
			if strings.HasPrefix(stem, "satellites-") || strings.HasPrefix(stem, "satellites_") {
				t.Errorf("%s · principle must be kebab-case with NO satellites prefix (constitution: Substrate naming)", path)
			}
		case "skills":
			if !strings.HasPrefix(stem, "satellites-") {
				t.Errorf("%s · skill must be named satellites-<kebab> (constitution: Substrate naming)", path)
			}
		case "mcp":
			if !strings.HasPrefix(stem, "satellites_") {
				t.Errorf("%s · MCP-service document must be named satellites_<snake> (constitution: Substrate naming)", path)
			}
		case "workflows":
			if !strings.HasPrefix(stem, "satellites-") {
				t.Errorf("%s · workflow must be named satellites-<kebab> (constitution: Substrate naming)", path)
			}
		case "documents":
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if schemaKind.MatchString(string(raw)) && !strings.HasPrefix(stem, "satellites_") {
				t.Errorf("%s · MCP/install schema document must be named satellites_<snake> (constitution: Substrate naming)", path)
			}
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
