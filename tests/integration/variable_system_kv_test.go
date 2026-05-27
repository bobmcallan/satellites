//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSystemKV exercises sty_cc4c671a — operator-tunable system KV. Two
// flavours coexist at scope='system': stored rows in the variables
// table, and computed names served by the verb-layer resolver.
//
// Asserts:
//   - SeedSystem is idempotent on name presence (re-seed does NOT
//     overwrite an operator-set value).
//   - variable_get(scope='system', name='stories.page_size') returns
//     the seeded value.
//   - variable_get prefers stored rows over the computed resolver when
//     a name appears in both (impossible if used correctly, but the
//     ordering is the contract).
//   - variable_set(scope='system', name=<unknown>) writes.
//   - variable_set(scope='system', name=<computed-name>) is rejected
//     with a clear "computed" error.
//   - variable_list at scope='system' surfaces stored AND computed rows.
//   - A second SeedSystem after operator-set does NOT regress the value.
func TestSystemKV(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	varStore := variable.New(env.DB)
	authStore := auth.New(env.DB)

	verb.SetVariableStore(varStore)
	verb.SetAuthStore(authStore)
	t.Cleanup(func() {
		verb.SetVariableStore(nil)
		verb.SetAuthStore(nil)
	})

	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	ctxAdmin := authWithUser(context.Background(), admin)

	// Wire a stub computed resolver: 'version' is computed, everything
	// else is unknown. stories.page_size is NOT in this set, so a
	// stored row for it is the only source of truth.
	verb.SetSystemVariableResolver(
		func(_ context.Context, name string) (string, bool) {
			if name == "version" {
				return "v0.0.99", true
			}
			return "", false
		},
		func(context.Context) []string { return []string{"version"} },
	)
	t.Cleanup(func() { verb.SetSystemVariableResolver(nil, nil) })

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	t.Run("seed installs default and is idempotent", func(t *testing.T) {
		created, err := varStore.SeedSystem(ctx, "stories.page_size", "50", now)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if !created {
			t.Error("first seed should report created=true")
		}
		// Re-seed must NOT overwrite — created=false.
		created, err = varStore.SeedSystem(ctx, "stories.page_size", "999", now)
		if err != nil {
			t.Fatalf("re-seed: %v", err)
		}
		if created {
			t.Error("re-seed should report created=false")
		}
		// Verify value still 50.
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"stories.page_size","scope":"system"}`))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got verb.VariableGetResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != "50" || got.ResolvedScope != "system" {
			t.Errorf("after re-seed: got value=%s scope=%s want 50/system", got.Value, got.ResolvedScope)
		}
	})

	t.Run("operator write persists across reseed", func(t *testing.T) {
		// Operator overrides the default.
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"stories.page_size","scope":"system","value":"100"}`)); err != nil {
			t.Fatalf("set: %v", err)
		}
		// Read back.
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"stories.page_size","scope":"system"}`))
		if err != nil {
			t.Fatalf("get after set: %v", err)
		}
		var got verb.VariableGetResponse
		_ = json.Unmarshal(raw, &got)
		if got.Value != "100" {
			t.Errorf("after operator write: got %s want 100", got.Value)
		}
		// A subsequent seed (simulates a server reboot) must NOT
		// regress to the default.
		if _, err := varStore.SeedSystem(ctx, "stories.page_size", "50", now); err != nil {
			t.Fatalf("reseed: %v", err)
		}
		raw, _ = verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"stories.page_size","scope":"system"}`))
		_ = json.Unmarshal(raw, &got)
		if got.Value != "100" {
			t.Errorf("after reseed: got %s want 100 (operator value preserved)", got.Value)
		}
	})

	t.Run("computed-name writes rejected", func(t *testing.T) {
		_, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"version","scope":"system","value":"forged"}`))
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("computed write: expected ErrForbidden, got %v", err)
		}
		if !strings.Contains(err.Error(), "computed") {
			t.Errorf("error %q should mention 'computed'", err.Error())
		}
	})

	t.Run("unknown system name writeable (no whitelist)", func(t *testing.T) {
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"experimental.knob","scope":"system","value":"hi"}`)); err != nil {
			t.Fatalf("unknown system write: %v", err)
		}
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"experimental.knob","scope":"system"}`))
		if err != nil {
			t.Fatalf("get unknown: %v", err)
		}
		var got verb.VariableGetResponse
		_ = json.Unmarshal(raw, &got)
		if got.Value != "hi" {
			t.Errorf("got %s want hi", got.Value)
		}
	})

	t.Run("computed resolver still works for non-stored names", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"version","scope":"system"}`))
		if err != nil {
			t.Fatalf("get version: %v", err)
		}
		var got verb.VariableGetResponse
		_ = json.Unmarshal(raw, &got)
		if got.Value != "v0.0.99" || got.ResolvedScope != "system" {
			t.Errorf("computed version: got value=%s scope=%s want v0.0.99/system", got.Value, got.ResolvedScope)
		}
	})

	t.Run("system list folds in stored + computed", func(t *testing.T) {
		raw, err := verb.Dispatch(ctxAdmin, "variable_list", json.RawMessage(
			`{"scope":"system"}`))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var got verb.VariableListResponse
		_ = json.Unmarshal(raw, &got)
		seen := map[string]string{}
		for _, e := range got.Variables {
			seen[e.Name] = e.Value
		}
		if seen["stories.page_size"] != "100" {
			t.Errorf("list stories.page_size: got %q want 100 (operator value)", seen["stories.page_size"])
		}
		if seen["experimental.knob"] != "hi" {
			t.Errorf("list experimental.knob: got %q want hi", seen["experimental.knob"])
		}
		if seen["version"] != "v0.0.99" {
			t.Errorf("list version: got %q want v0.0.99 (computed)", seen["version"])
		}
	})
}
