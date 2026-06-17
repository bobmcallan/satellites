package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestBuildChangelogUpsertRequest pins the request assembly (sty_1777fbeb): a
// system-scope type:changelog payload whose typed fields ride as tags, with the
// effective_date tag present only when supplied and the name minted when omitted.
func TestBuildChangelogUpsertRequest(t *testing.T) {
	req, err := buildChangelogUpsertRequest("satellites", "v0.0.256", "v0.0.272", "2026-06-17", "", "## notes\nbody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Type != "changelog" || req.Scope != "system" {
		t.Errorf("type/scope = %q/%q, want changelog/system", req.Type, req.Scope)
	}
	if req.Body != "## notes\nbody" {
		t.Errorf("body not preserved: %q", req.Body)
	}
	if !strings.HasPrefix(req.Name, "cl_") {
		t.Errorf("minted name should be cl_… , got %q", req.Name)
	}
	want := map[string]bool{
		"changelog":                 false,
		"service:satellites":        false,
		"version_from:v0.0.256":     false,
		"version_to:v0.0.272":       false,
		"effective_date:2026-06-17": false,
	}
	for _, tag := range req.Tags {
		if _, ok := want[tag]; ok {
			want[tag] = true
		}
	}
	for tag, seen := range want {
		if !seen {
			t.Errorf("missing expected tag %q (got %v)", tag, req.Tags)
		}
	}

	// No effective-date → the effective_date tag is absent; explicit name kept.
	req2, err := buildChangelogUpsertRequest("satellites-server", "v0.0.1", "v0.0.2", "", "cl_fixed", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req2.Name != "cl_fixed" {
		t.Errorf("explicit name should be preserved, got %q", req2.Name)
	}
	for _, tag := range req2.Tags {
		if strings.HasPrefix(tag, "effective_date:") {
			t.Errorf("no effective-date supplied but tag present: %q", tag)
		}
	}
}

// TestBuildChangelogUpsertRequest_Validation pins the required-flag refusals:
// missing service, version bound, or body each fail before any dispatch.
func TestBuildChangelogUpsertRequest_Validation(t *testing.T) {
	cases := []struct {
		name                             string
		service, from, to, effDate, body string
		wantErrSubstr                    string
	}{
		{"no service", "", "v1", "v2", "", "body", "--service is required"},
		{"no from", "satellites", "", "v2", "", "body", "--from and --to are required"},
		{"no to", "satellites", "v1", "", "", "body", "--from and --to are required"},
		{"empty body", "satellites", "v1", "v2", "", "  ", "body is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildChangelogUpsertRequest(c.service, c.from, c.to, c.effDate, "", c.body)
			if err == nil || !strings.Contains(err.Error(), c.wantErrSubstr) {
				t.Errorf("want error containing %q, got %v", c.wantErrSubstr, err)
			}
		})
	}
}

// TestResolveChangelogBody pins the body source precedence: --body wins over
// --file, and stdin is the fallback when neither is set.
func TestResolveChangelogBody(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewBufferString("from-stdin"))

	if got, err := resolveChangelogBody(cmd, "inline", ""); err != nil || got != "inline" {
		t.Errorf("--body should win: got %q, err %v", got, err)
	}
	if got, err := resolveChangelogBody(cmd, "", ""); err != nil || got != "from-stdin" {
		t.Errorf("stdin fallback: got %q, err %v", got, err)
	}
}

// TestNewChangelogName pins the minted-key shape: cl_ + 8 hex chars, unique.
func TestNewChangelogName(t *testing.T) {
	a, err := newChangelogName()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(a, "cl_") || len(a) != len("cl_")+8 {
		t.Errorf("name shape = %q, want cl_<8hex>", a)
	}
	b, _ := newChangelogName()
	if a == b {
		t.Errorf("names should be unique, both %q", a)
	}
}
