package server

import (
	"net/url"
	"strings"
)

// safeNext validates a post-login `next` return target. It returns raw only
// when raw is a LOCAL same-site path — it must start with a single "/" (not a
// protocol-relative "//host"), carry no scheme or host, and contain no
// backslash (browsers fold "\" into "/", so "/\evil.com" must be rejected).
// Anything else — empty, absolute, off-site, or malformed — falls back to "/".
// This is the single open-redirect guard every login entry point runs.
func safeNext(raw string) string {
	const fallback = "/"
	if raw == "" {
		return fallback
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	if strings.ContainsAny(raw, "\\") {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	return raw
}
