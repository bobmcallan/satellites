package cli

import (
	"testing"
	"time"
)

// TestReviewerKeyTTLOutlivesDispatch pins sty_64c6159f: the minted reviewer
// key must live longer than the gate dispatch it is minted for. A gate run
// (build + test) can take up to gateDispatchTimeout; if the key expires
// first, the gate's post-decision spine writes (review_accept/reject +
// status_transition) 401 on the dead key and the verdict's ledger trail is
// lost. The TTL is derived from the timeout + headroom so the two cannot
// drift; this test fails if a future edit lets the TTL fall to or below the
// dispatch timeout.
func TestReviewerKeyTTLOutlivesDispatch(t *testing.T) {
	if reviewerKeyTTL <= gateDispatchTimeout {
		t.Fatalf("reviewerKeyTTL (%s) must exceed gateDispatchTimeout (%s) so a long gate run can still write its spine rows",
			reviewerKeyTTL, gateDispatchTimeout)
	}
	if headroom := reviewerKeyTTL - gateDispatchTimeout; headroom < time.Minute {
		t.Fatalf("reviewerKeyTTL headroom over the dispatch timeout is only %s; want at least 1m of slack", headroom)
	}
}
