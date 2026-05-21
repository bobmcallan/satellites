package cli

import (
	"context"
	"os"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

func TestResolveCallerUserID_FlagWinsOverEnv(t *testing.T) {
	t.Setenv("SATELLITES_USER_ID", "from_env")
	got := resolveCallerUserID("from_flag")
	if got != "from_flag" {
		t.Errorf("flag should win, got %q", got)
	}
}

func TestResolveCallerUserID_EnvFallback(t *testing.T) {
	t.Setenv("SATELLITES_USER_ID", "from_env")
	got := resolveCallerUserID("")
	if got != "from_env" {
		t.Errorf("env fallback failed, got %q", got)
	}
}

func TestResolveCallerUserID_BothEmpty(t *testing.T) {
	_ = os.Unsetenv("SATELLITES_USER_ID")
	got := resolveCallerUserID("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestStampCallerUser_RoundTrip(t *testing.T) {
	ctx := stampCallerUser(context.Background(), "usr_test_42")
	u := auth.FromContext(ctx)
	if u == nil {
		t.Fatal("user not in ctx")
	}
	if u.ID != "usr_test_42" {
		t.Errorf("user id = %q, want usr_test_42", u.ID)
	}
}

func TestStampCallerUser_EmptyIsNoop(t *testing.T) {
	ctx := stampCallerUser(context.Background(), "")
	if auth.FromContext(ctx) != nil {
		t.Error("empty userID should not stamp a user")
	}
}
