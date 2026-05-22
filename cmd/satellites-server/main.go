package main

import (
	"context"
	"database/sql"
	"flag"
	"net/http"
	"time"

	"github.com/bobmcallan/satellites/config/seed"
	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	_ "github.com/lib/pq"
)

func main() {
	// Single flag — points at the TOML config file. Defaults in code,
	// file overrides defaults, env overrides file. Anything operators
	// want to tweak per-deploy goes through here.
	configPath := flag.String("config", "satellites-server.toml",
		"Path to satellites-server.toml (defaults applied when absent)")
	flag.Parse()

	cfg, err := config.LoadServer(*configPath)
	if err != nil {
		// Default logger is still the boot bootstrap — fine for this
		// one-shot error.
		arbor.Errf("load config: %v", err)
		arbor.Fatal("aborting")
	}

	// Wire arbor as the process-global logger now that we know the
	// caller's preference (level + json/text).
	logger := arbor.New(arbor.ParseLevel(cfg.Log.Level), cfg.Log.JSON, nil)
	arbor.SetDefault(logger)
	arbor.Info("satellites-server starting",
		"addr", cfg.Addr,
		"dev", cfg.Dev,
		"log_level", cfg.Log.Level,
		"log_json", cfg.Log.JSON,
	)

	if err := db.MigrateUp(cfg.DSN); err != nil {
		arbor.Fatal("migrate", "err", err)
	}

	sqlDB, err := sql.Open("postgres", cfg.DSN)
	if err != nil {
		arbor.Fatal("db open", "err", err)
	}
	defer sqlDB.Close()

	store := auth.New(sqlDB)
	verb.SetAuthStore(store)

	// Workspace store + boot-time default-workspace seed. The first
	// boot mints a NULL-owner workspace named "default"; subsequent
	// boots are no-ops.
	wsStore := workspace.New(sqlDB)
	defaultWs, err := workspace.SeedDefault(context.Background(), wsStore, time.Now().UTC())
	if err != nil {
		arbor.Fatal("workspace: seed default", "err", err)
	}
	arbor.Info("workspace default ready", "id", defaultWs.ID, "name", defaultWs.Name)
	verb.SetWorkspaceStore(wsStore)

	verb.SetProjectStore(project.New(sqlDB))
	verb.SetStoryStore(story.New(sqlDB))

	// Document substrate: wire the store + seed the embedded system
	// artifacts. SeedSystem is idempotent — a no-op when the embedded
	// markdown matches the latest active version on disk.
	docStore := document.New(sqlDB)
	verb.SetDocumentStore(docStore)
	for _, sd := range []struct {
		name string
		body []byte
	}{
		{"satellites_client_install", seed.ClientInstallMarkdown()},
		{"satellites_mcp_load_context", seed.MCPLoadContextMarkdown()},
	} {
		if err := document.SeedSystem(context.Background(), docStore, sd.name, string(sd.body), "system:seed", time.Now().UTC()); err != nil {
			arbor.Fatal("seed system document", "name", sd.name, "err", err)
		}
	}
	arbor.Info("system documents seeded")

	verb.SetVariableStore(variable.New(sqlDB))

	if cfg.Dev {
		if err := store.DevSeed(context.Background()); err != nil {
			arbor.Fatal("dev seed", "err", err)
		}
		arbor.Info("dev mode: seeded admin + user",
			"admin_email", auth.DevAdminEmail,
			"user_email", auth.DevUserEmail,
			"admin_key", auth.DevAdminKey,
			"user_key", auth.DevUserKey,
		)
	}

	// Session secret: env wins (handled by LoadServer); otherwise
	// load-or-create from server_settings so sessions survive restarts.
	var sessionSecret []byte
	if cfg.SessionSecret != "" {
		sessionSecret, err = auth.SecretFromHex(cfg.SessionSecret)
		if err != nil {
			arbor.Fatal("session secret: must be hex-encoded", "err", err)
		}
		arbor.Info("session secret loaded from config/env")
	} else {
		sessionSecret, err = auth.LoadOrCreateSessionSecret(context.Background(), sqlDB)
		if err != nil {
			arbor.Fatal("load session secret from db", "err", err)
		}
		arbor.Info("session secret loaded from server_settings (db-backed)")
	}
	sessions := auth.NewSessions(sessionSecret)

	oauthCfg := auth.OAuthConfig{
		GitHubClientID:     cfg.OAuth.GitHubClientID,
		GitHubClientSecret: cfg.OAuth.GitHubClientSecret,
		GoogleClientID:     cfg.OAuth.GoogleClientID,
		GoogleClientSecret: cfg.OAuth.GoogleClientSecret,
		RedirectBaseURL:    cfg.OAuth.RedirectBaseURL,
		AdminEmails:        auth.ParseAdminEmails(cfg.OAuth.AdminEmails),
	}
	providers := auth.BuildProviderSet(oauthCfg)
	for _, p := range providers.Enabled() {
		arbor.Info("oauth provider enabled", "provider", p.Name, "redirect", p.OAuth2.RedirectURL)
	}
	// Surface partial-config silently-disabled providers — common
	// footgun is setting *_CLIENT_ID / *_CLIENT_SECRET but forgetting
	// SATELLITES_OAUTH_REDIRECT_BASE_URL, which leaves the provider
	// off with no obvious clue why no button shows in the UI.
	if oauthCfg.RedirectBaseURL == "" &&
		(oauthCfg.GitHubClientID != "" || oauthCfg.GoogleClientID != "") {
		arbor.Warn("oauth: client credentials present but SATELLITES_OAUTH_REDIRECT_BASE_URL is empty — providers disabled")
	}
	if oauthCfg.GitHubClientID != "" && oauthCfg.GitHubClientSecret == "" {
		arbor.Warn("oauth: GITHUB_OAUTH_CLIENT_ID set without GITHUB_OAUTH_CLIENT_SECRET — github disabled")
	}
	if oauthCfg.GoogleClientID != "" && oauthCfg.GoogleClientSecret == "" {
		arbor.Warn("oauth: GOOGLE_OAUTH_CLIENT_ID set without GOOGLE_OAUTH_CLIENT_SECRET — google disabled")
	}
	if len(oauthCfg.AdminEmails) > 0 {
		arbor.Info("oauth admin emails configured", "count", len(oauthCfg.AdminEmails))
	}

	// OAuth Authorization Server: signs access tokens for the MCP
	// surface. JWT secret persists in server_settings so tokens
	// survive restarts; api-keys remain a parallel valid credential
	// (the CLI uses an api-key, the MCP SDK uses a JWT).
	jwtSecret, err := auth.LoadOrCreateJWTSecret(context.Background(), sqlDB)
	if err != nil {
		arbor.Fatal("load jwt secret", "err", err)
	}
	store.SetJWTSecret(jwtSecret)
	oauthServer := auth.NewOAuthServer(auth.OAuthServerConfig{
		JWTSecret:       jwtSecret,
		AccessTokenTTL:  1 * time.Hour,
		RefreshTokenTTL: 7 * 24 * time.Hour,
		CodeTTL:         10 * time.Minute,
		Store:           auth.NewOAuthStore(sqlDB),
		DevMode:         cfg.Dev,
		ResolveSessionUser: func(r *http.Request) string {
			id, err := sessions.UserID(r)
			if err != nil {
				return ""
			}
			return id
		},
		UserByID: func(ctx context.Context, userID string) (*auth.User, error) {
			return store.GetUserByID(ctx, userID)
		},
	})
	arbor.Info("oauth authorization server ready")

	handler := server.Build(server.Config{
		Store:       store,
		Sessions:    sessions,
		DevMode:     cfg.Dev,
		OAuth:       oauthCfg,
		Providers:   providers,
		OAuthStates: auth.NewStateStore(0),
		OAuthServer: oauthServer,
	})

	arbor.Info("satellites-server listening", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		arbor.Fatal("http listen", "err", err)
	}
}
