package auth

import "strings"

// DisplayName resolves a human-readable label for a user via a deterministic
// fallback chain (sty_fcf5477e): the stored profile name, then the email
// local-part (the segment before "@"), then the raw userID when nothing else
// exists. u may be nil (lookup miss) — the userID is the floor so the result is
// never empty. The email is used only to derive the local-part; it is never
// returned whole, so callers can render the result without leaking the address.
func DisplayName(u *User, userID string) string {
	if u != nil {
		if name := strings.TrimSpace(u.DisplayName); name != "" {
			return name
		}
		if email := strings.TrimSpace(u.Email); email != "" {
			if local := strings.SplitN(email, "@", 2)[0]; local != "" {
				return local
			}
		}
	}
	return userID
}
