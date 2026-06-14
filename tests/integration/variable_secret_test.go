//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bobmcallan/satellites/internal/agent"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/llmcfg"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestVariableSecrets exercises the LLM-config-as-substrate surface against a
// live Postgres (sty_a6983e32): a secret key is admin-gated on write and NEVER
// returned by variable_get / variable_list, while a server-internal resolver
// reads its plaintext directly and resolves the model from a plain
// stored-system key-value with an env fallback.
func TestVariableSecrets(t *testing.T) {
	env := testbootstrap.SetUp(t)

	varStore := variable.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	verb.SetVariableStore(varStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetVariableStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})

	if _, err := env.DB.Exec(`TRUNCATE api_keys, users, workspace_members RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth: %v", err)
	}
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	user, err := authStore.GetUserByEmail(context.Background(), auth.DevUserEmail)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	ctxAdmin := authWithUser(context.Background(), admin)
	ctxUser := authWithUser(context.Background(), user)

	const secretPlaintext = "sk-super-secret-key"

	t.Run("admin writes a secret; reads redact it; the store keeps plaintext", func(t *testing.T) {
		testbootstrap.Reset(t, env)

		// Admin sets the secret. The write response itself never echoes the value.
		setRaw, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"gemini.api_key","scope":"system","value":"`+secretPlaintext+`","secret":true}`))
		if err != nil {
			t.Fatalf("set secret: %v", err)
		}
		if json.Valid(setRaw) && containsPlaintext(setRaw, secretPlaintext) {
			t.Fatalf("variable_set echoed the secret plaintext: %s", setRaw)
		}

		// variable_get redacts: value blank, secret marker true.
		getRaw, err := verb.Dispatch(ctxAdmin, "variable_get", json.RawMessage(
			`{"name":"gemini.api_key","scope":"system"}`))
		if err != nil {
			t.Fatalf("get secret: %v", err)
		}
		var got verb.VariableGetResponse
		if err := json.Unmarshal(getRaw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != "" || !got.Secret {
			t.Fatalf("variable_get should redact: got value=%q secret=%v", got.Value, got.Secret)
		}

		// variable_list redacts too — name present, value blank, secret marker.
		listRaw, err := verb.Dispatch(ctxAdmin, "variable_list", json.RawMessage(`{"scope":"system"}`))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if containsPlaintext(listRaw, secretPlaintext) {
			t.Fatalf("variable_list leaked the secret plaintext: %s", listRaw)
		}
		var list verb.VariableListResponse
		if err := json.Unmarshal(listRaw, &list); err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, e := range list.Variables {
			if e.Name == "gemini.api_key" {
				found = true
				if e.Value != "" || !e.Secret {
					t.Fatalf("list entry should redact: got value=%q secret=%v", e.Value, e.Secret)
				}
			}
		}
		if !found {
			t.Fatal("variable_list did not surface the secret's name")
		}

		// The server-internal resolver reads the plaintext directly.
		resolver := llmcfg.New(varStore, func(string) string { return "" })
		if got := resolver.Secret(context.Background(), "gemini.api_key", "GEMINI_API_KEY"); got != secretPlaintext {
			t.Fatalf("resolver should read plaintext directly: got %q want %q", got, secretPlaintext)
		}
	})

	t.Run("non-admin secret write is forbidden", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		_, err := verb.Dispatch(ctxUser, "variable_set", json.RawMessage(
			`{"name":"gemini.api_key","scope":"system","value":"x","secret":true}`))
		if !errors.Is(err, verb.ErrForbidden) {
			t.Fatalf("expected ErrForbidden for non-admin secret write, got %v", err)
		}
	})

	t.Run("reserved key name cannot be a plain variable", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		_, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"anthropic.api_key","scope":"system","value":"x"}`))
		if !errors.Is(err, verb.ErrBadRequest) {
			t.Fatalf("expected ErrBadRequest for plain reserved name, got %v", err)
		}
	})

	t.Run("model resolves from a key-value at runtime; flipping it switches the model", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		resolver := llmcfg.New(varStore, func(string) string { return "" })

		// Unset → code default.
		if got := resolver.Model(context.Background(), "agent", agent.DefaultModel); got != agent.DefaultModel {
			t.Fatalf("unset agent.model should default: got %q want %q", got, agent.DefaultModel)
		}

		// Set the agent model as a plain stored-system key-value → it wins.
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"agent.model","scope":"system","value":"gemini-flip-one"}`)); err != nil {
			t.Fatalf("set agent.model: %v", err)
		}
		if got := resolver.Model(context.Background(), "agent", agent.DefaultModel); got != "gemini-flip-one" {
			t.Fatalf("agent.model should resolve from kv: got %q", got)
		}

		// Flip the value — the next resolution reflects it with no redeploy.
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"agent.model","scope":"system","value":"gemini-flip-two"}`)); err != nil {
			t.Fatalf("flip agent.model: %v", err)
		}
		if got := resolver.Model(context.Background(), "agent", agent.DefaultModel); got != "gemini-flip-two" {
			t.Fatalf("flipped agent.model should resolve fresh: got %q", got)
		}
	})

	t.Run("env is a migration fallback; the variable wins when set", func(t *testing.T) {
		testbootstrap.Reset(t, env)
		resolver := llmcfg.New(varStore, func(k string) string {
			if k == "GEMINI_API_KEY" {
				return "sk-from-env-fallback"
			}
			return ""
		})

		// No variable → env fallback.
		if got := resolver.Secret(context.Background(), "gemini.api_key", "GEMINI_API_KEY"); got != "sk-from-env-fallback" {
			t.Fatalf("unset secret should fall back to env: got %q", got)
		}

		// Set the variable → it wins over env.
		if _, err := verb.Dispatch(ctxAdmin, "variable_set", json.RawMessage(
			`{"name":"gemini.api_key","scope":"system","value":"`+secretPlaintext+`","secret":true}`)); err != nil {
			t.Fatalf("set secret: %v", err)
		}
		if got := resolver.Secret(context.Background(), "gemini.api_key", "GEMINI_API_KEY"); got != secretPlaintext {
			t.Fatalf("set secret should win over env: got %q want %q", got, secretPlaintext)
		}
	})
}

// containsPlaintext reports whether raw JSON contains the literal secret.
func containsPlaintext(raw []byte, secret string) bool {
	return secret != "" && bytes.Contains(raw, []byte(secret))
}
