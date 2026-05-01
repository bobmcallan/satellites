// Command satellites is the satellites server binary. It serves /healthz
// (and future endpoints added by later epics) and shuts down gracefully on
// SIGINT/SIGTERM within a 10-second drain bound.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ternarybob/arbor"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/codeindex"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/configseed"
	"github.com/bobmcallan/satellites/internal/contract"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/dispatcher"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/embeddings"
	"github.com/bobmcallan/satellites/internal/embedworker"
	"github.com/bobmcallan/satellites/internal/httpserver"
	"github.com/bobmcallan/satellites/internal/hub"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/permhook"
	"github.com/bobmcallan/satellites/internal/portal"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/ratelimit"
	"github.com/bobmcallan/satellites/internal/repo"
	"github.com/bobmcallan/satellites/internal/reviewer"
	"github.com/bobmcallan/satellites/internal/rolegrant"
	"github.com/bobmcallan/satellites/internal/session"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/internal/wshandler"
)

func main() {
	startedAt := time.Now()

	cfg, cfgWarnings := config.Load()

	logger := satarbor.New(cfg.LogLevel)
	logger.Info().
		Str("binary", "satellites-server").
		Str("version", config.Version).
		Str("build", config.Build).
		Str("commit", config.GitCommit).
		Str("env", cfg.Env).
		Str("fly_machine_id", os.Getenv("FLY_MACHINE_ID")).
		Msgf("satellites-server %s", config.GetFullVersion())

	if path := cfg.LoadedTOMLPath(); path != "" {
		logger.Info().Str("path", path).Msg("config: loaded TOML")
	}

	for _, w := range cfgWarnings {
		logger.Warn().Str("warning", w).Msg("config: startup warning")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var users auth.UserStore = auth.NewMemoryUserStore()
	var sessions auth.SessionStore = auth.NewMemorySessionStore()
	providers := auth.BuildProviderSet(cfg)
	states := auth.NewStateStore(10 * time.Minute)
	for _, p := range providers.Enabled() {
		// Story_40e3bd27: dual-route registration is the cutover compat
		// layer per pr_no_unrequested_compat. Surface both forms in the
		// boot log so operators can verify the OAuth client config has
		// both Authorized redirect URIs registered.
		logger.Info().
			Str("provider", p.Name).
			Str("legacy_start", "/auth/"+p.Name+"/start").
			Str("legacy_callback", "/auth/"+p.Name+"/callback").
			Str("v3_aligned_login", "/api/auth/login/"+p.Name).
			Str("v3_aligned_callback", "/api/auth/callback/"+p.Name).
			Str("redirect_url", p.OAuth2.RedirectURL).
			Msg("oauth: routes registered (legacy + v3-aligned for cutover)")
	}
	authHandlers := &auth.Handlers{
		Users:        users,
		Sessions:     sessions,
		Logger:       logger,
		Cfg:          cfg,
		Providers:    providers,
		States:       states,
		LoginLimiter: ratelimit.New(10, time.Minute),
	}
	// After the workspace store is wired (below), main() may set
	// authHandlers.OnUserCreated to seed each new user's default workspace.

	// Optional SurrealDB connection + document/project surfaces. When
	// SATELLITES_DB_DSN is empty we keep booting (tests, dev without Surreal) but the
	// MCP doc/project tools are disabled and /healthz omits db_ok.
	var (
		docStore         document.Store
		projStore        project.Store
		ledgerStore      ledger.Store
		storyStore       story.Store
		wsStore          workspace.Store
		contractStore    contract.Store
		sessionStore     session.Store
		grantStore       rolegrant.Store
		taskStore        task.Store
		repoStore        repo.Store
		repoIndexer      codeindex.Indexer
		defaultProjectID string
		dbPing           httpserver.HealthCheck

		surrealDocChunks    document.ChunkStore
		surrealLedgerChunks ledger.ChunkStore
		surrealDocsTyped    *document.SurrealStore
		surrealLedgerTyped  *ledger.SurrealStore

		oauthServer *auth.OAuthServer
	)
	if cfg.DBDSN != "" {
		dbCfg, err := db.ParseDSN(cfg.DBDSN)
		if err != nil {
			logger.Error().Str("error", err.Error()).Msg("db dsn parse failed")
			os.Exit(1)
		}
		conn, err := db.Connect(ctx, dbCfg)
		if err != nil {
			logger.Error().Str("error", err.Error()).Msg("db connect failed")
			os.Exit(1)
		}
		// MCP OAuth 2.1 Authorization Server — needs Surreal for client/
		// session/code/refresh-token persistence. Wired here inside the
		// db-available block so the !DBDSN path skips it entirely (the
		// fallback warning lives in the bearerValidator block below).
		oauthStore := auth.NewSurrealOAuthStore(conn, logger)
		// resolveSessionUser closes over the SessionStore + UserStore so
		// the OAuth-AS shortcircuit at /oauth/authorize can detect an
		// already-logged-in browser session and skip the mcp_session_id
		// bridge entirely. Returns "" when no cookie / invalid session,
		// causing the AS to fall through to the bridge dance.
		resolveSessionUser := func(r *http.Request) string {
			id := auth.ReadCookie(r)
			if id == "" {
				return ""
			}
			sess, err := sessions.Get(id)
			if err != nil {
				return ""
			}
			user, err := users.GetByID(sess.UserID)
			if err != nil {
				return ""
			}
			return user.ID
		}
		oauthServer = auth.NewOAuthServer(auth.OAuthServerConfig{
			JWTSecret:          cfg.JWTSecret,
			Issuer:             cfg.OAuthIssuer,
			AccessTokenTTL:     cfg.OAuthAccessTokenTTL,
			RefreshTokenTTL:    cfg.OAuthRefreshTokenTTL,
			CodeTTL:            cfg.OAuthCodeTTL,
			Store:              oauthStore,
			Logger:             logger,
			DevMode:            cfg.DevMode,
			ResolveSessionUser: resolveSessionUser,
		})
		authHandlers.OAuthServer = oauthServer

		surrealDocs := document.NewSurrealStore(conn)
		docStore = surrealDocs
		surrealDocsTyped = surrealDocs
		projStore = project.NewSurrealStore(conn)
		surrealLed := ledger.NewSurrealStore(conn)
		ledgerStore = surrealLed
		surrealLedgerTyped = surrealLed
		surrealDocChunks = document.NewSurrealChunkStore(conn)
		surrealLedgerChunks = ledger.NewSurrealChunkStore(conn)
		storyStore = story.NewSurrealStore(conn, ledgerStore)
		wsStore = workspace.NewSurrealStore(conn)
		contractStore = contract.NewSurrealStore(conn, docStore, storyStore)
		sessionStore = session.NewSurrealStore(conn)
		// story_0ab83f82: replace MemorySessionStore with the durable
		// Surreal-backed implementation when SATELLITES_DB_DSN is set so cookie
		// sessions survive Fly rolling restarts.
		sessions = auth.NewSurrealSessionStore(conn)
		authHandlers.Sessions = sessions
		// story_7512783a: replace MemoryUserStore with the durable
		// Surreal-backed implementation. OAuth-minted users now persist
		// across restarts, so cookies don't orphan after a deploy.
		users = auth.NewSurrealUserStore(conn)
		authHandlers.Users = users
		grantStore = rolegrant.NewSurrealStore(conn, docStore)
		taskStore = task.NewSurrealStore(conn)
		repoStore = repo.NewSurrealStore(conn)
		repoIndexer = codeindex.NewLocalIndexer(filepath.Join(os.TempDir(), "satellites-repos"))
		dbPing = func(hcCtx context.Context) error { return db.Ping(hcCtx, conn) }

		// Seed the system user's default workspace so bootstrap writes
		// (default project, seeded documents) land in a workspace from day
		// one. Idempotent — safe across reboots.
		systemWsID, err := workspace.EnsureDefault(ctx, wsStore, logger, project.DefaultOwnerUserID, time.Now().UTC())
		if err != nil {
			logger.Warn().Str("error", err.Error()).Msg("system workspace seed failed")
		}
		// Grant the synthetic "apikey" user admin access to the system
		// workspace so Bearer-API-key callers share the system scope. The
		// alternative — minting a per-API-key workspace — would require
		// per-key accounting that feature-order:4 can add later.
		if systemWsID != "" {
			if err := wsStore.AddMember(ctx, systemWsID, "apikey", workspace.RoleAdmin, "system", time.Now().UTC()); err != nil {
				logger.Warn().Str("error", err.Error()).Msg("apikey system membership seed failed")
			}
		}

		// Seed default project, then idempotently stamp any legacy
		// document rows that pre-date the project primitive.
		id, err := project.SeedDefault(ctx, projStore, logger, systemWsID)
		if err != nil {
			logger.Error().Str("error", err.Error()).Msg("default project seed failed")
			os.Exit(1)
		}
		defaultProjectID = id
		if n, err := surrealDocs.BackfillProjectID(ctx, defaultProjectID); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("document backfill failed")
		} else if n > 0 {
			logger.Info().Int("rows", n).Str("project_id", defaultProjectID).Msg("document project_id backfilled")
		}

		if n, err := surrealDocs.MigrateLegacyRows(ctx, time.Now().UTC()); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("document migrate legacy rows failed")
		} else if n > 0 {
			logger.Info().Int("rows", n).Msg("document legacy rows migrated to v4 schema")
		}

		if _, err := document.SeedIfEmpty(ctx, docStore, logger, systemWsID, defaultProjectID, cfg.DocsDir); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("document seed failed")
		}

		// Seed the system-scope orchestrator role + agent documents that
		// the SessionStart path uses to mint orchestrator grants. System
		// scope means workspace_id=systemWsID, project_id=nil. Idempotent:
		// skip when a document with the canonical name already exists.
		// Story_7d9c4b1b.
		if err := seedOrchestratorDocs(ctx, docStore, systemWsID, time.Now().UTC()); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("orchestrator docs seed failed")
		}

		// Seed the system-scope lifecycle agent documents that
		// story_b39b393f's strict-required follow-up will allocate to
		// each contract instance at workflow_claim time. Idempotent.
		// Story_488b8223.
		if err := seedLifecycleAgents(ctx, docStore, systemWsID, time.Now().UTC()); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("lifecycle agents seed failed")
		}

		// story_7bfd629c: load system-tier configuration from
		// ./config/seed/{agents,contracts,workflows}/*.md and
		// ./config/help/*.md. Markdown is the single source of truth;
		// this loader runs after the in-Go seeds so it can override
		// their content with the file-driven version. Failures log at
		// warn — the platform stays bootable on a malformed file.
		if summary, err := configseed.RunAll(ctx, docStore,
			configseed.ResolveSeedDir(), configseed.ResolveHelpDir(),
			systemWsID, "system", time.Now().UTC()); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("configseed run failed")
		} else {
			logger.Info().
				Int("loaded", summary.Loaded).
				Int("created", summary.Created).
				Int("updated", summary.Updated).
				Int("skipped", summary.Skipped).
				Int("errors", len(summary.Errors)).
				Msg("configseed run complete")
			for _, e := range summary.Errors {
				logger.Warn().Str("path", e.Path).Str("reason", e.Reason).Msg("configseed entry failed")
			}
		}

		// story_b1108d4a: migrate legacy skill→contract bindings into
		// agent.skill_refs so the new contract_next resolution path
		// surfaces skills via the allocated agent. Idempotent — skills
		// without a binding or already migrated are no-ops. Runs AFTER
		// seedLifecycleAgents so the matching agent docs exist.
		document.MigrateSkillContractBindings(ctx, docStore, logger, time.Now().UTC())

		// Backfill required_role on pre-existing contract documents.
		// Contracts without a required_role field in their structured
		// payload receive role_orchestrator so the process-order gate's
		// new grant check (story_85675c33) can hit a stable target.
		// Idempotent: contracts that already carry required_role are
		// untouched.
		if n, err := stampRequiredRoleOnContracts(ctx, docStore, time.Now().UTC()); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("contract required_role backfill failed")
		} else if n > 0 {
			logger.Info().Int("rows", n).Msg("contract required_role backfill stamped")
		}

		// Migrate pre-6.5 contract_instance rows off the legacy
		// claimed_by_session_id column (story_4608a82c): first stamp
		// claimed_via_grant_id by resolving each row's legacy session
		// through the session registry, then UNSET the column. The two
		// operations are idempotent — a clean DB short-circuits both.
		if surrealContracts, ok := contractStore.(*contract.SurrealStore); ok {
			sessionMap, err := buildSessionGrantLookup(ctx, sessionStore)
			if err != nil {
				logger.Warn().Str("error", err.Error()).Msg("contract grant backfill: session lookup failed")
			} else {
				stamped, missed, err := surrealContracts.BackfillClaimedViaGrant(ctx, sessionMap, time.Now().UTC())
				if err != nil {
					logger.Warn().Str("error", err.Error()).Msg("contract grant backfill failed")
				} else if stamped > 0 || missed > 0 {
					logger.Info().Int("stamped", stamped).Int("missed", missed).Msg("contract claimed_via_grant_id backfill complete")
				}
			}
			if err := surrealContracts.DropLegacySessionColumn(ctx); err != nil {
				logger.Warn().Str("error", err.Error()).Msg("contract drop legacy session column failed")
			}
		}

		if surrealLedger, ok := ledgerStore.(*ledger.SurrealStore); ok {
			if n, err := surrealLedger.MigrateLegacyRows(ctx, time.Now().UTC()); err != nil {
				logger.Warn().Str("error", err.Error()).Msg("ledger migrate legacy rows failed")
			} else if n > 0 {
				logger.Info().Int("rows", n).Msg("ledger legacy rows migrated to v4 schema")
			}
		}

		// Backfill workspace_id across primitives. Idempotent on every
		// boot — second invocation finds no rows with empty workspace_id.
		if _, err := workspace.BackfillPrimitives(ctx, wsStore, projStore, storyStore, ledgerStore, docStore, logger, time.Now().UTC()); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("workspace backfill failed")
		}

		// Wire user-creation → workspace.EnsureDefault. Each new user gets
		// a personal workspace on first login. Project creation is now an
		// explicit action — sty_c975ebeb removed the auto-seeded "Default"
		// project because it conflated multi-repo scopes with single-repo
		// stories. Users land on /projects with an empty-state panel until
		// they create a project (typically via project_create with a
		// git_remote, or via the portal).
		authHandlers.OnUserCreated = func(hookCtx context.Context, userID string) {
			now := time.Now().UTC()
			if _, err := workspace.EnsureDefault(hookCtx, wsStore, logger, userID, now); err != nil {
				logger.Warn().Str("user_id", userID).Str("error", err.Error()).Msg("default workspace seed for user failed")
			}
		}

		// One-shot migration: archive legacy per-user "Default" projects
		// that the old EnsureDefault hook minted on first login. Idempotent
		// — a second invocation finds none active. sty_c975ebeb.
		if _, err := project.ArchiveLegacyDefaults(ctx, projStore, logger, time.Now().UTC()); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("archive legacy defaults failed")
		}
	}

	portalHandlers, err := portal.New(cfg, logger, sessions, users, projStore, ledgerStore, storyStore, contractStore, taskStore, docStore, repoStore, repoIndexer, grantStore, wsStore, startedAt)
	if err != nil {
		logger.Error().Str("error", err.Error()).Msg("portal init failed")
		os.Exit(1)
	}
	if oauthServer != nil {
		// Defense-in-depth: when the AS shortcircuit at /oauth/authorize
		// doesn't fire (e.g. user not yet logged in) the browser bounces
		// through /?mcp_session=… and the portal landing must complete
		// the flow if the user is already authenticated.
		portalHandlers.SetOAuthServer(oauthServer)
	}

	// Websocket hub (slice 10.1) + workspace-aware AuthHub (slice 10.2) +
	// store-layer emit hooks (slice 10.3). One hub instance per process.
	sharedHub := hub.New()
	var authHub *hub.AuthHub
	var wsHandlers *wshandler.Handler
	if wsStore != nil && ledgerStore != nil {
		audit := &ledgerMismatchAudit{
			ledger:    ledgerStore,
			projectID: defaultProjectID,
			logger:    logger,
		}
		authHub = hub.NewAuthHub(sharedHub, wsStore, audit)
		wsHandlers = wshandler.New(wshandler.Deps{
			AuthHub:  authHub,
			Sessions: &sessionResolverAdapter{sessions: sessions, users: users},
			Logger:   logger,
		})

		// Attach the store-layer publisher so ledger / task / contract /
		// story mutations fan to the hub on every write.
		publisher := &hubPublisher{authHub: authHub}
		attachPublisher(ledgerStore, publisher)
		attachPublisher(taskStore, publisher)
		attachPublisher(contractStore, publisher)
		attachPublisher(storyStore, publisher)
	}

	bearerValidator := auth.NewBearerValidator(auth.BearerValidatorConfig{
		CacheTTL:  cfg.OAuthTokenCacheTTL,
		JWTSecret: []byte(cfg.JWTSecret),
	})

	if oauthServer == nil {
		logger.Warn().Msg("oauth: DB unavailable — MCP OAuth 2.1 endpoints disabled (clients must use SATELLITES_API_KEYS bearer or session cookie)")
	}
	tokenExchange := &auth.TokenExchange{
		Sessions:  sessions,
		Users:     users,
		Validator: bearerValidator,
	}
	registrars := []httpserver.RouteRegistrar{authHandlers, portalHandlers, tokenExchange}
	if wsHandlers != nil {
		registrars = append(registrars, wsHandlers)
	}
	if oauthServer != nil {
		registrars = append(registrars, &oauthRoutes{srv: oauthServer})
	}
	// story_c08856b2: /hooks/enforce HTTP handler resolves a tool
	// call's allow/deny via the active CI's allocated agent (or the
	// session-default-install row when no CI is claimed).
	if sessionStore != nil && ledgerStore != nil && contractStore != nil && docStore != nil {
		permHandler := &permhook.Handler{
			Resolver: &permhook.Resolver{
				Sessions:  sessionStore,
				Ledger:    ledgerStore,
				Contracts: contractStore,
				Docs:      docStore,
			},
			Logger: logger,
		}
		registrars = append(registrars, permHandler)
	}
	srv := httpserver.New(cfg, logger, startedAt, registrars...)
	if dbPing != nil {
		srv.SetHealthCheck(dbPing)
	}

	rev := buildReviewer(logger, cfg)

	mcp := mcpserver.New(cfg, logger, startedAt, mcpserver.Deps{
		DocStore:         docStore,
		DocsDir:          cfg.DocsDir,
		ProjectStore:     projStore,
		DefaultProjectID: defaultProjectID,
		LedgerStore:      ledgerStore,
		StoryStore:       storyStore,
		WorkspaceStore:   wsStore,
		ContractStore:    contractStore,
		SessionStore:     sessionStore,
		RoleGrantStore:   grantStore,
		TaskStore:        taskStore,
		RepoStore:        repoStore,
		Indexer:          repoIndexer,
		Reviewer:         rev,
	})
	// Sty_088f6d5c: install the portal_replicate action vocabulary
	// from the seeded replicate_vocabulary document. configseed has
	// already loaded config/seed/replicate_vocabulary/default.md by
	// this point. Failures fall back to the canonical-only vocabulary
	// so the tool stays callable with built-in action names.
	if err := mcp.LoadReplicateVocabularyFromDoc(ctx, "default"); err != nil {
		logger.Warn().Str("error", err.Error()).Msg("portal_replicate vocabulary load failed (canonical-only fallback)")
	}
	mcpAuth := mcpserver.AuthMiddleware(mcpserver.AuthDeps{
		Sessions:       sessions,
		Users:          users,
		APIKeys:        cfg.APIKeys,
		Logger:         logger,
		OAuthValidator: bearerValidator,
	})
	srv.Mount("/mcp", mcpAuth(mcp))

	// Dispatch watchdog: scans for expired claims and reclaims them
	// into the queue. Story_b4513c8c. Runs only when the task store is
	// wired (SATELLITES_DB_DSN present).
	if taskStore != nil {
		disp := dispatcher.New(taskStore, ledgerStore, logger, dispatcher.Options{})
		if err := disp.Start(ctx); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("dispatcher watchdog start failed")
		} else {
			defer disp.Stop()
		}
	}

	// In-process repo reindex worker (story_c99995c8). Drains
	// reindex_repo tasks the MCP repo_add / repo_scan handlers enqueue,
	// runs HandleReindex inline, and closes the task. Lives in the
	// satellites binary so the repo collection pipeline is self-contained
	// — operators don't need a separate worker process for the repo
	// reference primitive to populate SurrealDB.
	if taskStore != nil && repoStore != nil && repoIndexer != nil {
		repoWorker := repo.NewWorker(repoStore, taskStore, ledgerStore, repoIndexer, nil, logger, repo.WorkerOptions{})
		if err := repoWorker.Start(ctx); err != nil {
			logger.Warn().Str("error", err.Error()).Msg("repo reindex worker start failed")
		} else {
			defer repoWorker.Stop()
		}
	}

	// Embedding ingestion worker (story_5abfe61c). Boots only when an
	// embeddings provider is configured AND SATELLITES_DB_DSN is set so the Surreal
	// chunk stores are available. SATELLITES_EMBEDDINGS_PROVIDER=none / unset → the
	// worker is not started; document_search and ledger_search fall
	// back to filter-only Search via the verb-layer ErrSemanticUnavailable
	// path.
	embedCfg := embeddings.FromConfig(cfg)
	if taskStore != nil && surrealDocChunks != nil && surrealLedgerChunks != nil {
		embedder, err := embeddings.New(embedCfg)
		if err != nil {
			logger.Warn().Str("error", err.Error()).Msg("embeddings provider construction failed; semantic search disabled")
		} else if embedder != nil {
			// Wire the chunk stores into the typed Surreal stores so
			// SearchSemantic delegates instead of returning
			// ErrSemanticUnavailable.
			if surrealDocsTyped != nil {
				surrealDocsTyped.WithEmbeddings(embedder, surrealDocChunks)
			}
			if surrealLedgerTyped != nil {
				surrealLedgerTyped.WithEmbeddings(embedder, surrealLedgerChunks)
			}

			worker := embedworker.New(embedworker.Deps{
				Tasks:        taskStore,
				Embedder:     embedder,
				Docs:         docStore,
				DocChunks:    surrealDocChunks,
				Ledger:       ledgerStore,
				LedgerChunks: surrealLedgerChunks,
				Logger:       logger,
			})
			if worker != nil {
				if err := worker.Start(ctx); err != nil {
					logger.Warn().Str("error", err.Error()).Msg("embedworker start failed")
				} else {
					defer worker.Stop()
					logger.Info().Str("provider", embedCfg.Provider).Str("model", embedder.Model()).Msg("embedding worker started")
				}
			}
		}
	}

	if err := srv.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error().Str("error", err.Error()).Msg("server terminated with error")
		os.Exit(1)
	}
	logger.Info().Msg("server stopped cleanly")
}

// seedOrchestratorDocs creates the system-scope `role_orchestrator` and
// `agent_claude_orchestrator` documents that the SessionStart path uses
// to mint orchestrator grants. Idempotent: existing rows are left
// untouched. Called from main during boot; a nil docStore short-circuits
// so early-boot tests that don't configure Surreal stay green.
// Story_7d9c4b1b.
func seedOrchestratorDocs(ctx context.Context, docStore document.Store, workspaceID string, now time.Time) error {
	if docStore == nil {
		return nil
	}
	// Create the role first so we can reference its id from the agent's
	// permitted_roles payload.
	role, err := docStore.GetByName(ctx, "", "role_orchestrator", nil)
	if err != nil {
		role, err = docStore.Create(ctx, document.Document{
			WorkspaceID: workspaceID,
			ProjectID:   nil,
			Type:        document.TypeRole,
			Name:        "role_orchestrator",
			Scope:       document.ScopeSystem,
			Status:      document.StatusActive,
			Body:        "Orchestrator role — the interactive Claude session's authorisation bundle. Holds every orchestrator-surface MCP verb (document_*, story_*, ledger_*, project_*, repo_*, contract_*, task_*, session_whoami, agent_role_*). Required hooks: SessionStart, PreToolUse, enforce. Seeded by platform bootstrap per pr_contract_separation.",
			Structured:  []byte(`{"allowed_mcp_verbs":["document_*","story_*","ledger_*","project_*","repo_*","workspace_*","principle_*","contract_*","skill_*","reviewer_*","agent_*","role_*","session_whoami","satellites_info"],"required_hooks":["SessionStart","PreToolUse","enforce"],"claim_requirements":[],"default_context_policy":"fresh-per-claim"}`),
			Tags:        []string{"v4", "agents-roles", "seed"},
			CreatedBy:   "system",
		}, now)
		if err != nil {
			return fmt.Errorf("seed role_orchestrator: %w", err)
		}
	}
	if _, err := docStore.GetByName(ctx, "", "agent_claude_orchestrator", nil); err == nil {
		return nil
	}
	structured := `{"provider_chain":[{"provider":"claude","model":"opus-4","tier":"opus"}],"tier":"opus","permitted_roles":["` + role.ID + `"],"tool_ceiling":["*"]}`
	if _, err := docStore.Create(ctx, document.Document{
		WorkspaceID: workspaceID,
		ProjectID:   nil,
		Type:        document.TypeAgent,
		Name:        "agent_claude_orchestrator",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Body:        "Claude orchestrator agent — the interactive session's delivery-agent configuration. provider_chain=claude/opus, tier=opus, tool_ceiling=['*']. permitted_roles pins role_orchestrator so the SessionStart grant path resolves. Seeded by platform bootstrap.",
		Structured:  []byte(structured),
		Tags:        []string{"v4", "agents-roles", "seed"},
		CreatedBy:   "system",
	}, now); err != nil {
		return fmt.Errorf("seed agent_claude_orchestrator: %w", err)
	}
	return nil
}

// lifecycleAgentSpec captures one seed agent — its document name and
// the permission_patterns the agent grants when allocated to a CI.
// Story_488b8223.
type lifecycleAgentSpec struct {
	Name     string
	Body     string
	Patterns []string
}

// seedLifecycleAgents creates one system-scope `type=agent` document
// per lifecycle role: **developer_agent** (preplan + plan + develop),
// **releaser_agent** (push + merge_to_main), and **story_close_agent**
// (story_close). Story_87b46d01 (S8 of
// `epic:orchestrator-driven-configuration`) collapsed the original
// 1-1 contract-shadow agents into these role-shaped agents per design
// `docs/architecture-orchestrator-driven-configuration.md` §4. Each
// agent's `permission_patterns` is the union of the patterns the
// folded contracts needed, so action_claim resolution stays a simple
// lookup against the agent doc's structured payload (story_cc55e093).
// Idempotent — agents already present by name are skipped.
// story_488b8223.
func seedLifecycleAgents(ctx context.Context, docStore document.Store, workspaceID string, now time.Time) error {
	if docStore == nil {
		return nil
	}
	specs := []lifecycleAgentSpec{
		{
			Name: "developer_agent",
			Body: "Role-shaped agent (story_87b46d01) covering preplan + plan + develop. Reads code/git/ledger, authors plan + review-criteria artefacts, edits + tests + commits. Bumps .version exactly once per story.",
			Patterns: []string{
				"Read:**", "Edit:**", "Write:**", "MultiEdit:**", "Grep:**", "Glob:**",
				"Bash:git_status", "Bash:git_log", "Bash:git_diff", "Bash:git_show",
				"Bash:git_add", "Bash:git_commit",
				"Bash:go_build", "Bash:go_test", "Bash:go_vet", "Bash:go_mod", "Bash:go_run",
				"Bash:gofmt", "Bash:goimports", "Bash:golangci_lint",
				"Bash:ls", "Bash:pwd", "Bash:cat", "Bash:echo", "Bash:mkdir",
				"mcp__satellites__satellites_*", "mcp__jcodemunch__*",
			},
		},
		{
			Name: "releaser_agent",
			Body: "Role-shaped agent (story_87b46d01) covering push + merge_to_main. Pushes the develop commit upstream; fast-forward merges to local main. Never re-bumps .version. No force operations.",
			Patterns: []string{
				"Read:**",
				"Bash:git_status", "Bash:git_log", "Bash:git_diff",
				"Bash:git_fetch", "Bash:git_push",
				"Bash:git_checkout", "Bash:git_branch", "Bash:git_merge",
				"Bash:ls", "Bash:pwd",
				"mcp__satellites__satellites_*",
			},
		},
		{
			Name: "story_close_agent",
			Body: "Lifecycle story_close agent — reads evidence + calls satellites_story_close to transition the story. Read-only across the codebase plus MCP write to the close verb.",
			Patterns: []string{
				"Read:**",
				"mcp__satellites__satellites_*",
			},
		},
	}
	for _, spec := range specs {
		if _, err := docStore.GetByName(ctx, "", spec.Name, nil); err == nil {
			continue
		}
		structured, err := document.MarshalAgentSettings(document.AgentSettings{
			PermissionPatterns: spec.Patterns,
		})
		if err != nil {
			return fmt.Errorf("seed %s: marshal: %w", spec.Name, err)
		}
		if _, err := docStore.Create(ctx, document.Document{
			WorkspaceID: workspaceID,
			ProjectID:   nil,
			Type:        document.TypeAgent,
			Name:        spec.Name,
			Scope:       document.ScopeSystem,
			Status:      document.StatusActive,
			Body:        spec.Body,
			Structured:  structured,
			Tags:        []string{"v4", "agents-roles", "seed", "lifecycle"},
			CreatedBy:   "system",
		}, now); err != nil {
			return fmt.Errorf("seed %s: %w", spec.Name, err)
		}
	}
	return nil
}

// stampRequiredRoleOnContracts scans every active type=contract row and,
// when the row's structured payload lacks a required_role field, writes
// an updated payload with required_role=role_orchestrator. Returns the
// number of rows stamped. Idempotent — a second call finds no rows
// without required_role. Story_85675c33.
func stampRequiredRoleOnContracts(ctx context.Context, docStore document.Store, now time.Time) (int, error) {
	if docStore == nil {
		return 0, nil
	}
	rows, err := docStore.List(ctx, document.ListOptions{Type: document.TypeContract}, nil)
	if err != nil {
		return 0, fmt.Errorf("list contracts: %w", err)
	}
	stamped := 0
	for _, row := range rows {
		updated, ok := addRequiredRoleIfMissing(row.Structured, "role_orchestrator")
		if !ok {
			continue
		}
		structuredVal := updated
		if _, err := docStore.Update(ctx, row.ID, document.UpdateFields{Structured: &structuredVal}, "system", now, nil); err != nil {
			return stamped, fmt.Errorf("update contract %s: %w", row.ID, err)
		}
		stamped++
	}
	return stamped, nil
}

// buildSessionGrantLookup walks every registered session and returns a
// map session_id → orchestrator_grant_id for the subset carrying a
// grant. Used by the boot-time contract_instance grant backfill in
// story_4608a82c. Empty map on nil store — the caller tolerates that.
func buildSessionGrantLookup(ctx context.Context, sessions session.Store) (map[string]string, error) {
	out := make(map[string]string)
	if sessions == nil {
		return out, nil
	}
	rows, err := sessions.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.OrchestratorGrantID == "" {
			continue
		}
		out[row.SessionID] = row.OrchestratorGrantID
	}
	return out, nil
}

// addRequiredRoleIfMissing inserts `required_role=roleName` into the
// JSON payload if the key is absent. Returns (newPayload, true) when a
// mutation is needed; (nil, false) when the key already exists or the
// payload is not a JSON object. Malformed JSON (non-object, non-empty)
// is left untouched — the caller logs via the return value.
func addRequiredRoleIfMissing(raw []byte, roleName string) ([]byte, bool) {
	if len(raw) == 0 {
		// Synthesize a minimal payload so the claim gate can resolve it.
		out, _ := json.Marshal(map[string]any{"required_role": roleName})
		return out, true
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	if _, exists := obj["required_role"]; exists {
		return nil, false
	}
	obj["required_role"] = roleName
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

// buildReviewer wires the production Reviewer for the MCP server.
// When cfg.GeminiAPIKey is set, returns a Gemini-backed reviewer using
// cfg.GeminiReviewModel (default gemini-2.5-flash). When unset, returns
// AcceptAll with a warning log so test/dev boots stay green but the
// operator knows the review path is a stub.
//
// Story_b218cb81 migrated the credentials from os.Getenv reads to the
// shared *config.Config so production resolution stays env→TOML→default
// and tests can carry the values via mounted TOML.
func buildReviewer(logger arbor.ILogger, cfg *config.Config) reviewer.Reviewer {
	if cfg.GeminiAPIKey == "" {
		logger.Warn().Msg("SATELLITES_GEMINI_API_KEY not set — reviewer falls back to AcceptAll (validation_mode=llm closes auto-accepted)")
		return reviewer.AcceptAll{}
	}
	model := cfg.GeminiReviewModel
	if model == "" {
		model = reviewer.DefaultGeminiReviewModel
	}
	logger.Info().Str("model", model).Msg("gemini reviewer wired")
	return reviewer.NewGeminiReviewer(reviewer.GeminiConfig{
		APIKey: cfg.GeminiAPIKey,
		Model:  model,
	})
}

// oauthRoutes adapts auth.OAuthServer to the httpserver.RouteRegistrar
// interface. The five route bindings are the surface the MCP-spec OAuth
// 2.1 chain (RFC 9728 / 8414 / 7591 + PKCE) requires from any compliant
// authorization server.
type oauthRoutes struct{ srv *auth.OAuthServer }

func (o *oauthRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", o.srv.HandleAuthorizationServer)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", o.srv.HandleProtectedResource)
	mux.HandleFunc("POST /oauth/register", o.srv.HandleRegister)
	mux.HandleFunc("/oauth/authorize", o.srv.HandleAuthorize)
	mux.HandleFunc("POST /oauth/token", o.srv.HandleToken)
}
