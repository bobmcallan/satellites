package server

import "testing"

func TestSafeNext(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty falls back", "", "/"},
		{"root", "/", "/"},
		{"local path", "/projects/proj_x", "/projects/proj_x"},
		{"local path with query", "/projects/proj_x?status=open&order=priority", "/projects/proj_x?status=open&order=priority"},
		{"protocol-relative rejected", "//evil.com/path", "/"},
		{"absolute http rejected", "http://evil.com", "/"},
		{"absolute https rejected", "https://evil.com/x", "/"},
		{"bare word rejected", "evil.com", "/"},
		{"backslash trick rejected", "/\\evil.com", "/"},
		{"scheme without slash rejected", "javascript:alert(1)", "/"},
		{"relative without leading slash rejected", "projects/x", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeNext(tc.raw); got != tc.want {
				t.Fatalf("safeNext(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
