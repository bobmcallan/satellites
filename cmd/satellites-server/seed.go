package main

import (
	"strings"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/frontmatter"
)

// seedPlan is the pure decision for one embedded config/ file: seed it, skip it
// as client-homed, or SKIP it as malformed. Separating this decision from the DB
// reconcile (and from os-exit) makes the boot-seed's skip behaviour unit-testable
// — a malformed file must be skipped, never fatal (sty_adad79f8).
type seedPlan struct {
	// Skip is true when the file is malformed or mis-scoped — it is logged and
	// skipped at boot, never fatal. Reason carries the human explanation.
	Skip   bool
	Reason string
	// ClientHomed is true for a well-formed file that the server intentionally
	// does not seed (authored process substrate resolved client-side).
	ClientHomed bool
	// The resolved seed (valid when !Skip && !ClientHomed).
	Name     string
	Type     string
	Body     string
	Tags     []string
	Headline string
}

// planSeedFile resolves one embedded file into a seedPlan. It NEVER exits the
// process: a frontmatter that does not parse, or an unsupported scope, yields
// Skip=true with a reason so the caller can log-and-continue. This is the guard
// that keeps a single bad embed file from crash-looping the service.
func planSeedFile(filename string, raw []byte, serverHomed func(docType, name string, tags []string) bool) seedPlan {
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		return seedPlan{Skip: true, Reason: "frontmatter does not parse: " + err.Error()}
	}
	name := fm.Name
	if name == "" {
		name = strings.TrimSuffix(filename, ".md")
	}
	scope := fm.Scope
	if scope == "" {
		scope = "system"
	}
	if scope != "system" {
		return seedPlan{Skip: true, Reason: "config embed only supports scope=system, got " + scope}
	}
	docType := fm.Type
	if docType == "" {
		docType = document.TypeDocument
	}
	if !serverHomed(docType, name, fm.Tags) {
		// Well-formed, but client-homed authored substrate — not a skip-as-error.
		return seedPlan{ClientHomed: true, Name: name}
	}
	// Skills preserve their frontmatter in the stored body so the document mirror
	// is byte-identical to the SKILL.md a client materialises into
	// .claude/skills/<name>/SKILL.md. Documents strip frontmatter.
	storedBody := string(body)
	if docType == document.TypeSkill {
		storedBody = string(raw)
	}
	return seedPlan{Name: name, Type: docType, Body: storedBody, Tags: fm.Tags, Headline: fm.Headline}
}
