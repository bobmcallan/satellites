package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	substrate "github.com/bobmcallan/satellites/config"
	"github.com/bobmcallan/satellites/internal/agent"
	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/blob"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/embed"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/ingest"
	"github.com/bobmcallan/satellites/internal/invitation"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/live"
	"github.com/bobmcallan/satellites/internal/llmcfg"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/synth"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	_ "github.com/lib/pq"
)

// serverHomedSeed decides whether an embedded config/ artifact is the
// SERVER's to mirror into the system documents table, or the CLIENT
// binary's own concern (epic:system-substrate). The server keeps the
// MCP-service docs (mcp/), principles, the server-machinery docs, and
// the summariser reviewer it runs; it drops the gate reviewers
// (named *-review) and the authored kind:contract docs, both of which
// resolve client-side from the binary embed.
func serverHomedSeed(docType, name string, tags []string) bool {
	if docType == document.TypeSkill && strings.HasSuffix(name, "-review") {
		return false // client-resolved gate reviewer — embed is the source
	}
	for _, t := range tags {
		if t == "kind:contract" {
			return false // client-embed authored contract (e.g. satellites_surface_contract)
		}
	}
	return true
}

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

	// Workspace store. The shared boot-seeded default was retired in favour
	// of a personal workspace per user (epic:user-admin, sty_3a54bf42);
	// personal workspaces are minted on first login (ProvisionLogin below)
	// and backfilled by migration 0028.
	wsStore := workspace.New(sqlDB)
	verb.SetWorkspaceStore(wsStore)

	invStore := invitation.New(sqlDB)
	verb.SetInvitationStore(invStore)

	verb.SetProjectStore(project.New(sqlDB))
	blobStore := blob.New(sqlDB) // binary ingestion (sty_59652a7d)
	ledgerStore := ledger.New(sqlDB)
	verb.SetLedgerStore(ledgerStore)

	// The server-side story_request_review driver was retired (sty_6e1c3641):
	// the reviewer gate runs client-side (satellites story review), spawning
	// its own claude -p, so the server no longer wires a gate dispatcher.

	// Document substrate + system-seed registry: wire stores then
	// reconcile every artifact embedded under config/ (documents/,
	// principles/, skills/ subdirs). Each
	// file declares its identity (`scope`, `name`, optional tags) in
	// frontmatter; the reconciler dispatches by scope rather than by
	// directory layout. ReconcileSystemSeed is idempotent — a no-op
	// when the system_seeds.embedded_hash matches the embed.FS body
	// hash. A binary release that changes one artifact rewrites
	// exactly that system_seeds row and appends one document_versions
	// row.
	docStore := document.New(sqlDB)
	verb.SetDocumentStore(docStore)
	sysSeedStore := document.NewSystemSeedStore(sqlDB)
	verb.SetSystemSeedStore(sysSeedStore)

	// Walk the embed FS recursively: artifacts are filed by type into
	// config/{documents,principles,skills}/ subdirs, so the reconcile must
	// descend into them rather than read a flat top-level directory.
	var docFilenames []string
	if err := fs.WalkDir(substrate.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		docFilenames = append(docFilenames, path)
		return nil
	}); err != nil {
		arbor.Fatal("walk config embed FS", "err", err)
	}
	sort.Strings(docFilenames)
	// keep collects every embedded artifact's resolved name so the prune
	// pass (after the loop) can remove system_seeds rows whose embed file
	// was retired — making removal symmetrical with addition (sty_a1a74121).
	keep := make(map[string]bool, len(docFilenames))
	for _, filename := range docFilenames {
		raw, err := fs.ReadFile(substrate.FS, filename)
		if err != nil {
			arbor.Fatal("read embedded document", "file", filename, "err", err)
		}
		fm, body, err := frontmatter.Parse(raw)
		if err != nil {
			arbor.Fatal("parse document frontmatter", "file", filename, "err", err)
		}
		name := fm.Name
		if name == "" {
			name = strings.TrimSuffix(filename, ".md")
		}
		scope := fm.Scope
		if scope == "" {
			scope = "system"
		}
		if scope != "system" {
			arbor.Fatal("config embed currently only supports scope=system",
				"file", filename, "scope", scope)
		}
		docType := fm.Type
		if docType == "" {
			docType = document.TypeDocument
		}
		// epic:system-substrate — the server no longer mirrors the
		// client's authored-process GATE substrate into system rows. A
		// reviewer gate (named *-review) resolves client-side from the
		// binary embed (embed → .claude/skills → server fallback), so a
		// duplicate server row is dead weight; omitting it from `keep`
		// lets the prune pass below tombstone any that linger. What STAYS
		// server-homed: the MCP-service docs under mcp/, principles
		// (served to CLI-less MCP clients), the server-machinery docs
		// (agent-operating-prompt / system_variables), and the summariser
		// reviewer satellites-story-summary, which the summary hook runs
		// from the boot-loaded registry. The kind:contract authored docs
		// (e.g. satellites_surface_contract) are client-embed only.
		if !serverHomedSeed(docType, name, fm.Tags) {
			arbor.Info("system seed skipped (client-homed authored substrate)", "name", name)
			continue
		}
		// Skills preserve their frontmatter in the stored body so the
		// document mirror is byte-identical to the SKILL.md a client
		// would materialise into .claude/skills/<name>/SKILL.md.
		// Documents strip frontmatter (substrate keys are not part of
		// the rendered body for templated docs).
		var storedBody string
		if docType == document.TypeSkill {
			storedBody = string(raw)
		} else {
			storedBody = string(body)
		}
		keep[name] = true
		res, err := document.ReconcileSystemSeedTyped(context.Background(), sysSeedStore, docStore, docType, name, storedBody, fm.Tags, fm.Headline, "system:seed", time.Now().UTC())
		if err != nil {
			arbor.Fatal("reconcile system seed", "name", name, "err", err)
		}
		switch {
		case res.Created:
			arbor.Info("system seed installed", "name", name)
		case res.Changed:
			arbor.Info("system seed updated (drift)", "name", name)
		default:
			arbor.Info("system seed unchanged", "name", name)
		}
	}
	// Prune: a system_seeds row whose embed file was removed is retired here,
	// the mirror of the upsert above — so a removed artifact propagates as a
	// removed row on the next deploy instead of lingering immortal
	// (sty_a1a74121). Scoped to system_seeds-tracked names only.
	pruned, err := document.PruneSystemSeeds(context.Background(), sysSeedStore, docStore, keep, "system:prune", time.Now().UTC())
	if err != nil {
		arbor.Fatal("prune retired system seeds", "err", err)
	}
	for _, name := range pruned {
		arbor.Info("system seed pruned (embed removed)", "name", name)
	}
	arbor.Info("system seeds reconciled", "pruned", len(pruned))

	// Reviewer registry — load every type:"skill" row tagged
	// `kind:reviewer` from the documents store (the reconciler seeded
	// these from config/ above), then wire either the
	// production Anthropic client (when ANTHROPIC_API_KEY is set) or
	// no client at all. Without a client, dispatch is a no-op so the
	// substrate boots cleanly in environments without LLM creds.
	reviewerDefs, err := reviewer.LoadFromStore(context.Background(), docStore)
	if err != nil {
		arbor.Fatal("reviewer load from store", "err", err)
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
		"ledger_buffer_size":            "4096",
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

	// Operator-observability ledger handler (sty_0006f5f5). Replaces the
	// default arbor logger with a tee that fans every log record out to
	// both stderr (existing behaviour) and the LedgerHandler. The handler
	// persists records whose context carries correlation ids (set by the
	// HTTP correlationMiddleware) as evidence ledger rows so the portal
	// /ledger page (sty_becb7019) can surface them.
	level := arbor.ParseLevel(cfg.Log.Level)
	bufferSize := 4096
	if v, err := variableStore.Get(context.Background(), variable.Key{Scope: variable.ScopeSystem, Name: "ledger_buffer_size"}); err == nil {
		if n, perr := strconv.Atoi(v.Value); perr == nil && n > 0 {
			bufferSize = n
		} else {
			arbor.Warn("ledger_buffer_size unparseable; using default",
				"value", v.Value, "default", bufferSize, "err", perr)
		}
	}
	ledgerHandler := ledger.NewHandler(ledgerStore, ledger.HandlerOptions{
		MinLevel:   level,
		BufferSize: bufferSize,
	})
	arbor.SetHandlers(level,
		arbor.NewStderrHandler(level, cfg.Log.JSON, nil),
		ledgerHandler,
	)
	arbor.Info("ledger handler wired", "buffer_size", bufferSize)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ledgerHandler.Close(ctx); err != nil {
			// Use stderr directly; arbor itself is shutting down so we
			// can't route this through it.
			fmt.Fprintf(os.Stderr, "ledger handler close: %v\n", err)
		}
	}()

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

	// SSE trigger bus (sty_b6e39eb8): one Postgres LISTEN connection per
	// instance fans NOTIFYs to the in-memory hub, which the session-gated
	// /events stream subscribes to. Opened on its own pq connection, separate
	// from the app sql.DB pool.
	liveHub := live.NewHub()
	liveListener, err := live.NewListener(cfg.DSN, liveHub)
	if err != nil {
		arbor.Fatal("live: start listener", "err", err)
	}
	defer func() { _ = liveListener.Close() }()
	arbor.Info("live trigger bus ready", "channel", live.EventsChannel)

	// liveScope resolves a session user's /events topic scope: an admin sees
	// every topic; everyone else only their workspaces' topics. Injected here
	// (not as a store on server.Config) so the transport package stays free of
	// substrate-domain imports per the layering guard.
	liveScope := func(ctx context.Context, userID string) (live.Scope, error) {
		if u, err := store.GetUserByID(ctx, userID); err == nil && u != nil && u.Role == auth.RoleAdmin {
			return live.Scope{Admin: true}, nil
		}
		ids, err := wsStore.ListWorkspaceIDsForUser(ctx, userID)
		if err != nil {
			return live.Scope{}, err
		}
		return live.NewScope(false, ids), nil
	}

	// LLM config as substrate (epic:workspace-agents, sty_a6983e32): the
	// Gemini key + model resolve at REQUEST time from the variable store —
	// the key as a SECRET variable (gemini.api_key), the model as a plain
	// stored-system knob (<kind>.model) — each with an env fallback so the
	// deployed fly secret keeps working until the variable is set, and the
	// variable winning when present. Rotating the key or switching the model
	// then takes effect without a redeploy.
	llmResolver := llmcfg.New(variableStore, os.Getenv)
	geminiKey := func(ctx context.Context) string {
		return llmResolver.Secret(ctx, "gemini.api_key", "GEMINI_API_KEY")
	}
	embedModelDefault := cfg.Embedding.Model
	if strings.TrimSpace(embedModelDefault) == "" {
		embedModelDefault = embed.DefaultModel
	}

	// The agent + objective executors are always wired with the resolver so a
	// key set as a secret variable, and a model switched via a stored-system
	// variable, take effect at request time. A blank resolved key surfaces as
	// the existing clear not-run note from each verb, not a crash.
	verb.SetObjectiveService(synth.NewObjectiveService(
		synth.NewGeminiGeneratorFunc(func(ctx context.Context) (string, string) {
			return geminiKey(ctx), llmResolver.Model(ctx, "synth", synth.DefaultGenerationModel)
		}), docStore))
	verb.SetAgentModel(agent.NewGeminiAgentModelFunc(func(ctx context.Context) (string, string) {
		return geminiKey(ctx), llmResolver.Model(ctx, "agent", agent.DefaultModel)
	}))

	// Workspace corpus embeddings (sty_7f4f7e11): the reconcile worker is a
	// long-running goroutine, so it is gated on a key resolved at boot (env or
	// the secret variable). Absent key → embeddings disabled and the corpus is
	// stored unembedded (graceful). The embedder itself still resolves key +
	// model per request; the vector dimension stays fixed.
	if bootKey := strings.TrimSpace(geminiKey(context.Background())); bootKey != "" {
		embedder := embed.NewGeminiEmbedderFunc(func(ctx context.Context) (string, string) {
			return geminiKey(ctx), llmResolver.Model(ctx, "embedding", embedModelDefault)
		}, cfg.Embedding.Dimension)
		embedSvc := embed.NewService(
			embedder,
			embed.NewChunkStore(sqlDB), docStore, wsStore,
			cfg.Embedding.ChunkMaxTokens, cfg.Embedding.ChunkOverlap,
		)
		verb.SetEmbedService(embedSvc) // semantic_search dispatches through this (sty_e8db6032)
		go embed.NewWorker(embedSvc, 30*time.Second).Run(context.Background())
		arbor.Info("embedding worker started", "model", embedModelDefault, "dim", cfg.Embedding.Dimension)

		// Scheduled agent-tasks (sty_7bf60667): the scheduler ticks every 30s,
		// running trigger:"schedule" tasks on their interval through the shared
		// run path. Gated with the embed worker — a scheduled run needs the
		// server executor (Gemini key present).
		go verb.NewTaskScheduler(30*time.Second, ledgerStore).Run(context.Background())
		arbor.Info("task scheduler started", "tick", "30s")
	} else {
		arbor.Info("embeddings disabled — no gemini.api_key variable and GEMINI_API_KEY unset")
	}

	handler := server.Build(server.Config{
		Store:       store,
		Sessions:    sessions,
		DevMode:     cfg.Dev,
		OAuth:       oauthCfg,
		Providers:   providers,
		OAuthStates: auth.NewPGStateStore(sqlDB, 0),
		OAuthServer: oauthServer,
		Live:        liveHub,
		LiveScope:   liveScope,
		ProvisionLogin: func(ctx context.Context, userID, email, displayName string) error {
			if _, err := wsStore.EnsurePersonalWorkspace(ctx, userID, displayName, time.Now().UTC()); err != nil {
				return err
			}
			// Claim any pending invitations for this verified email.
			_, err := invStore.ClaimForEmail(ctx, email, userID, time.Now().UTC())
			return err
		},
		// Binary ingestion (sty_59652a7d): the transport hands us a BlobUpload;
		// we persist it through the blob store and hand back the reference. Keeps
		// internal/blob out of the server package per the layering guard.
		StoreBlob: func(ctx context.Context, up server.BlobUpload) (server.BlobRef, error) {
			// Delegate to the shared ingest path (sty_3c2f02bf): persist the
			// blob + extract a text document — project-scoped when a project is
			// named, else workspace-corpus scoped. The integration tests
			// exercise the identical function.
			ref, err := ingest.StoreBlobAndExtract(ctx, blobStore, docStore, ingest.Upload{
				WorkspaceID: up.WorkspaceID,
				ProjectID:   up.ProjectID,
				Filename:    up.Filename,
				ContentType: up.ContentType,
				CreatedBy:   up.CreatedBy,
				Content:     up.Content,
			})
			if err != nil {
				return server.BlobRef{}, err
			}
			return server.BlobRef{
				ID:          ref.ID,
				ProjectID:   ref.ProjectID,
				Filename:    ref.Filename,
				ContentType: ref.ContentType,
				SizeBytes:   ref.SizeBytes,
				SHA256:      ref.SHA256,
				DocumentID:  ref.DocumentID,
				Extracted:   ref.Extracted,
			}, nil
		},
		GetBlob: func(ctx context.Context, blobID string) (server.BlobContent, error) {
			b, content, err := blobStore.GetContent(ctx, blobID)
			if err != nil {
				return server.BlobContent{}, err
			}
			return server.BlobContent{
				WorkspaceID: b.WorkspaceID,
				ProjectID:   b.ProjectID,
				Filename:    b.Filename,
				ContentType: b.ContentType,
				Content:     content,
			}, nil
		},
	})

	arbor.Info("satellites-server listening", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		arbor.Fatal("http listen", "err", err)
	}
}
