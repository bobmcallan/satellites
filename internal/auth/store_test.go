package auth

import "testing"

func TestHashKey_DeterministicAndDistinct(t *testing.T) {
	h1 := HashKey("sk_dev_admin")
	h2 := HashKey("sk_dev_admin")
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %s vs %s", h1, h2)
	}

	hOther := HashKey("sk_dev_user")
	if hOther == h1 {
		t.Fatalf("distinct inputs collided: %s == %s", hOther, h1)
	}

	// SHA-256 hex is 64 chars.
	if len(h1) != 64 {
		t.Fatalf("unexpected hash length %d (want 64): %s", len(h1), h1)
	}
}

func TestGenerateRawKey_PrefixAndLength(t *testing.T) {
	k, err := generateRawKey()
	if err != nil {
		t.Fatalf("rng: %v", err)
	}
	// "sk_" + 64 hex chars
	if len(k) != 67 {
		t.Fatalf("unexpected key length %d: %s", len(k), k)
	}
	if k[:3] != "sk_" {
		t.Fatalf("missing sk_ prefix: %s", k)
	}
}
