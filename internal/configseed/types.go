// Package configseed loads system-tier configuration markdown from
// ./config/seed/ and upserts it into the document store at boot. The
// markdown is the single source of truth for system agents, contracts,
// workflows, and (sibling story_cc5c67a9) help pages — git tracks it,
// the loader keeps the DB in sync.
//
// story_7bfd629c.
package configseed

// Kind enumerates the subdirectories the loader recognises under
// SATELLITES_SEED_DIR. Each kind maps to a document type discriminator
// (agent / contract / workflow / help).
type Kind string

const (
	KindAgent               Kind = "agent"
	KindContract            Kind = "contract"
	KindWorkflow            Kind = "workflow"
	KindHelp                Kind = "help"
	KindPrinciple           Kind = "principle"
	KindStoryTemplate       Kind = "story_template"
	KindReplicateVocabulary Kind = "replicate_vocabulary"
)

// Summary captures the per-kind result counts a Run pass produces.
// Surfaced to the boot logs and to the system_seed_run MCP verb so
// operators can see what changed.
type Summary struct {
	Loaded  int          `json:"loaded"`
	Created int          `json:"created"`
	Updated int          `json:"updated"`
	Skipped int          `json:"skipped"`
	Errors  []ErrorEntry `json:"errors,omitempty"`
}

// ErrorEntry is a per-file error record. Path is relative to the seed
// dir; Reason is the human-readable cause.
type ErrorEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Add merges other into s. Used to combine per-subdirectory results
// into one boot summary.
func (s *Summary) Add(other Summary) {
	s.Loaded += other.Loaded
	s.Created += other.Created
	s.Updated += other.Updated
	s.Skipped += other.Skipped
	s.Errors = append(s.Errors, other.Errors...)
}
