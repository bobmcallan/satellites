package cli

import (
	"strings"
	"testing"
)

func TestReleaseCheckVerdict(t *testing.T) {
	t.Run("tag missing → clean (version already bumped)", func(t *testing.T) {
		ok, report := releaseCheckVerdict("0.0.165", false, nil)
		if !ok {
			t.Fatalf("want clean, got blocked: %s", report)
		}
		if !strings.Contains(report, "unreleased") {
			t.Errorf("report should explain the version is unreleased: %s", report)
		}
	})

	t.Run("tag exists, no client change → clean", func(t *testing.T) {
		ok, report := releaseCheckVerdict("0.0.164", true, nil)
		if !ok {
			t.Fatalf("want clean, got blocked: %s", report)
		}
		if !strings.Contains(report, "no client") {
			t.Errorf("report should note no client release needed: %s", report)
		}
	})

	t.Run("tag exists + client change → blocked", func(t *testing.T) {
		ok, report := releaseCheckVerdict("0.0.164", true, []string{"internal/verb/workspace.go"})
		if ok {
			t.Fatalf("want blocked, got clean: %s", report)
		}
		if !strings.Contains(report, "internal/verb/workspace.go") {
			t.Errorf("report should list the changed path: %s", report)
		}
		if !strings.Contains(report, "BLOCKED") {
			t.Errorf("report should be BLOCKED: %s", report)
		}
	})
}

func TestParseSatellitesVersion(t *testing.T) {
	body := `# comment
satellites.version: 0.0.164
satellites.build: 2026-06-08-12-09-29
satellites-server.version: 0.0.186
satellites-server.build: 2026-06-08-11-58-49
`
	v, err := parseSatellitesVersion(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v != "0.0.164" {
		t.Fatalf("version = %q, want 0.0.164", v)
	}

	if _, err := parseSatellitesVersion("satellites-server.version: 0.0.1\n"); err == nil {
		t.Error("missing satellites.version should error")
	}
}
