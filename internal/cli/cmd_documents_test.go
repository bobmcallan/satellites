package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// testProjectID is the repo project_id the upload path now resolves from
// satellites.toml (sty_afc0769c). Tests pass it directly to planUpload /
// validateUpload in place of the retired path-encoded identity.
const testProjectID = "proj_test"

// writeSource creates rootDir/<relPath> with content, making parents.
func writeSource(t *testing.T, rootDir, relPath, content string) {
	t.Helper()
	p := filepath.Join(rootDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestPlanUpload_FlatProjectScope pins sty_afc0769c: the walker reads
// .satellites/<kind>/<name>.md (flat), every target is project-scoped with
// the repo project_id, and a frontmatter name override is honoured.
func TestPlanUpload_FlatProjectScope(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "documents/feature-rule.md",
		"---\nname: overridden-name\ntags: [principles:project]\n---\n# PJ body\n")
	writeSource(t, root, "documents/ledger-spine.md",
		"---\ntags: [area:substrate]\n---\n# spine\n")
	// Non-md files are ignored.
	writeSource(t, root, "documents/notes.txt", "ignore")
	// A skill in the same tree must not surface for the documents kind.
	writeSource(t, root, "skills/a-skill.md", "---\n---\n# skill body\n")

	targets, err := planUpload(root, "documents", testProjectID)
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}

	got := make([]string, 0, len(targets))
	for _, tg := range targets {
		got = append(got, uploadLabel(tg))
	}
	sort.Strings(got)
	want := []string{
		"project/proj_test/ledger-spine",
		"project/proj_test/overridden-name",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets mismatch:\n got  %v\n want %v", got, want)
	}

	for _, tg := range targets {
		if tg.ProjectID != testProjectID {
			t.Errorf("%s ProjectID = %q, want %q", tg.Name, tg.ProjectID, testProjectID)
		}
	}

	tagsBy := map[string][]string{}
	for _, tg := range targets {
		tagsBy[uploadLabel(tg)] = tg.Tags
	}
	if tags := tagsBy["project/proj_test/overridden-name"]; !reflect.DeepEqual(tags, []string{"principles:project"}) {
		t.Errorf("project tags = %v want [principles:project]", tags)
	}
}

func TestPlanUpload_MissingRoot(t *testing.T) {
	targets, err := planUpload(filepath.Join(t.TempDir(), "does-not-exist"), "documents", testProjectID)
	if err != nil {
		t.Fatalf("expected nil error for missing root, got %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("expected zero targets, got %d", len(targets))
	}
}

func TestPlanUpload_SkillKindFiltersAndType(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/my-skill.md", "---\n---\n# body\n")
	// A documents-kind file must be excluded when uploading the skills kind.
	writeSource(t, root, "documents/a-doc.md", "---\n---\n# body\n")

	targets, err := planUpload(root, "skills", testProjectID)
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %d", len(targets))
	}
	if targets[0].Type != "skill" {
		t.Errorf("Type = %q, want skill", targets[0].Type)
	}
	if targets[0].Name != "my-skill" {
		t.Errorf("Name = %q, want my-skill", targets[0].Name)
	}
}

func TestPlanUpload_FrontmatterTypeOverridesDefault(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/doc.md", "---\ntype: document\n---\n# body\n")
	targets, err := planUpload(root, "skills", testProjectID)
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}
	if targets[0].Type != "document" {
		t.Errorf("frontmatter type override ignored: %q", targets[0].Type)
	}
}

// TestPlanUpload_SkillPreservesFrontmatterDocumentStrips pins sty_4b517016:
// a type:skill upload must store the artifact with its authored frontmatter
// intact (so the substrate row is a registerable SKILL.md), while documents
// strip frontmatter from the stored body. Mirrors the server system-seed
// rule (storedBody = raw for TypeSkill).
func TestPlanUpload_SkillPreservesFrontmatterDocumentStrips(t *testing.T) {
	root := t.TempDir()
	skillSrc := "---\nname: my-skill\ndescription: does a thing\n---\n# Skill body\n"
	docSrc := "---\nname: my-doc\ntags: [principles:project]\n---\n# Doc body\n"
	writeSource(t, root, "skills/my-skill.md", skillSrc)
	writeSource(t, root, "documents/my-doc.md", docSrc)

	// Skill: body keeps the authored frontmatter (name + description).
	skills, err := planUpload(root, "skills", testProjectID)
	if err != nil {
		t.Fatalf("planUpload skills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected one skill target, got %d", len(skills))
	}
	sb := skills[0].Body
	if !strings.Contains(sb, "name: my-skill") || !strings.Contains(sb, "description: does a thing") {
		t.Errorf("skill body must retain authored frontmatter, got:\n%s", sb)
	}
	if !strings.HasPrefix(strings.TrimSpace(sb), "---") {
		t.Errorf("skill body must start with frontmatter delimiter, got:\n%s", sb)
	}
	if !strings.Contains(sb, "# Skill body") {
		t.Errorf("skill body lost its content, got:\n%s", sb)
	}

	// Document: frontmatter stripped from the stored body.
	docs, err := planUpload(root, "documents", testProjectID)
	if err != nil {
		t.Fatalf("planUpload documents: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected one document target, got %d", len(docs))
	}
	db := docs[0].Body
	if strings.Contains(db, "name: my-doc") || strings.Contains(db, "principles:project") {
		t.Errorf("document body must strip frontmatter, got:\n%s", db)
	}
	if !strings.Contains(db, "# Doc body") {
		t.Errorf("document body lost its content, got:\n%s", db)
	}
}

// TestPlanUpload_NestedFileSkipped pins sty_afc0769c AC3: the layout is flat,
// so a file nested below the kind dir (the retired <wksp>/<proj> shape, or any
// other subdir) is not dispatched.
func TestPlanUpload_NestedFileSkipped(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "documents/flat.md", "---\n---\n# ok\n")
	writeSource(t, root, "documents/wksp_one/proj_one/nested.md", "---\n---\n# nested\n")

	targets, err := planUpload(root, "documents", testProjectID)
	if err != nil {
		t.Fatalf("planUpload: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "flat" {
		t.Fatalf("expected only the flat file, got %d targets: %v", len(targets), targets)
	}
}

func TestMarshalUpsertRequest_ProjectScopeNoWorkspace(t *testing.T) {
	target := documentTarget{
		ProjectID: testProjectID,
		Name:      "my-skill",
		Type:      "skill",
		Body:      "# body",
		Tags:      []string{"kind:test"},
	}
	raw, err := marshalUpsertRequest(target)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"type":"skill"`) {
		t.Errorf("payload missing type:\"skill\": %s", s)
	}
	if !strings.Contains(s, `"scope":"project"`) {
		t.Errorf("payload missing scope:\"project\": %s", s)
	}
	if !strings.Contains(s, `"project_id":"proj_test"`) {
		t.Errorf("payload missing project_id: %s", s)
	}
	if strings.Contains(s, "workspace_id") {
		t.Errorf("payload must NOT carry workspace_id (server derives it): %s", s)
	}
}

// flagged reports whether vs contains a violation whose path ends in
// pathSuffix and whose rule equals want. Matching by suffix keeps the
// assertions independent of the temp-dir absolute root.
func flagged(vs []violation, pathSuffix, want string) bool {
	for _, v := range vs {
		if v.Rule == want && strings.HasSuffix(filepath.ToSlash(v.Path), pathSuffix) {
			return true
		}
	}
	return false
}

// countRule counts violations whose path ends in pathSuffix and whose rule
// equals want.
func countRule(vs []violation, pathSuffix, want string) int {
	n := 0
	for _, v := range vs {
		if v.Rule == want && strings.HasSuffix(filepath.ToSlash(v.Path), pathSuffix) {
			n++
		}
	}
	return n
}

// rulesByPath indexes a violation slice by file path → set of rule ids, used
// by the idempotency check (which only compares two verdicts for equality).
func rulesByPath(vs []violation) map[string][]string {
	m := map[string][]string{}
	for _, v := range vs {
		m[filepath.ToSlash(v.Path)] = append(m[filepath.ToSlash(v.Path)], v.Rule)
	}
	return m
}

// TestValidateUpload_CleanTreePasses pins sty_50ecb56f AC1/AC5: a well-formed
// tree — a skill with name+description, a document, a principle — yields no
// violations.
func TestValidateUpload_CleanTreePasses(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/feature-workflow.md",
		"---\nname: feature-workflow\ndescription: the feature lifecycle\nkind: workflow\napplies_to: [feature]\n---\n# wf\n")
	writeSource(t, root, "skills/satellites-story-done-review.md",
		"---\nname: satellites-story-done-review\ndescription: done gate\nkind: gate\nwhen: status==in_progress\n---\n# gate\n")
	writeSource(t, root, "documents/project-config.md",
		"---\nname: project-config\nscope: project\n---\n# cfg\n")
	writeSource(t, root, "principles/agent-goals.md",
		"---\ntags: [principles:project]\n---\n# goals\n")

	vs, err := validateUpload(root, filepath.Join(root, "no-claude-skills"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if len(vs) != 0 {
		t.Fatalf("clean tree should pass, got violations: %v", vs)
	}
}

// TestValidateUpload_WorkflowLifecycleDrift (sty_1604064f): a kind:workflow skill
// whose state machine breaks the engagement lifecycle — here a cyclic workflow
// with no terminal state — is flagged loudly at upload, not silently mis-gated.
func TestValidateUpload_WorkflowLifecycleDrift(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/broken-workflow.md",
		"---\nname: broken-workflow\ndescription: a cyclic workflow with no terminal\nkind: workflow\napplies_to: [broken]\n---\n# broken\n\n```yaml\nstates:\n  - a\n  - b\ntransitions:\n  - {from: a, to: b}\n  - {from: b, to: a}\n```\n")

	vs, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if !flagged(vs, "skills/broken-workflow.md", "workflow-lifecycle") {
		t.Fatalf("cyclic workflow (no terminal) not flagged: %v", vs)
	}
}

// TestValidateUpload_WorkflowLifecycleValidPasses: a well-formed
// begin→work→end workflow draws no lifecycle violation.
func TestValidateUpload_WorkflowLifecycleValidPasses(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/ok-workflow.md",
		"---\nname: ok-workflow\ndescription: a sound lifecycle\nkind: workflow\napplies_to: [ok]\n---\n# ok\n\n```yaml\nstates:\n  - backlog\n  - in_progress\n  - done\ntransitions:\n  - {from: backlog, to: in_progress, reviewer_skill: plan}\n  - {from: in_progress, to: done, reviewer_skill: done}\n```\n")

	vs, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if flagged(vs, "skills/ok-workflow.md", "workflow-lifecycle") {
		t.Errorf("valid workflow wrongly flagged for lifecycle drift: %v", vs)
	}
}

// TestValidateUpload_TypeMismatches pins AC2: a skills/ file declaring
// type:document, and a documents/ file declaring type:skill, are both
// rejected — naming file + rule.
func TestValidateUpload_TypeMismatches(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/mislabeled.md",
		"---\nname: mislabeled\ndescription: d\ntype: document\n---\n# x\n")
	writeSource(t, root, "documents/pretender.md",
		"---\nname: pretender\ntype: skill\n---\n# x\n")

	vs, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if !flagged(vs, "skills/mislabeled.md", "type-mismatch") {
		t.Errorf("skills/ type:document not flagged: %v", vs)
	}
	if !flagged(vs, "documents/pretender.md", "type-mismatch") {
		t.Errorf("documents/ type:skill not flagged: %v", vs)
	}
}

// TestValidateUpload_SkillMissingFrontmatter pins AC2: a skills/ file missing
// name and description is rejected on both.
func TestValidateUpload_SkillMissingFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/bare.md", "---\n---\n# body only\n")

	vs, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if got := countRule(vs, "skills/bare.md", "skill-frontmatter"); got != 2 {
		t.Fatalf("expected 2 skill-frontmatter violations (name + description), got %d: %v", got, vs)
	}
}

// TestValidateUpload_SkillDispatch pins the dispatch-contract checks
// (sty_3359cb48): a skill missing kind, an invalid kind, and a workflow
// missing applies_to are each flagged skill-dispatch; a fully-specified skill
// is clean of that rule.
func TestValidateUpload_SkillDispatch(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/no-kind.md",
		"---\nname: no-kind\ndescription: d\n---\n# x\n")
	writeSource(t, root, "skills/bad-kind.md",
		"---\nname: bad-kind\ndescription: d\nkind: widget\n---\n# x\n")
	writeSource(t, root, "skills/wf-no-applies.md",
		"---\nname: wf-no-applies\ndescription: d\nkind: workflow\n---\n# x\n")
	writeSource(t, root, "skills/ok-gate.md",
		"---\nname: ok-gate\ndescription: d\nkind: gate\nwhen: status==in_progress\n---\n# x\n")

	vs, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if !flagged(vs, "skills/no-kind.md", "skill-dispatch") {
		t.Errorf("missing kind not flagged: %v", vs)
	}
	if !flagged(vs, "skills/bad-kind.md", "skill-dispatch") {
		t.Errorf("invalid kind not flagged: %v", vs)
	}
	if !flagged(vs, "skills/wf-no-applies.md", "skill-dispatch") {
		t.Errorf("workflow missing applies_to not flagged: %v", vs)
	}
	if flagged(vs, "skills/ok-gate.md", "skill-dispatch") {
		t.Errorf("fully-specified gate should be clean of skill-dispatch: %v", vs)
	}
}

// TestValidateUpload_UnsupportedIdentity pins sty_afc0769c AC2/AC3: a
// workspace scope, a workspace_id, and a mismatched project_id in frontmatter
// are each rejected — the client authors only project scope under the repo's
// own project_id.
func TestValidateUpload_UnsupportedIdentity(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "documents/ws-scope.md",
		"---\nname: ws-scope\nscope: workspace\n---\n# x\n")
	writeSource(t, root, "documents/has-ws.md",
		"---\nname: has-ws\nworkspace_id: wksp_OTHER\n---\n# x\n")
	writeSource(t, root, "documents/wrong-proj.md",
		"---\nname: wrong-proj\nproject_id: proj_OTHER\n---\n# x\n")

	vs, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if !flagged(vs, "documents/ws-scope.md", "scope-unsupported") {
		t.Errorf("workspace scope not flagged: %v", vs)
	}
	if !flagged(vs, "documents/has-ws.md", "workspace-unsupported") {
		t.Errorf("workspace_id not flagged: %v", vs)
	}
	if !flagged(vs, "documents/wrong-proj.md", "project-mismatch") {
		t.Errorf("mismatched project_id not flagged: %v", vs)
	}
}

// TestValidateUpload_NestedLayoutRejected pins sty_afc0769c AC3: a file nested
// below the kind dir (the retired <wksp>/<proj> shape) is flagged layout.
func TestValidateUpload_NestedLayoutRejected(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "documents/wksp_one/proj_one/nested.md", "---\n---\n# x\n")

	vs, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if !flagged(vs, "nested.md", "layout") {
		t.Fatalf("nested file not flagged layout: %v", vs)
	}
}

// TestValidateUpload_OrphanStampedSkill pins AC3 drift: a stamped (sync-owned)
// skill in .claude/skills with no source under .satellites/skills/ is flagged,
// while a stamped skill that DOES have a source is not.
func TestValidateUpload_OrphanStampedSkill(t *testing.T) {
	root := t.TempDir()
	skillsRoot := t.TempDir()

	// source exists for "kept" only.
	writeSource(t, root, "skills/kept.md",
		"---\nname: kept\ndescription: d\n---\n# kept\n")

	// Two stamped local skills: "kept" (has source) and "orphan" (no source).
	if err := applySyncItem(skillsRoot, syncPlanItem{Name: "kept", Action: actionInstall,
		Sub: &substrateSkill{Name: "kept", DocumentID: "doc_k", Version: 1, Body: "---\nname: kept\n---\n# k\n"}}); err != nil {
		t.Fatalf("install kept: %v", err)
	}
	if err := applySyncItem(skillsRoot, syncPlanItem{Name: "orphan", Action: actionInstall,
		Sub: &substrateSkill{Name: "orphan", DocumentID: "doc_o", Version: 1, Body: "---\nname: orphan\n---\n# o\n"}}); err != nil {
		t.Fatalf("install orphan: %v", err)
	}

	vs, err := validateUpload(root, skillsRoot, testProjectID)
	if err != nil {
		t.Fatalf("validateUpload: %v", err)
	}
	if !flagged(vs, "/orphan", "orphan-skill") {
		t.Errorf("orphan stamped skill not flagged: %v", vs)
	}
	if flagged(vs, "/kept", "orphan-skill") {
		t.Errorf("source-backed stamped skill wrongly flagged as orphan: %v", vs)
	}
}

// TestValidateUpload_Idempotent pins AC4: validateUpload is a pure read — two
// runs over the same tree return identical verdicts and write nothing.
func TestValidateUpload_Idempotent(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "skills/bad.md", "---\n---\n# x\n")

	a, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := validateUpload(root, filepath.Join(root, "none"), testProjectID)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(rulesByPath(a), rulesByPath(b)) {
		t.Fatalf("non-idempotent verdict:\n %v\n %v", a, b)
	}
}
