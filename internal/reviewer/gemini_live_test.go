//go:build live

// Live integration test for the Gemini reviewer client. Build-tag-gated
// so default `go test ./...` skips the file entirely; run with
// `go test -tags=live ./internal/reviewer/...` (or `make test-live`).
//
// The blank import of tests/common triggers the dotenv loader's package
// init so GEMINI_API_KEY / GEMINI_REVIEW_MODEL set in tests/.env land in
// the test process env. Host-exported values still win.
//
// This test does not boot a satellites container — it talks to Gemini
// directly. A future variant that exercises the in-container reviewer
// dispatch path should pair tests/common with the TOML harness in
// tests/integration/toml_boot.go (writeTestTOML + startServerWithTOML)
// so the container loads its config from a mounted /app/satellites.toml,
// matching the production config-resolution order.

package reviewer_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/reviewer"
	_ "github.com/bobmcallan/satellites/tests/common"
)

// TestGeminiReviewer_Live drives the real Gemini generateContent
// endpoint. Catches model/schema drift the httptest unit tests cannot
// see — e.g. a future release renaming usageMetadata fields, swapping
// candidates structure, or tightening JSON-mode behaviour.
func TestGeminiReviewer_Live(t *testing.T) {
	// Build-tag-gated test (//go:build live) — default `go test ./...`
	// excludes this file. Under -tags=live, an empty key is a hard fail,
	// not a skip: PASS-by-skip hides Gemini-side regressions, which is
	// the workaround pattern story_d21436a4 explicitly removes.
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Fatal("GEMINI_API_KEY not set — populate tests/.env (see tests/README.md 'Rotating credentials'). " +
			"PASS-by-skip is rejected under -tags=live.")
	}

	model := os.Getenv("GEMINI_REVIEW_MODEL")
	if model == "" {
		model = reviewer.DefaultGeminiReviewModel
	}

	r := reviewer.NewGeminiReviewer(reviewer.GeminiConfig{
		APIKey: apiKey,
		Model:  model,
	})

	req := reviewer.Request{
		ContractID:   "contract_live_probe",
		ContractName: "develop",
		AgentInstruction: "Develop contract: deliver code changes for the story's two acceptance criteria. " +
			"AC1: ship function Foo. AC2: ship tests for Foo.",
		ReviewerRubric: "Approve when both ACs cite file:line evidence and a passing test output. " +
			"Reject when an AC has no evidence. Ask review_questions when evidence is ambiguous.",
		EvidenceMarkdown: "AC1 satisfied: pkg/foo/foo.go:12 defines Foo. " +
			"AC2 not addressed in this submission — no test file was added.",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	verdict, usage, err := r.Review(ctx, req)
	require.NoError(t, err, "live Gemini Review must not return a transport error")

	t.Logf("verdict.outcome=%q rationale=%q tokens=%d/%d model=%q",
		verdict.Outcome, verdict.Rationale, usage.InputTokens, usage.OutputTokens, usage.Model)

	assert.Contains(t,
		[]string{reviewer.VerdictAccepted, reviewer.VerdictRejected, reviewer.VerdictNeedsMore},
		verdict.Outcome,
		"outcome must be one of the canonical verdict strings")
	assert.NotEmpty(t, verdict.Rationale, "rationale must be non-empty for any verdict")
	assert.Greater(t, usage.InputTokens, 0, "input tokens must be reported by the live API")
	assert.Greater(t, usage.OutputTokens, 0, "output tokens must be reported by the live API")
	assert.Equal(t, model, usage.Model, "usage.Model must match the model the test selected")
}
