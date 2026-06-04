// Package measure is the satellites client's session-observability surface
// ("measure mode"): it captures what an agent does during a session into
// in-repo logs the client can later review for metrics and improvement
// suggestions.
//
// Scope note (M1, the in-repo data-path prerequisite): session capture lands
// IN THE CONSUMER REPO, under the directory resolved by
// cliconfig.Config.ResolveLogDir (default .satellites/logs), alongside
// .satellites/work/ — never in $HOME. The separate `satellites-client`
// daemon that writes task logs to ~/.satellites/daemon/logs is a DIFFERENT
// binary, not part of this module, and is explicitly OUT OF SCOPE here:
// measure mode does its own in-repo capture rather than depending on that
// background service.
package measure

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppendEvent appends one event as a single JSON line to
// <logDir>/<sessionID>.jsonl, creating logDir if it does not exist. The file
// is append-only with one line per event — the on-disk shape the client's
// session review reads back. logDir is expected to come from
// cliconfig.Config.ResolveLogDir so every caller writes to the same in-repo
// location (single source of truth for where session logs live).
func AppendEvent(logDir, sessionID string, event any) error {
	if strings.TrimSpace(logDir) == "" {
		return fmt.Errorf("measure: AppendEvent: empty logDir")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("measure: create log dir %s: %w", logDir, err)
	}

	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("measure: marshal event: %w", err)
	}

	path := filepath.Join(logDir, sessionFileName(sessionID))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("measure: open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("measure: write %s: %w", path, err)
	}
	return nil
}

// sessionFileName maps a session id to its append-only log file. An empty or
// path-bearing id is normalised so the result is always a single safe file
// name under logDir (a session id must never let a caller escape logDir).
func sessionFileName(sessionID string) string {
	s := strings.TrimSpace(sessionID)
	if s == "" {
		s = "session"
	}
	// collapse anything that could traverse or nest into a flat name
	s = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(s)
	return s + ".jsonl"
}
