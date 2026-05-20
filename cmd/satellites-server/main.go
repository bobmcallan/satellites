package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/db"
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/verb"
	_ "github.com/lib/pq"
)

func main() {
	addr := flag.String("addr",
		envOr("SATELLITES_LISTEN_ADDR", ":8080"),
		"HTTP listen address")
	dsn := flag.String("dsn",
		envOr("DATABASE_URL", "postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable"),
		"Postgres DSN")
	devMode := flag.Bool("dev", false,
		"Dev mode: seed admin/user accounts with well-known api keys (NEVER in production)")
	flag.Parse()

	if err := db.MigrateUp(*dsn); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}

	sqlDB, err := sql.Open("postgres", *dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db open:", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	store := auth.New(sqlDB)
	verb.SetAuthStore(store) // satellites_init verb mints api-keys via this store

	if *devMode {
		if err := store.DevSeed(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "dev seed:", err)
			os.Exit(1)
		}
		log.Printf("dev mode: seeded admin (%s) and user (%s); api keys = %s / %s",
			auth.DevAdminEmail, auth.DevUserEmail, auth.DevAdminKey, auth.DevUserKey)
	}

	oauthCfg := auth.OAuthConfig{
		GitHubClientID:     os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
	}

	s := mcpserver.New()
	mcpHandler := mcpserver.HTTPHandler(s)

	mux := http.NewServeMux()
	auth.RegisterRoutes(mux, oauthCfg)
	mux.Handle("/mcp", store.Middleware(mcpHandler))
	mux.Handle("/mcp/", store.Middleware(mcpHandler))

	log.Printf("satellites-server listening on %s (dev=%v)", *addr, *devMode)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func envOr(key, defaultV string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultV
}
