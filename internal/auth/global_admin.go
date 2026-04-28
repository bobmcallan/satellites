package auth

import (
	"os"
	"strings"
)

// GlobalAdminEmailsEnv is the env var name carrying the comma-separated
// list of email addresses that resolve as global_admin without needing
// a persisted user-record flag. story_3548cde2.
const GlobalAdminEmailsEnv = "SATELLITES_GLOBAL_ADMIN_EMAILS"

// LoadGlobalAdminEmails reads SATELLITES_GLOBAL_ADMIN_EMAILS and returns
// the lowercased trimmed entries as a set for cheap membership checks.
// Empty / unset env var returns an empty set.
func LoadGlobalAdminEmails() map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(GlobalAdminEmailsEnv))
	out := map[string]struct{}{}
	if raw == "" {
		return out
	}
	for _, part := range strings.Split(raw, ",") {
		email := strings.ToLower(strings.TrimSpace(part))
		if email == "" {
			continue
		}
		out[email] = struct{}{}
	}
	return out
}

// IsGlobalAdmin reports whether the user is a platform-tier admin.
// Returns true when the user record carries the GlobalAdmin flag OR the
// user's email (case-insensitive) is in the env-driven set.
func IsGlobalAdmin(user User, emails map[string]struct{}) bool {
	if user.GlobalAdmin {
		return true
	}
	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" {
		return false
	}
	_, ok := emails[email]
	return ok
}
