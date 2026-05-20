package verb

import "crypto/rand"

// randRead is a thin shim so satellites_init's id-suffix generator can
// pull crypto-backed entropy without polluting the package import set
// with unrelated callers.
func randRead(b []byte) (int, error) {
	return rand.Read(b)
}
