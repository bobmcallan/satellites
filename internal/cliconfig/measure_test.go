package cliconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func boolp(b bool) *bool { return &b }

func TestMeasureDefaultsOn(t *testing.T) {
	// Absent [measure] section ⇒ zero MeasureConfig ⇒ enabled, record.
	var m MeasureConfig
	if !m.IsEnabled() {
		t.Fatal("absent [measure] should default ON")
	}
	if m.ResolveMode() != "record" {
		t.Fatalf("default mode = %q, want record", m.ResolveMode())
	}
}

func TestMeasureEnabledMatrix(t *testing.T) {
	cases := []struct {
		name string
		m    MeasureConfig
		want bool
	}{
		{"default", MeasureConfig{}, true},
		{"explicit enabled", MeasureConfig{Enabled: boolp(true)}, true},
		{"explicit disabled", MeasureConfig{Enabled: boolp(false)}, false},
		{"mode off disables", MeasureConfig{Mode: "off"}, false},
		{"mode enforce on", MeasureConfig{Mode: "enforce"}, true},
	}
	for _, c := range cases {
		if got := c.m.IsEnabled(); got != c.want {
			t.Errorf("%s: IsEnabled = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoadRejectsInvalidMeasureMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "satellites.toml")
	if err := os.WriteFile(cfgPath, []byte("server_url=\"http://x\"\n[measure]\nmode=\"bogus\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(cfgPath); err == nil {
		t.Fatal("expected error for invalid measure.mode")
	}

	// a valid mode loads fine
	if err := os.WriteFile(cfgPath, []byte("server_url=\"http://x\"\n[measure]\nmode=\"record\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(cfgPath); err != nil {
		t.Fatalf("valid mode should load, got %v", err)
	}
}

func TestMeasureLogDirOverrideAndFallback(t *testing.T) {
	// explicit measure.log_dir wins, repo-relative
	c := Config{Measure: MeasureConfig{LogDir: "obs"}}
	if got, want := c.MeasureLogDir("/repo"), filepath.Join("/repo", "obs"); got != want {
		t.Errorf("override: got %q want %q", got, want)
	}
	// fallback to log_path / default
	c = Config{}
	if got, want := c.MeasureLogDir("/repo"), filepath.Join("/repo", DefaultLogDir); got != want {
		t.Errorf("fallback: got %q want %q", got, want)
	}
}
