package httpserver

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/ternarybob/arbor"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
)

// requestID injects a UUID v4 into the request context when the inbound
// request does not carry an X-Request-ID header. The value is also echoed on
// the response so clients can correlate logs.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := satarbor.WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// accessLog wraps next and emits one arbor Info line per request on complete,
// carrying method, path, status, duration_ms, and request_id.
func accessLog(logger arbor.ILogger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", sw.status).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Str("request_id", satarbor.RequestIDFrom(r.Context())).
			Msg("http access")
	})
}

// statusRecorder captures the response status for access logging. net/http's
// ResponseWriter doesn't expose it directly.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Hijack delegates to the underlying ResponseWriter when it implements
// http.Hijacker so middleware composition does not break protocol-upgrade
// paths (e.g. the gorilla/websocket /ws upgrade). Without this passthrough
// gorilla/websocket fails the upgrade with "response does not implement
// http.Hijacker" and the client receives a 500, leaving the nav indicator
// stuck in reconnecting → disconnected.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpserver: underlying ResponseWriter does not implement http.Hijacker")
	}
	return hj.Hijack()
}
