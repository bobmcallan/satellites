package main

import (
	"context"
	"database/sql"
	"flag"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/bobmcallan/satellites/config/reviewers"
	"github.com/bobmcallan/satellites/config/seed"
	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/changelog"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/server"
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
	verb.SetLedgerStore(ledger.New(sqlDB))
	clStore := changelog.New(sqlDB)
	verb.SetChangelogStore(clStore)
	server.SetChangelogStore(clStore)

	// Reviewer registry — load every markdown definition embedded
	// under config/reviewers/, then wire either the production
	// Anthropic client (when ANTHROPIC_API_KEY is set) or no client
	// at all. Without a client, dispatch is a no-op so the
	// substrate boots cleanly in environments without LLM creds.
	reviewerDefs, err := reviewer.Load(reviewers.FS)
	if err != nil {
		arbor.Fatal("reviewer load", "err", err)
	}
	var reviewerClient reviewer.Client
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		reviewerClient = reviewer.NewAnthropicClient(key)
		arbor.Info("reviewer client wired (anthropic)", "defs", len(reviewerDefs))
	} else {
		arbor.Info("reviewer client absent — ANTHROPIC_API_KEY unset, reviews are no-ops",
			"defs", len(reviewerDefs))
	}
	verb.SetReviewerRegistry(reviewer.NewRegistry(reviewerDefs, reviewerClient))

	// story_request_review verb runs gate skills via `claude -p`. The
	// dispatcher is wired here so the verb has a transport on boot;
	// individual test setups inject a stub via verb.SetGateDispatcher.
	verb.SetGateDispatcher(verb.ClaudeCLIGateDispatcher{
		DefaultTimeout: 5 * time.Minute,
	})
	arbor.Info("gate dispatcher wired (claude -p)")

	// Document substrate + system-seed registry: wire stores then
	// reconcile each embedded artifact. ReconcileSystemSeed is
	// idempotent — a no-op when the system_seeds.embedded_hash matches
	// the embed.FS body hash. A binary release that changes one
	// artifact rewrites exactly that system_seeds row and appends one
	// document_versions row.
	docStore := document.New(sqlDB)
	verb.SetDocumentStore(docStore)
	sysSeedStore := document.NewSystemSeedStore(sqlDB)
	verb.SetSystemSeedStore(sysSeedStore)
	for _, sd := range []struct {
		name string
		body []byte
	}{
		{"satellites_client_install", seed.ClientInstallMarkdown()},
		{"satellites_mcp_load_context", seed.MCPLoadContextMarkdown()},
		{"satellites_mcp_reference_dispatch", seed.MCPReferenceDispatchMarkdown()},
		{"satellites_mcp_reference_documents", seed.MCPReferenceDocumentsMarkdown()},
		{"system_variables", seed.SystemVariablesMarkdown()},
		{"principle-configuration-over-code", seed.PrincipleConfigurationOverCodeMarkdown()},
	} {
		fm, body, err := frontmatter.Parse(sd.body)
		if err != nil {
			arbor.Fatal("parse system seed frontmatter", "name", sd.name, "err", err)
		}
		res, err := document.ReconcileSystemSeed(context.Background(), sysSeedStore, docStore, sd.name, string(body), fm.Tags, "system:seed", time.Now().UTC())
		if err != nil {
			arbor.Fatal("reconcile system seed", "name", sd.name, "err", err)
		}
		switch {
		case res.Created:
			arbor.Info("system seed installed", "name", sd.name)
		case res.Changed:
			arbor.Info("system seed updated (drift)", "name", sd.name)
		default:
			arbor.Info("system seed unchanged", "name", sd.name)
		}
	}
	arbor.Info("system seeds reconciled")

	variableStore := variable.New(sqlDB)
	verb.SetVariableStore(variableStore)

	// Stored-system KV seed registry — operator-tunable defaults that
	// land in the variables table at scope='system'. Seed is idempotent
	// on name presence: a re-boot does NOT overwrite an operator-set
	// value. Add new knobs here; the consumer reads via
	// variable_get(scope='system', name=...).
	for name, defaultValue := range map[string]string{
		"stories.page_size":             "50",
		"changelog.page_size":           "20",
		"mcp_instructions_budget_bytes": "1500",
	} {
		created, err := variableStore.SeedSystem(context.Background(), name, defaultValue, time.Now().UTC())
		if err != nil {
			arbor.Fatal("variable: seed system", "name", name, "err", err)
		}
		if created {
			arbor.Info("system kv seeded", "name", name, "default", defaultValue)
		} else {
			arbor.Info("system kv unchanged", "name", name)
		}
	}

	// Push the operator-tunable MCP initialize budget into the
	// mcpserver package before server.Build() constructs the MCP
	// handler. Falls back to the package default on parse failure or
	// missing row.
	if v, err := variableStore.Get(context.Background(), variable.Key{Scope: variable.ScopeSystem, Name: "mcp_instructions_budget_bytes"}); err == nil {
		if n, perr := strconv.Atoi(v.Value); perr == nil {
			mcpserver.SetInstructionsBudget(n)
			arbor.Info("mcp instructions budget wired", "bytes", n)
		} else {
			arbor.Warn("mcp instructions budget unparseable; using default",
				"value", v.Value, "default", mcpserver.DefaultInstructionsBudgetBytes, "err", perr)
		}
	}

	// System-variables resolver: the computed values document_get
	// substitutes into {{name}} placeholders. Read-only by contract;
	// operators who need an overridable knob set a workspace/project
	// variable, which takes precedence over the system layer for
	// variable_get(inherit=true) but never overrides the system layer
	// inside a templated document body.
	publicURL := cfg.OAuth.RedirectBaseURL
	if v := os.Getenv("SATELLITES_PUBLIC_URL"); v != "" {
		publicURL = v
	}
	systemVars := map[string]func(ctx context.Context) string{
		"version":         func(context.Context) string { return verb.Version },
		"cli_version":     func(context.Context) string { return verb.CLIVersionEffective() },
		"os":              func(ctx context.Context) string { return verb.OSFromContext(ctx) },
		"arch":            func(ctx context.Context) string { return verb.ArchFromContext(ctx) },
		"server_url":      func(context.Context) string { return publicURL },
		"current_version": func(ctx context.Context) string { return verb.CurrentVersionFromContext(ctx) },
		"state": func(ctx context.Context) string {
			return verb.ComputeInstallState(verb.CurrentVersionFromContext(ctx), verb.CLIVersionEffective())
		},
	}
	systemVarNames := make([]string, 0, len(systemVars))
	for k := range systemVars {
		systemVarNames = append(systemVarNames, k)
	}
	sort.Strings(systemVarNames)
	verb.SetSystemVariableResolver(
		func(ctx context.Context, name string) (string, bool) {
			fn, ok := systemVars[name]
			if !ok {
				return "", false
			}
			return fn(ctx), true
		},
		func(context.Context) []string {
			out := make([]string, len(systemVarNames))
			copy(out, systemVarNames)
			return out
		},
	)
	arbor.Info("system variables resolver wired", "names", systemVarNames)

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
		OAuthStates: auth.NewPGStateStore(sqlDB, 0),
		OAuthServer: oauthServer,
	})

	arbor.Info("satellites-server listening", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		arbor.Fatal("http listen", "err", err)
	}
}
