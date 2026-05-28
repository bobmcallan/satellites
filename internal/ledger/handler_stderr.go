package ledger

import (
	"io"
	"os"
)

// stderrFallbackWriter is the destination of the non-recursive
// fallback log used by LedgerHandler when batches are dropped. We
// indirect through this variable so tests can swap it for a buffer.
var stderrFallbackWriter io.Writer = os.Stderr
