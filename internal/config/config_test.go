package config

import (
	"testing"
)

func TestLoad_Happy_DevDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want \"dev\"", cfg.Env)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want \"info\"", cfg.LogLevel)
	}
	if !cfg.DevMode {
		t.Errorf("DevMode = false, want true (default dev env)")
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("ENV", "prod")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DB_DSN", "ws://db.internal:8000/rpc")
	t.Setenv("FLY_MACHINE_ID", "1234abcd")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want \"prod\"", cfg.Env)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want \"debug\"", cfg.LogLevel)
	}
	if cfg.DBDSN != "ws://db.internal:8000/rpc" {
		t.Errorf("DBDSN = %q", cfg.DBDSN)
	}
	if cfg.FlyMachineID != "1234abcd" {
		t.Errorf("FlyMachineID = %q", cfg.FlyMachineID)
	}
	if cfg.DevMode {
		t.Errorf("DevMode = true, want false in prod with default DEV_MODE")
	}
}

func TestLoad_MissingDBDSN_InProd(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", "prod")
	_, err := Load()
	if err == nil {
		t.Fatalf("Load() = nil, want error about DB_DSN")
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "abc")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on non-numeric PORT")
	}

	t.Setenv("PORT", "99999")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on out-of-range PORT")
	}
}

func TestLoad_InvalidEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", "staging")
	if _, err := Load(); err == nil {
		t.Fatalf("Load() = nil, want error on unknown ENV")
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PORT", "SATELLITES_PORT", "ENV", "LOG_LEVEL", "DEV_MODE", "DB_DSN", "FLY_MACHINE_ID"} {
		t.Setenv(k, "")
	}
}
