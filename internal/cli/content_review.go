// content_review.go — the strict, deterministic half of the upload content
// review (sty_f302bd8b). validateUpload checks an artifact's STRUCTURE;
// reviewContent checks its CONTENT for drift-prone references that rot as the
// referenced row closes or renames. The per-type review skills
// (satellites-document-review / satellites-principle-review / satellites-skill-review) carry the conversational
// maintainability critique the local agent runs; this Go check is the
// mechanical block the CLI enforces before dispatching document_upsert,
// overridable with --skip-review.
//
// The rule is lifted from satellites-audit-agent-prose's "rotting identifiers":
// a durable artifact must not hard-code a concrete substrate slug
// (sty_be65b4dd, doc_4dc59149, wksp_…, proj_…). Template forms (story:<id>)
// carry no slug and pass. Fenced code blocks are skipped — example slugs
// inside ``` are documentation, not live references.

package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// rottingRefPattern matches a concrete substrate identifier slug: a short
// lowercase prefix, an underscore, then 6+ hex digits (sty_be65b4dd,
// doc_4dc59149, wksp_6f048cd8, proj_fc7d72d8). Template placeholders like
// story:<id> have no slug and never match.
var rottingRefPattern = regexp.MustCompile(`\b[a-z]{2,5}_[0-9a-f]{6,}\b`)

// reviewExemptTag, present in an artifact's frontmatter tags, exempts it from
// the strict drift-ref check — for artifacts whose PURPOSE is to carry concrete
// ids (a technical-debt register maps tests to owning story ids) or that
// document the id pattern itself. Declared in the artifact (config-over-code),
// not passed per-invocation like --skip-review.
const reviewExemptTag = "content-review:allow-refs"

// hasReviewExemptTag reports whether the artifact opted out of the drift check.
func hasReviewExemptTag(tags []string) bool {
	for _, t := range tags {
		if t == reviewExemptTag {
			return true
		}
	}
	return false
}

// reviewFinding is one strict-check violation.
type reviewFinding struct {
	Line int
	Rule string
	Text string
	Hint string // optional instructional suffix — how to fix, shown to the author
}

func (f reviewFinding) String() string {
	if f.Hint != "" {
		return fmt.Sprintf("line %d · %s · %s — %s", f.Line, f.Rule, f.Text, f.Hint)
	}
	return fmt.Sprintf("line %d · %s · %s", f.Line, f.Rule, f.Text)
}

// reviewContent runs the strict content checks over an artifact body and
// returns every violation (empty = clean). Fenced code blocks are exempt.
func reviewContent(body string) []reviewFinding {
	var out []reviewFinding
	inFence := false
	// Frontmatter is substrate keys and provenance (project_id, forked-from
	// stamps), not authored prose — exempt like code fences. Only a block
	// opening on line 1 counts; a later `---` is a markdown rule.
	inFrontmatter := false
	for i, line := range strings.Split(body, "\n") {
		if strings.TrimRight(line, "\r") == "---" {
			if i == 0 {
				inFrontmatter = true
				continue
			}
			if inFrontmatter {
				inFrontmatter = false
				continue
			}
		}
		if inFrontmatter {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range rottingRefPattern.FindAllString(line, -1) {
			out = append(out, reviewFinding{Line: i + 1, Rule: "rotting-ref", Text: m})
		}
	}
	return out
}

// repoScriptPattern matches a repo-local script path (scripts/foo.sh). A
// skill step depending on one is not self-contained: the logic belongs in the
// skill text or the client verb surface (the record-ci-evidence.sh case).
// Unlike the rotting-ref rule this is checked across the WHOLE body including
// code fences, because that is exactly where invocations live.
var repoScriptPattern = regexp.MustCompile(`\bscripts/[A-Za-z0-9._-]+\.(sh|bash|py)\b`)

// reviewSkillSelfContained runs the skills-only strict checks: a skill body
// may not depend on an unversioned repo script.
func reviewSkillSelfContained(body string) []reviewFinding {
	var out []reviewFinding
	for i, line := range strings.Split(body, "\n") {
		for _, m := range repoScriptPattern.FindAllString(line, -1) {
			out = append(out, reviewFinding{Line: i + 1, Rule: "repo-script-dependency", Text: m})
		}
	}
	return out
}

// wikilinkRefPattern matches a [[kebab-name]] reference anywhere in a workflow
// body. Unlike checkpointGateRe (which scans only a `## Checkpoint gates`
// section), this matches every prose reference — the dangling [[satellites-commit-push]]
// class `workflow check` misses.
var wikilinkRefPattern = regexp.MustCompile(`\[\[([a-z0-9-]+)\]\]`)

// reviewWorkflowRefs is the deterministic DRY-RUN at the heart of
// satellites-workflow-review (sty_fc39ca77 AC4): it resolves every artifact a
// workflow references — each transition's reviewer_skill and every [[wikilink]]
// in the prose — and returns a finding for any that resolves from NO tier. This
// catches the dangling-reference class `workflow check` cannot: that validator
// only resolves [[..]] inside a `## Checkpoint gates` section, never prose
// wikilinks like the deleted [[satellites-commit-push]] capability.
//
// resolveSkill folds the dispatcher's local→embed→server reviewer resolution
// (verb.GateResolvable / verb.IsConfigSkill) so a config/skills embedded
// reviewer such as satellites-intent-plan-review — named by every repo's entry
// edge but resolved from the binary embed — is correctly AVAILABLE, not a false
// positive. resolveDoc
// resolves a substrate document/principle by bare name through the scope cascade
// (a [[wikilink]] may point at a principle like [[reviewer-only-model]], not a
// skill). A reference resolving as EITHER is fine; one resolving as neither is
// dangling.
func reviewWorkflowRefs(body string, resolveSkill, resolveDoc func(string) bool) []reviewFinding {
	var out []reviewFinding
	seen := map[string]bool{}

	// 1) Transition reviewer_skills — a gate resolving from no tier fails closed
	// at dispatch (the story cannot move), so a dangling reviewer_skill is a block.
	if wf, err := workflow.Parse([]byte(body)); err == nil && wf != nil {
		for _, tr := range wf.Transitions {
			rs := strings.TrimSpace(tr.ReviewerSkill)
			if rs == "" || seen["skill:"+rs] {
				continue
			}
			seen["skill:"+rs] = true
			if !resolveSkill(rs) {
				f := reviewFinding{Line: firstLineContaining(body, rs), Rule: "unresolvable-reviewer", Text: rs}
				if ln := localSkillName(rs); ln != rs {
					f.Hint = fmt.Sprintf("no reviewer resolves under the materialised name %q; upload a reviewer that resolves there, or reference that name", ln)
				}
				out = append(out, f)
			}
		}
	}

	// 2) Prose [[wikilinks]] — resolve as a skill/gate OR a substrate
	// document/principle; one that resolves as neither is a dangling reference.
	for i, line := range strings.Split(body, "\n") {
		for _, m := range wikilinkRefPattern.FindAllStringSubmatch(line, -1) {
			name := m[1]
			if seen["link:"+name] {
				continue
			}
			seen["link:"+name] = true
			if resolveSkill(name) || resolveDoc(name) {
				continue
			}
			out = append(out, reviewFinding{Line: i + 1, Rule: "dangling-reference", Text: name})
		}
	}
	return out
}

// firstLineContaining returns the 1-based line number of the first line that
// contains substr, or 0 when absent — used to point a reviewer_skill finding at
// where the name sits in the workflow's ## Workflow block.
func firstLineContaining(body, substr string) int {
	for i, line := range strings.Split(body, "\n") {
		if strings.Contains(line, substr) {
			return i + 1
		}
	}
	return 0
}

// reviewSkillForKind maps an upload kind dir to the per-type review skill the
// local agent runs for the maintainability critique. All substrate-owned skills
// carry the `satellites-` prefix (satellites-skill-naming): documents→
// satellites-document-review, skills→satellites-skill-review, principles→
// satellites-principle-review. The name must match the system seed's frontmatter
// `name:` in config/documents/ — document_get resolves the review skill by it.
func reviewSkillForKind(kind string) string {
	return "satellites-" + strings.TrimSuffix(kind, "s") + "-review"
}
