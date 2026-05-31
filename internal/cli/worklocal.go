// Local working-state fast-path for the reviewer loop (sty_8e8ec0e7).
//
// `.satellites/work/<story-id>/` is a client-side coordination area, a
// sibling of logs/ (operational output) and worktree/ (code checkout). The
// server ledger stays the system of record and the authority boundary; these
// files are a cache + message bus the client owns, modelled on Claude Agent
// Teams (a shared on-disk area with claim → work → write-back, file-locked).
//
//   inbox/<seq>-<kind>.json  append-only, one message per file, monotonic seq
//   status.json              claim/lease state, atomic write under an flock
//   .lock                    the advisory lockfile status writes serialise on
//
// The inbox message shape mirrors the ledger Entry fields it is later flushed
// as (kind/body/payload/created_at) but is defined here, not imported from
// internal/ledger — the CLI layering guard forbids that import. It reuses the
// existing ledger `kind` discriminators; nothing new is invented.

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// workRoot is the local working-state area, under .satellites/ alongside
// logs/ and worktree/. Relative to CWD, like the satellites.toml walk root.
const workRoot = ".satellites/work"

// workMessage is one inbox row: the ledger fields it carries when flushed
// (kind/body/payload/created_at) plus the local seq. Defined locally to
// honour the no-internal/ledger import guard.
type workMessage struct {
	Seq       int             `json:"seq"`
	Kind      string          `json:"kind"`
	Body      string          `json:"body,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt string          `json:"created_at,omitempty"`
}

// workStatus is the claim/lease state for one story's local work area.
type workStatus struct {
	StoryID    string `json:"story_id"`
	Status     string `json:"status"`
	ClaimedBy  string `json:"claimed_by"`
	LeaseUntil string `json:"lease_until"` // RFC3339
	LastSeq    int    `json:"last_seq"`
}

func storyWorkDir(storyID string) string { return filepath.Join(workRoot, storyID) }
func inboxDir(storyID string) string     { return filepath.Join(storyWorkDir(storyID), "inbox") }

// atomicWrite writes data via a temp file + rename, so a concurrent reader
// never observes a half-written file.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has consumed it
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// seqOf parses the leading zero-padded <seq> of an inbox filename; 0 if none.
func seqOf(name string) int {
	i := strings.IndexByte(name, '-')
	if i <= 0 {
		return 0
	}
	n, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0
	}
	return n
}

// nextInboxSeq returns 1 + the highest seq present, or 1 for an empty/absent
// inbox. Seqs are the numeric filename prefix of <seq>-<kind>.json.
func nextInboxSeq(storyID string) (int, error) {
	entries, err := os.ReadDir(inboxDir(storyID))
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	max := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := seqOf(e.Name()); n > max {
			max = n
		}
	}
	return max + 1, nil
}

// safeKind sanitises a kind for a filename (log:info → log_info).
func safeKind(kind string) string {
	return strings.NewReplacer(":", "_", "/", "_", " ", "_").Replace(kind)
}

// inboxAppend writes one message to the inbox atomically and returns its seq.
func inboxAppend(storyID, kind, body string, payload json.RawMessage, now time.Time) (int, error) {
	seq, err := nextInboxSeq(storyID)
	if err != nil {
		return 0, err
	}
	msg := workMessage{Seq: seq, Kind: kind, Body: body, Payload: payload, CreatedAt: now.UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return 0, err
	}
	name := fmt.Sprintf("%06d-%s.json", seq, safeKind(kind))
	if err := atomicWrite(filepath.Join(inboxDir(storyID), name), data, 0o644); err != nil {
		return 0, err
	}
	return seq, nil
}

// inboxReadAll returns every inbox message, oldest-first by seq. A malformed
// file is skipped rather than failing the read.
func inboxReadAll(storyID string) ([]workMessage, error) {
	entries, err := os.ReadDir(inboxDir(storyID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var msgs []workMessage
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, rErr := os.ReadFile(filepath.Join(inboxDir(storyID), e.Name()))
		if rErr != nil {
			return nil, rErr
		}
		var m workMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Seq < msgs[j].Seq })
	return msgs, nil
}

// inboxReadRecent returns up to n most-recent inbox messages, oldest-first.
func inboxReadRecent(storyID string, n int) ([]workMessage, error) {
	msgs, err := inboxReadAll(storyID)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs, nil
}

// localRecentLedgerJSON renders the recent inbox tail as a ledger_list-shaped
// `{"entries":[…]}` document, so the caller can unmarshal it into the same
// response type ledger_list returns — serving recent context from local files
// with no wire round-trip (AC1). ok is false when the inbox is empty/absent,
// signalling the caller to fall back to ledger_list. The inbox message JSON
// mirrors the ledger Entry fields (kind/body/payload/created_at); the extra
// `seq` is ignored on the consumer's unmarshal.
func localRecentLedgerJSON(storyID string, n int) ([]byte, bool, error) {
	msgs, err := inboxReadRecent(storyID, n)
	if err != nil {
		return nil, false, err
	}
	if len(msgs) == 0 {
		return nil, false, nil
	}
	b, err := json.Marshal(map[string]any{"entries": msgs})
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// readWorkStatus reads status.json, or a zero value (no error) when absent.
func readWorkStatus(storyID string) (workStatus, error) {
	raw, err := os.ReadFile(filepath.Join(storyWorkDir(storyID), "status.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return workStatus{}, nil
		}
		return workStatus{}, err
	}
	var st workStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return workStatus{}, err
	}
	return st, nil
}

// writeWorkStatus persists status.json atomically.
func writeWorkStatus(storyID string, st workStatus) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(storyWorkDir(storyID), "status.json"), data, 0o644)
}

// withWorkLock runs fn while holding an exclusive advisory flock on the work
// area's lockfile, so a status read-modify-write is atomic against a
// concurrent reviewer. Advisory (cooperative): every claimant takes this path.
func withWorkLock(storyID string, fn func() error) error {
	dir := storyWorkDir(storyID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("work lock: %w", err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	return fn()
}

// errWorkClaimed is returned when the work area is held by a different
// reviewer under a still-live lease.
var errWorkClaimed = fmt.Errorf("work area already claimed by another reviewer")

// claimWork acquires the story's work area for claimedBy with a lease,
// atomically under the flock. It refuses (errWorkClaimed) only when an
// UNEXPIRED lease is held by a DIFFERENT claimant — preventing two reviewers
// double-claiming the same transition (AC5). An expired lease, or one already
// held by claimedBy (a re-run/crash-recovery by the same operator), is
// reclaimable. now is injected for testability.
func claimWork(storyID, claimedBy, status string, lease time.Duration, now time.Time) error {
	return withWorkLock(storyID, func() error {
		st, err := readWorkStatus(storyID)
		if err != nil {
			return err
		}
		if st.ClaimedBy != "" && st.ClaimedBy != claimedBy && st.LeaseUntil != "" {
			if until, pErr := time.Parse(time.RFC3339, st.LeaseUntil); pErr == nil && now.Before(until) {
				return fmt.Errorf("%w: held by %q until %s", errWorkClaimed, st.ClaimedBy, st.LeaseUntil)
			}
		}
		st.StoryID = storyID
		st.Status = status
		st.ClaimedBy = claimedBy
		st.LeaseUntil = now.Add(lease).UTC().Format(time.RFC3339)
		return writeWorkStatus(storyID, st)
	})
}

// cleanupWork removes the story's local work area after a terminal flush.
func cleanupWork(storyID string) error {
	return os.RemoveAll(storyWorkDir(storyID))
}

// workClaimant is a stable per-operator/host identity for the lease, so a
// re-run by the same operator reclaims its own lease while a different
// reviewer with a live lease is refused.
func workClaimant(userID string) string {
	host, _ := os.Hostname()
	u := strings.TrimSpace(userID)
	if u == "" {
		u = "local"
	}
	if host == "" {
		host = "host"
	}
	return u + "@" + host
}
