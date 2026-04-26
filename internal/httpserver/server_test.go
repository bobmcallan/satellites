package httpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
)

func TestHealthzShape(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Port: 0, Env: "dev", LogLevel: "info", DevMode: true}
	s := New(cfg, satarbor.New("info"), time.Now().Add(-2*time.Second))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	for _, k := range []string{"version", "build", "commit", "started_at", "uptime_seconds"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing key %q in healthz payload", k)
		}
	}
	if uptime, ok := body["uptime_seconds"].(float64); !ok || uptime < 1 {
		t.Errorf("uptime_seconds = %v, want >=1", body["uptime_seconds"])
	}
}

func TestRequestIDMiddlewareInjects(t *testing.T) {
	t.Parallel()
	var seen string
	h := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = satarbor.RequestIDFrom(r.Context())
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if seen == "" {
		t.Fatal("expected request id to be injected into context")
	}
	if echoed := rec.Header().Get("X-Request-ID"); echoed != seen {
		t.Errorf("header echo = %q, context = %q", echoed, seen)
	}
}

func TestRequestIDMiddlewarePreservesInbound(t *testing.T) {
	t.Parallel()
	var seen string
	h := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = satarbor.RequestIDFrom(r.Context())
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "req_supplied")
	h.ServeHTTP(rec, req)
	if seen != "req_supplied" {
		t.Errorf("seen = %q, want req_supplied", seen)
	}
}

// TestAccessLogPreservesHijacker is the regression for story_fb6ac2d8
// (WS indicator orange→red on /). The accessLog middleware wraps the
// ResponseWriter in *statusRecorder; before the fix the wrapper shadowed
// http.Hijacker, which caused gorilla/websocket's Upgrade to reject the
// connection with a 500 ("response does not implement http.Hijacker") and
// left the nav indicator stuck in reconnecting → disconnected.
func TestAccessLogPreservesHijacker(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	})
	mux := http.NewServeMux()
	mux.Handle("/ws", wsHandler)
	wrapped := requestID(accessLog(satarbor.New("info"), mux))

	srv := httptest.NewServer(wrapped)
	defer srv.Close()

	wsURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	wsURL.Scheme = "ws"
	wsURL.Path = "/ws"

	dialer := websocket.Dialer{
		NetDialContext:   (&net.Dialer{}).DialContext,
		HandshakeTimeout: 2 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, resp, err := dialer.DialContext(ctx, wsURL.String(), nil)
	if err != nil {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("websocket dial through accessLog middleware failed: status=%d err=%v", got, err)
	}
	defer conn.Close()
}
