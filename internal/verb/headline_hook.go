// Headline hook — generates the caveman one-line headline for a
// document/principle on upsert. Mirrors the summary hook
// (summary_hook.go): reuses the same reviewer registry + client; the
// headline reviewer is just another markdown definition whose output
// is a single free-form line instead of JSON findings.
//
// Generation is generate-when-empty and fail-soft: an operator-supplied
// headline is never overwritten, and a missing registry/client or a
// failing LLM call leaves the headline empty without breaking the
// originating upsert (epic:always-context, sty_f68f9053).

package verb

import (
	"context"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/reviewer"
)

// HeadlineReviewerName is the registry key for the headline definition
// (config/documents/satellites-document-headline.md). Looked up by name;
// the on-disk file name is incidental.
const HeadlineReviewerName = "satellites-document-headline"

var (
	// headlineDispatch is the indirection seam tests use to assert the
	// hook fired without spawning a goroutine or calling an LLM.
	// Production wires to spawnHeadlineRegen.
	headlineDispatch = spawnHeadlineRegen
)

// SetHeadlineDispatchForTest overrides the goroutine spawn with a
// synchronous variant. Pass nil to restore the default.
func SetHeadlineDispatchForTest(fn func(ctx context.Context, docID string)) {
	if fn == nil {
		headlineDispatch = spawnHeadlineRegen
		return
	}
	headlineDispatch = fn
}

// shouldGenerateHeadline reports whether the upsert path should trigger
// generation: only when the operator supplied no headline (reqHeadline nil),
// the row is a document/principle, and no headline is stored yet. An
// operator-supplied headline (non-nil, even "") or an already-present one is
// left untouched, and skills/stories are never processed.
func shouldGenerateHeadline(reqHeadline *string, docType, storedHeadline string) bool {
	return reqHeadline == nil && docType == document.TypeDocument && storedHeadline == ""
}

// dispatchHeadlineRegen is the post-upsert hook. It returns immediately;
// the LLM call + UPDATE happen via the configurable dispatch. All the
// not-configured guards live here so callers stay a single line, and so
// an unconfigured environment (in-process upsert, no reviewer client) is
// a silent no-op rather than an error — generation is best-effort.
func dispatchHeadlineRegen(_ context.Context, docID string) {
	if reviewerRegistry == nil {
		return
	}
	def, ok := reviewerRegistry.Defs[HeadlineReviewerName]
	if !ok || !def.Enabled {
		return
	}
	if reviewerRegistry.Client == nil {
		return
	}
	if documentStore == nil {
		return
	}
	if docID == "" {
		return
	}
	headlineDispatch(context.Background(), docID)
}

// spawnHeadlineRegen is the production dispatch: fire a goroutine against
// a fresh background context, since the caller's ctx may be cancelled
// before the LLM call returns.
func spawnHeadlineRegen(_ context.Context, docID string) {
	go regenerateHeadline(context.Background(), docID)
}

// regenerateHeadline loads the document + its latest body, renders the
// headline prompt, calls the LLM, and writes the resulting line to
// documents.headline. Errors are logged and swallowed — a failing
// headline regen never breaks the originating upsert.
func regenerateHeadline(ctx context.Context, docID string) {
	if reviewerRegistry == nil || documentStore == nil {
		return
	}
	def, ok := reviewerRegistry.Defs[HeadlineReviewerName]
	if !ok || !def.Enabled || reviewerRegistry.Client == nil {
		return
	}
	d, body, err := documentStore.GetByIDWithLatestBody(ctx, docID)
	if err != nil {
		arbor.Warn("headline regen: get document failed", "doc_id", docID, "err", err)
		return
	}
	// Only documents/principles carry a headline; never overwrite an
	// existing one (operator-supplied or a prior generation wins).
	if d.Type != document.TypeDocument || d.Headline != "" {
		return
	}
	envelope := headlineEnvelope{Name: d.Name, Body: body, Tags: d.Tags}
	text, err := reviewer.RunText(ctx, def, reviewerRegistry.Client, envelope)
	if err != nil {
		arbor.Warn("headline regen: reviewer failed", "doc_id", docID, "err", err)
		return
	}
	if text == "" {
		return
	}
	if _, err := documentStore.SetDocumentHeadline(ctx, docID, text, time.Now().UTC()); err != nil {
		arbor.Warn("headline regen: set headline failed", "doc_id", docID, "err", err)
	}
}

// headlineEnvelope is the JSON shape passed to the headline reviewer's
// prompt template. Field names match the schema the markdown body
// documents to the LLM.
type headlineEnvelope struct {
	Name string   `json:"name"`
	Body string   `json:"body"`
	Tags []string `json:"tags"`
}

// RegenerateHeadlineForTest is the test-facing synchronous variant, so
// tests can drive the regen without goroutine races.
func RegenerateHeadlineForTest(ctx context.Context, docID string) {
	regenerateHeadline(ctx, docID)
}
