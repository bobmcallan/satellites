package contract

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTreeWalk verifies that children of a parent CI render directly
// after their parent, regardless of insertion order.
func TestTreeWalk(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		input    []ContractInstance
		expected []string
	}{
		{
			name:     "empty",
			input:    nil,
			expected: nil,
		},
		{
			name: "single root",
			input: []ContractInstance{
				{ID: "ci_a", Sequence: 0},
			},
			expected: []string{"ci_a"},
		},
		{
			name: "flat sequence preserved",
			input: []ContractInstance{
				{ID: "ci_a", Sequence: 0},
				{ID: "ci_b", Sequence: 1},
				{ID: "ci_c", Sequence: 2},
			},
			expected: []string{"ci_a", "ci_b", "ci_c"},
		},
		{
			name: "child follows parent",
			input: []ContractInstance{
				{ID: "ci_plan", Sequence: 0},
				{ID: "ci_dev1", Sequence: 1},
				{ID: "ci_dev2", Sequence: 6, ParentInvocationID: "ci_dev1"},
				{ID: "ci_push", Sequence: 2},
			},
			expected: []string{"ci_plan", "ci_dev1", "ci_dev2", "ci_push"},
		},
		{
			name: "missing parent stays as root",
			input: []ContractInstance{
				{ID: "ci_a", Sequence: 0},
				{ID: "ci_orphan", Sequence: 1, ParentInvocationID: "ci_missing"},
			},
			expected: []string{"ci_a", "ci_orphan"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TreeWalk(tc.input)
			ids := make([]string, len(got))
			for i, c := range got {
				ids[i] = c.ID
			}
			if tc.expected == nil {
				assert.Empty(t, ids)
				return
			}
			assert.Equal(t, tc.expected, ids)
		})
	}
}
