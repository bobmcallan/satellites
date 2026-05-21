package main

import (
	"context"
	"database/sql"
	"flag"
	"net/http"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/config"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/verb"
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

	handler := server.Build(server.Config{
		Store:    store,
		Sessions: sessions,
		DevMode:  cfg.Dev,
		OAuth: auth.OAuthConfig{
			GitHubClientID:     cfg.OAuth.GitHubClientID,
			GitHubClientSecret: cfg.OAuth.GitHubClientSecret,
		},
	})

	arbor.Info("satellites-server listening", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		arbor.Fatal("http listen", "err", err)
	}
}
