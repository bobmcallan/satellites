// content_review.go — the strict, deterministic half of the upload content
// review (sty_f302bd8b). validateUpload checks an artifact's STRUCTURE;
// reviewContent checks its CONTENT for drift-prone references that rot as the
// referenced row closes or renames. The per-type review skills
// (document-review / principle-review / skill-review) carry the conversational
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
}

func (f reviewFinding) String() string {
	return fmt.Sprintf("line %d · %s · %s", f.Line, f.Rule, f.Text)
}

// reviewContent runs the strict content checks over an artifact body and
// returns every violation (empty = clean). Fenced code blocks are exempt.
func reviewContent(body string) []reviewFinding {
	var out []reviewFinding
	inFence := false
	for i, line := range strings.Split(body, "\n") {
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

// reviewSkillForKind maps an upload kind dir to the per-type review skill the
// local agent runs for the maintainability critique. documents→document-review,
// skills→skill-review, principles→principle-review.
func reviewSkillForKind(kind string) string {
	return strings.TrimSuffix(kind, "s") + "-review"
}
