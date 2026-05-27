package frontmatter

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantFM   Frontmatter
		wantBody string
		wantErr  bool
	}{
		{
			name:     "no frontmatter — whole input is body",
			input:    "# Plain markdown\n\ncontent",
			wantFM:   Frontmatter{},
			wantBody: "# Plain markdown\n\ncontent",
		},
		{
			name:     "tags only",
			input:    "---\ntags: [principles:global]\n---\n# Body\n",
			wantFM:   Frontmatter{Tags: []string{"principles:global"}},
			wantBody: "# Body\n",
		},
		{
			name:     "tags + name override",
			input:    "---\nname: custom-name\ntags:\n  - principles:project\n  - area:auth\n---\n# Body",
			wantFM:   Frontmatter{Name: "custom-name", Tags: []string{"principles:project", "area:auth"}},
			wantBody: "# Body",
		},
		{
			name:     "empty frontmatter block",
			input:    "---\n---\n# Body\n",
			wantFM:   Frontmatter{},
			wantBody: "# Body\n",
		},
		{
			name:     "CRLF line endings",
			input:    "---\r\ntags: [principles:workspace]\r\n---\r\n# Body\r\n",
			wantFM:   Frontmatter{Tags: []string{"principles:workspace"}},
			wantBody: "# Body\r\n",
		},
		{
			name:    "opening delimiter without closing → error",
			input:   "---\ntags: [x]\n# Body without closing\n",
			wantErr: true,
		},
		{
			name:    "malformed yaml → error",
			input:   "---\ntags: [unterminated\n---\n# Body\n",
			wantErr: true,
		},
		{
			name:     "frontmatter on a file starting with text — no frontmatter",
			input:    "Just text\n---\nthis is not a frontmatter block\n",
			wantFM:   Frontmatter{},
			wantBody: "Just text\n---\nthis is not a frontmatter block\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm, body, err := Parse([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; frontmatter=%+v body=%q", fm, string(body))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(fm, tc.wantFM) {
				t.Errorf("frontmatter mismatch:\n got  %+v\n want %+v", fm, tc.wantFM)
			}
			if string(body) != tc.wantBody {
				t.Errorf("body mismatch:\n got  %q\n want %q", string(body), tc.wantBody)
			}
		})
	}
}
