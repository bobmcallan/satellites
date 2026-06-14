package verb

import "testing"

// taskSpecFromSkillBody strips a leading frontmatter block and satellites stamp
// comment lines, leaving the instruction markdown a run feeds the generator.
func TestTaskSpecFromSkillBody(t *testing.T) {
	body := "---\nname: corpus-summarise\nkind: task\n---\n" +
		"<!-- satellites-sync:begin {\"x\":1} satellites-sync:end -->\n\n" +
		"# Corpus summarise\n\nDo the thing.\n"
	if got, want := taskSpecFromSkillBody(body), "# Corpus summarise\n\nDo the thing."; got != want {
		t.Fatalf("frontmatter+stamp strip:\n got=%q\nwant=%q", got, want)
	}
	if got := taskSpecFromSkillBody("\n\njust text\n"); got != "just text" {
		t.Fatalf("plain body: got=%q want=%q", got, "just text")
	}
	if got := taskSpecFromSkillBody("---\nonly: frontmatter\n---\n"); got != "" {
		t.Fatalf("empty spec: got=%q want empty", got)
	}
}
