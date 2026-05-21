// Package config holds the TOML-backed configuration for both V5
// binaries. Defaults live in code; an optional file overrides them;
// env vars override the file. This is the layered-config pattern V4
// used — no surprises, no implicit precedence.
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Server is the runtime configuration for satellites-server.
//
// File: satellites-server.toml (operator-supplied, optional).
// Env overrides match the field-level `env:` tags on each member;
// they are applied AFTER the file decode so env always wins.
type Server struct {
	Addr          string `toml:"addr"`
	DSN           string `toml:"dsn"`
	Dev           bool   `toml:"dev"`
	SessionSecret string `toml:"session_secret"`

	OAuth ServerOAuth `toml:"oauth"`
	Log   LogConfig   `toml:"log"`
}

// ServerOAuth holds OAuth provider credentials + bootstrap policy.
//
// RedirectBaseURL is the public origin (scheme + host[:port]) the
// provider should redirect back to — e.g. https://satellites.fly.dev
// in prod, http://localhost:8080 in dev. Required for OAuth to work;
// missing/empty disables OAuth start/callback routes.
//
// AdminEmails is a comma-separated list of emails that are promoted
// to RoleAdmin on first OAuth sign-in. Solves the prod-bootstrap
// problem without manual SQL.
type ServerOAuth struct {
	GitHubClientID     string `toml:"github_client_id"`
	GitHubClientSecret string `toml:"github_client_secret"`
	GoogleClientID     string `toml:"google_client_id"`
	GoogleClientSecret string `toml:"google_client_secret"`
	RedirectBaseURL    string `toml:"redirect_base_url"`
	AdminEmails        string `toml:"admin_emails"`
}

// LogConfig drives the arbor logger setup. Shared by both binaries.
type LogConfig struct {
	Level string `toml:"level"` // debug | info | warn | error
	JSON  bool   `toml:"json"`  // true for structured output (prod/Fly)
}

// ServerDefaults returns the in-code defaults. Operators who supply
// no config file and set no env vars get exactly this.
func ServerDefaults() Server {
	return Server{
		Addr: ":8080",
		DSN:  "postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable",
		Dev:  false,
		Log: LogConfig{
			Level: "info",
			JSON:  false,
		},
	}
}

// LoadServer applies the layered-config pattern:
//
//  1. Start from ServerDefaults.
//  2. If path is non-empty AND the file exists, decode it over the
//     defaults (TOML zero-values do not clobber set defaults — the
//     decoder only assigns keys present in the file).
//  3. Apply env-var overrides last.
//
// An explicit path that does not exist is an error. An empty path
// means "no file" — defaults + env only.
func LoadServer(path string) (Server, error) {
	cfg := ServerDefaults()

	if path != "" {
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				return cfg, fmt.Errorf("config: stat %s: %w", path, err)
			}
			// File absent: defaults + env only.
		} else {
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return cfg, fmt.Errorf("config: decode %s: %w", path, err)
			}
		}
	}

	cfg.applyServerEnv()
	return cfg, nil
}

func (s *Server) applyServerEnv() {
	if v := os.Getenv("SATELLITES_LISTEN_ADDR"); v != "" {
		s.Addr = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		s.DSN = v
	}
	if v := os.Getenv("SATELLITES_DEV"); v != "" {
		s.Dev = v == "true" || v == "1"
	}
	if v := os.Getenv("SATELLITES_SESSION_SECRET"); v != "" {
		s.SessionSecret = v
	}
	if v := os.Getenv("GITHUB_OAUTH_CLIENT_ID"); v != "" {
		s.OAuth.GitHubClientID = v
	}
	if v := os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"); v != "" {
		s.OAuth.GitHubClientSecret = v
	}
	if v := os.Getenv("GOOGLE_OAUTH_CLIENT_ID"); v != "" {
		s.OAuth.GoogleClientID = v
	}
	if v := os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"); v != "" {
		s.OAuth.GoogleClientSecret = v
	}
	if v := os.Getenv("SATELLITES_OAUTH_REDIRECT_BASE_URL"); v != "" {
		s.OAuth.RedirectBaseURL = v
	}
	if v := os.Getenv("SATELLITES_ADMIN_EMAILS"); v != "" {
		s.OAuth.AdminEmails = v
	}
	if v := os.Getenv("SATELLITES_LOG_LEVEL"); v != "" {
		s.Log.Level = v
	}
	if v := os.Getenv("SATELLITES_LOG_JSON"); v != "" {
		s.Log.JSON = v == "true" || v == "1"
	}
}
