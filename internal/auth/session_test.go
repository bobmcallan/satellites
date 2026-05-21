package auth

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessions_IssueAndVerify(t *testing.T) {
	s := NewSessions([]byte("fixed-secret-for-determinism"))

	rec := httptest.NewRecorder()
	s.Issue(rec, "usr_test_123")
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != SessionCookieName {
		t.Errorf("cookie name: got %q", cookies[0].Name)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	uid, err := s.UserID(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if uid != "usr_test_123" {
		t.Errorf("user_id: got %q want usr_test_123", uid)
	}
}

func TestSessions_MissingCookie(t *testing.T) {
	s := NewSessions([]byte("secret"))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := s.UserID(req); err != ErrInvalidSession {
		t.Errorf("got %v want ErrInvalidSession", err)
	}
}

func TestSessions_TamperedSignature(t *testing.T) {
	s := NewSessions([]byte("secret"))
	rec := httptest.NewRecorder()
	s.Issue(rec, "usr_x")
	c := rec.Result().Cookies()[0]

	// Flip the last byte of the HMAC portion.
	parts := strings.Split(c.Value, "|")
	if len(parts) != 3 {
		t.Fatalf("unexpected cookie shape: %q", c.Value)
	}
	tampered := parts[2]
	if tampered[len(tampered)-1] == 'A' {
		tampered = tampered[:len(tampered)-1] + "B"
	} else {
		tampered = tampered[:len(tampered)-1] + "A"
	}
	c.Value = parts[0] + "|" + parts[1] + "|" + tampered

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	if _, err := s.UserID(req); err != ErrInvalidSession {
		t.Errorf("tampered: got %v want ErrInvalidSession", err)
	}
}

func TestSessions_WrongSecretRejects(t *testing.T) {
	issued := NewSessions([]byte("secret-A"))
	rec := httptest.NewRecorder()
	issued.Issue(rec, "usr_x")

	other := NewSessions([]byte("secret-B"))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])
	if _, err := other.UserID(req); err != ErrInvalidSession {
		t.Errorf("foreign secret: got %v want ErrInvalidSession", err)
	}
}

func TestSecretFromHex(t *testing.T) {
	want := []byte{0xde, 0xad, 0xbe, 0xef}
	got, err := SecretFromHex(hex.EncodeToString(want))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %x want %x", got, want)
	}
	if g, err := SecretFromHex(""); err != nil || g != nil {
		t.Errorf("empty: got (%v,%v) want (nil,nil)", g, err)
	}
}
