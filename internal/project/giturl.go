package project

import (
	"errors"
	"strings"
)

// ErrInvalidGitRemote signals that an input string does not parse to a
// recognisable git remote. Callers map this to a structured
// "git_url_invalid" error at the verb layer.
var ErrInvalidGitRemote = errors.New("project: invalid git remote URL")

// CanonicaliseGitRemote normalises a git remote URL so the same repo
// reaches the same row regardless of how the caller wrote it. The
// canonical form is `https://<host-lowercased>/<owner>/<repo>` — no
// trailing `.git`, no trailing slash.
//
// Supported inputs (round-trip to the same canonical):
//   - SSH:        `git@github.com:owner/repo.git`
//   - SSH:        `ssh://git@github.com/owner/repo.git`
//   - HTTPS:      `https://github.com/owner/repo.git/`
//   - HTTPS:      `HTTPS://GitHub.com/owner/repo`
//   - Git:        `git://github.com/owner/repo.git`
//   - Scoped:     `<scope>:github.com/owner/repo`   (e.g. `bobmcallan:github.com/owner/repo`)
//   - Bare:       `github.com/owner/repo`
//
// Empty input returns ("", nil) — callers decide whether empty is
// allowed (Create accepts it; project_update rejects it on explicit
// non-empty inputs that fail to parse).
func CanonicaliseGitRemote(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", nil
	}

	if strings.HasPrefix(s, "git@") && !strings.Contains(s, "://") {
		colon := strings.Index(s, ":")
		if colon <= len("git@") {
			return "", ErrInvalidGitRemote
		}
		host := s[len("git@"):colon]
		path := s[colon+1:]
		s = "https://" + host + "/" + path
	} else {
		switch {
		case strings.HasPrefix(s, "ssh://git@"):
			s = "https://" + s[len("ssh://git@"):]
		case strings.HasPrefix(s, "ssh://"):
			s = "https://" + s[len("ssh://"):]
		case strings.HasPrefix(s, "git://"):
			s = "https://" + s[len("git://"):]
		case strings.HasPrefix(strings.ToLower(s), "https://"):
			s = "https://" + s[len("https://"):]
		case strings.HasPrefix(strings.ToLower(s), "http://"):
			s = "https://" + s[len("http://"):]
		default:
			// Scoped (`<scope>:<host>/<owner>/<repo>`) and bare
			// (`<host>/<owner>/<repo>`) inputs land here. The scope
			// segment (if present) carries no semantic information —
			// it's an upstream artifact (org prefix, user namespace,
			// editor decoration) we strip to recover the host/path.
			if colon := strings.Index(s, ":"); colon > 0 && !strings.Contains(s[:colon], "/") {
				s = s[colon+1:]
			}
			s = "https://" + s
		}
	}

	rest := s[len("https://"):]
	slash := strings.Index(rest, "/")
	if slash <= 0 {
		return "", ErrInvalidGitRemote
	}
	host := strings.ToLower(rest[:slash])
	path := rest[slash+1:]

	path = strings.TrimRight(path, "/")
	if strings.HasSuffix(path, ".git") {
		path = path[:len(path)-len(".git")]
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "", ErrInvalidGitRemote
	}

	return "https://" + host + "/" + path, nil
}
