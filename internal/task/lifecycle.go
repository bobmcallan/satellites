package task

import (
	"encoding/json"
	"sync"
)

// Lifecycle is the runtime form of a task lifecycle document loaded
// from config/seed/lifecycles/*.md by the configseed loader. It
// declares the legal status set, the transition matrix, and the
// subscriber-visibility rules. The runtime resolves these at boot via
// RegisterLifecycle so the substrate's behaviour follows the seed —
// adding a new status is a seed edit, not a code change. sty_c1200f75.
type Lifecycle struct {
	Statuses                  []string
	Transitions               map[string][]string
	DefaultStatusOnCreate     string
	SubscriberVisibleStatuses []string
}

// AllowTransition reports whether moving from → to is permitted by
// the lifecycle's transition matrix. Returns false for any from/to
// pair the matrix doesn't enumerate.
func (lc *Lifecycle) AllowTransition(from, to string) bool {
	if lc == nil {
		return false
	}
	allowed, ok := lc.Transitions[from]
	if !ok {
		return false
	}
	for _, t := range allowed {
		if t == to {
			return true
		}
	}
	return false
}

// SubscriberVisible returns the set of statuses subscribers may see.
func (lc *Lifecycle) SubscriberVisible() map[string]struct{} {
	if lc == nil {
		return nil
	}
	out := make(map[string]struct{}, len(lc.SubscriberVisibleStatuses))
	for _, s := range lc.SubscriberVisibleStatuses {
		out[s] = struct{}{}
	}
	return out
}

// LifecycleFromStructured decodes the JSON structured payload of a
// type=lifecycle document into a Lifecycle. The payload shape matches
// what configseed.lifecycleToInput writes.
func LifecycleFromStructured(payload []byte) (*Lifecycle, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var raw struct {
		Statuses                  []string            `json:"statuses"`
		Transitions               map[string][]string `json:"transitions"`
		DefaultStatusOnCreate     string              `json:"default_status_on_create"`
		SubscriberVisibleStatuses []string            `json:"subscriber_visible_statuses"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	return &Lifecycle{
		Statuses:                  raw.Statuses,
		Transitions:               raw.Transitions,
		DefaultStatusOnCreate:     raw.DefaultStatusOnCreate,
		SubscriberVisibleStatuses: raw.SubscriberVisibleStatuses,
	}, nil
}

var (
	lifecycleMu sync.RWMutex
	lifecycle   *Lifecycle
)

// RegisterLifecycle installs the runtime lifecycle. main.go's boot
// path calls this after configseed loads the task lifecycle document.
// Passing nil clears the registration; subsequent ValidTransition
// calls fall back to the built-in default matrix.
func RegisterLifecycle(lc *Lifecycle) {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	lifecycle = lc
}

func registeredLifecycle() *Lifecycle {
	lifecycleMu.RLock()
	defer lifecycleMu.RUnlock()
	return lifecycle
}
