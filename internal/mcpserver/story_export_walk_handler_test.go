package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobmcallan/satellites/internal/story"
)

// TestStoryExportWalk_MarkdownShape checks the deterministic markdown
// shape: H1 story header, AC block, H2 contract group with iteration
// suffix on loops, H3 CI rows, footer line. sty_a248f4df.
func TestStoryExportWalk_MarkdownShape(t *testing.T) {
	t.Parallel()
	f := newTaskWalkFixture(t)
	ctx := context.WithValue(context.Background(), userKey, f.caller)

	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"story_id": f.storyID}
	res, err := f.server.handleStoryExportWalk(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError, "%+v", res)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &payload))
	content, _ := payload["content"].(string)
	require.NotEmpty(t, content)

	for _, want := range []string{
		"# " + f.storyID,
		"loop story",
		"## develop ×3 (loop)",
		"### develop #1   role=developer",
		"### develop #2   role=developer",
		"### develop #3   role=developer",
		"## push",
		"### push #1   role=releaser",
		"Process defined by: project default",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("markdown missing %q\n---full content---\n%s", want, content)
		}
	}
	assert.NotContains(t, content, "## push ×")
}

// TestStoryExportWalk_EmptyStory renders the header + footer with no
// per-CI sections.
func TestStoryExportWalk_EmptyStory(t *testing.T) {
	t.Parallel()
	f := newTaskWalkFixture(t)
	// Build a fresh empty story sharing the project.
	emptyStory, err := f.server.stories.Create(context.Background(), story.Story{
		WorkspaceID: f.wsID,
		ProjectID:   f.projectID,
		Title:       "blank",
		Status:      story.StatusBacklog,
	}, f.now)
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), userKey, f.caller)
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"story_id": emptyStory.ID}
	res, err := f.server.handleStoryExportWalk(ctx, req)
	require.NoError(t, err)
	require.False(t, res.IsError)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcpgo.TextContent).Text), &payload))
	content, _ := payload["content"].(string)
	assert.Contains(t, content, "blank")
	assert.NotContains(t, content, "### ")
	assert.Contains(t, content, "Process defined by:")
}

// TestStoryExportWalk_UnsupportedFormat rejects non-markdown formats.
func TestStoryExportWalk_UnsupportedFormat(t *testing.T) {
	t.Parallel()
	f := newTaskWalkFixture(t)
	ctx := context.WithValue(context.Background(), userKey, f.caller)
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"story_id": f.storyID, "format": "pdf"}
	res, err := f.server.handleStoryExportWalk(ctx, req)
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
