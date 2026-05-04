package portal

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

func renderPath(t *testing.T, p *Portal, path, sessionCookie string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	p.Register(mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if sessionCookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sessionCookie})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
