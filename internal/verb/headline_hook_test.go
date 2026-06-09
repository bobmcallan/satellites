package verb

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/reviewer"
)

// stubReviewerClient is a non-nil reviewer.Client for the dispatch guard
// tests — the dispatch seam is swapped out, so Complete is never actually
// called here.
type stubReviewerClient struct{}

func (stubReviewerClient) Complete(_ context.Context, _ string, _ int, _ string) (string, error) {
	return "caveman line", nil
}

func strp(s string) *string { return &s }

// TestShouldGenerateHeadline pins the trigger predicate: generation fires
// only on operator silence (nil request headline), a document/principle row,
// and an empty stored headline. Operator-supplied headlines win, an
// already-present headline is left alone, and skills/stories are never
// processed (epic:always-context AC1/AC2).
func TestShouldGenerateHeadline(t *testing.T) {
	cases := []struct {
		name     string
		req      *string
		docType  string
		stored   string
		wantFire bool
	}{
		{"absent + empty document → generate", nil, document.TypeDocument, "", true},
		{"operator-supplied wins", strp("hand written"), document.TypeDocument, "", false},
		{"operator-supplied empty still wins", strp(""), document.TypeDocument, "", false},
		{"already present → skip", nil, document.TypeDocument, "existing line", false},
		{"skills never processed", nil, document.TypeSkill, "", false},
		{"stories never processed", nil, document.TypeStory, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldGenerateHeadline(c.req, c.docType, c.stored); got != c.wantFire {
				t.Errorf("shouldGenerateHeadline(%v, %q, %q) = %v, want %v",
					c.req, c.docType, c.stored, got, c.wantFire)
			}
		})
	}
}

// TestDispatchHeadlineRegen covers the fail-soft guards: the hook fires the
// dispatch only when the reviewer registry, the headline definition, the
// client, the store, and a doc id are all present. Any missing piece is a
// silent no-op — generation never errors or blocks the upsert
// (epic:always-context AC4/AC5).
func TestDispatchHeadlineRegen(t *testing.T) {
	prevReg, prevStore := reviewerRegistry, documentStore
	defer func() {
		reviewerRegistry = prevReg
		documentStore = prevStore
		SetHeadlineDispatchForTest(nil)
	}()

	enabled := reviewer.NewRegistry(map[string]reviewer.Definition{
		HeadlineReviewerName: {Name: HeadlineReviewerName, Enabled: true, Model: "m", MaxTokens: 64},
	}, stubReviewerClient{})
	disabled := reviewer.NewRegistry(map[string]reviewer.Definition{
		HeadlineReviewerName: {Name: HeadlineReviewerName, Enabled: false},
	}, stubReviewerClient{})
	noClient := reviewer.NewRegistry(map[string]reviewer.Definition{
		HeadlineReviewerName: {Name: HeadlineReviewerName, Enabled: true},
	}, nil)
	missingDef := reviewer.NewRegistry(map[string]reviewer.Definition{}, stubReviewerClient{})

	cases := []struct {
		name     string
		reg      *reviewer.Registry
		store    *document.Store
		docID    string
		wantFire bool
	}{
		{"all configured → fires", enabled, &document.Store{}, "doc_x", true},
		{"nil registry → no-op", nil, &document.Store{}, "doc_x", false},
		{"reviewer disabled → no-op", disabled, &document.Store{}, "doc_x", false},
		{"def missing → no-op", missingDef, &document.Store{}, "doc_x", false},
		{"nil client → no-op", noClient, &document.Store{}, "doc_x", false},
		{"nil store → no-op", enabled, nil, "doc_x", false},
		{"empty doc id → no-op", enabled, &document.Store{}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fired := false
			SetHeadlineDispatchForTest(func(_ context.Context, _ string) { fired = true })
			reviewerRegistry = c.reg
			documentStore = c.store
			dispatchHeadlineRegen(context.Background(), c.docID)
			if fired != c.wantFire {
				t.Errorf("fired = %v, want %v", fired, c.wantFire)
			}
		})
	}
}
