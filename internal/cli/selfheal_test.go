package cli

import (
	"context"
	"testing"

	"github.com/bobmcallan/satellites/internal/cliconfig"
)

func TestShouldAttemptSelfHeal_Skips(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		verb string
		cfg  cliconfig.Config
		path string
		want bool
	}{
		{
			name: "no config path",
			ctx:  context.Background(),
			verb: "story_list",
			cfg:  cliconfig.Config{},
			path: "",
			want: false,
		},
		{
			name: "already has project_id",
			ctx:  context.Background(),
			verb: "story_list",
			cfg:  cliconfig.Config{ProjectID: "proj_x"},
			path: "/x/y.toml",
			want: false,
		},
		{
			name: "verb never needs project_id",
			ctx:  context.Background(),
			verb: "version",
			cfg:  cliconfig.Config{},
			path: "/x/y.toml",
			want: false,
		},
		{
			name: "project_match itself",
			ctx:  context.Background(),
			verb: "project_match",
			cfg:  cliconfig.Config{},
			path: "/x/y.toml",
			want: false,
		},
		{
			name: "already inside heal",
			ctx:  withSelfHealSkip(context.Background()),
			verb: "story_list",
			cfg:  cliconfig.Config{},
			path: "/x/y.toml",
			want: false,
		},
		{
			name: "eligible: story_list with no project_id",
			ctx:  context.Background(),
			verb: "story_list",
			cfg:  cliconfig.Config{},
			path: "/x/y.toml",
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAttemptSelfHeal(tc.ctx, tc.verb, tc.cfg, tc.path)
			if got != tc.want {
				t.Fatalf("shouldAttemptSelfHeal = %v, want %v", got, tc.want)
			}
		})
	}
}
