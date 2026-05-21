package auth

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleAuthorizationServer(t *testing.T) {
	r := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	r.Host = "satellites-pprod.fly.dev"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	HandleAuthorizationServer(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	const base = "https://satellites-pprod.fly.dev"
	wantStr := map[string]string{
		"issuer":                 base,
		"authorization_endpoint": base + "/oauth/authorize",
		"token_endpoint":         base + "/oauth/token",
		"registration_endpoint":  base + "/oauth/register",
	}
	for k, want := range wantStr {
		if got, _ := body[k].(string); got != want {
			t.Errorf("body[%q] = %q, want %q", k, got, want)
		}
	}
	for _, k := range []string{
		"response_types_supported",
		"grant_types_supported",
		"code_challenge_methods_supported",
		"scopes_supported",
	} {
		if _, ok := body[k].([]any); !ok {
			t.Errorf("body[%q] missing or not an array", k)
		}
	}
}

func TestHandleProtectedResource(t *testing.T) {
	r := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource", nil)
	r.Host = "satellites-pprod.fly.dev"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	HandleProtectedResource(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	const base = "https://satellites-pprod.fly.dev"
	if got, _ := body["resource"].(string); got != base+"/mcp" {
		t.Errorf("resource = %q, want %q", got, base+"/mcp")
	}
	servers, ok := body["authorization_servers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("authorization_servers = %v, want single-element array", body["authorization_servers"])
	}
	if got, _ := servers[0].(string); got != base {
		t.Errorf("authorization_servers[0] = %q, want %q", got, base)
	}
}

func TestSchemeAndHost(t *testing.T) {
	cases := []struct {
		name       string
		host       string
		forwarded  string
		tlsEnabled bool
		want       string
	}{
		{
			name: "plain_http",
			host: "localhost:8080",
			want: "http://localhost:8080",
		},
		{
			name:      "x_forwarded_proto_https",
			host:      "satellites-pprod.fly.dev",
			forwarded: "https",
			want:      "https://satellites-pprod.fly.dev",
		},
		{
			name:      "x_forwarded_proto_case_insensitive",
			host:      "example.com",
			forwarded: "HTTPS",
			want:      "https://example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = tc.host
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			got := SchemeAndHost(r)
			if got != tc.want {
				t.Errorf("SchemeAndHost = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleAuthorizationServer_DerivesHostFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	r.Host = "localhost:9999"
	w := httptest.NewRecorder()

	HandleAuthorizationServer(w, r)

	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	issuer, _ := body["issuer"].(string)
	if !strings.HasPrefix(issuer, "http://localhost:9999") {
		t.Errorf("issuer = %q, want http://localhost:9999 prefix", issuer)
	}
}
