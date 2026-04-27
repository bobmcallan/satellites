package contract

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestACIterationCount(t *testing.T) {
	t.Parallel()
	cis := []ContractInstance{
		{ID: "ci1", ACScope: []int{1, 2, 3}},
		{ID: "ci2", ACScope: []int{2}},
		{ID: "ci3"},
		{ID: "ci4", ACScope: []int{3}},
	}
	assert.Equal(t, 1, ACIterationCount(cis, 1))
	assert.Equal(t, 2, ACIterationCount(cis, 2))
	assert.Equal(t, 2, ACIterationCount(cis, 3))
	assert.Equal(t, 0, ACIterationCount(cis, 99))
}

func TestValidateACScope_UnderCap(t *testing.T) {
	t.Parallel()
	existing := []ContractInstance{
		{ID: "ci1", ACScope: []int{1, 2, 3}},
		{ID: "ci2", ACScope: []int{2}},
	}
	amended := []ContractInstance{
		{ID: "ci3", ACScope: []int{2}},
	}
	assert.NoError(t, ValidateACScope(existing, amended, 5))
}

func TestValidateACScope_AtCap(t *testing.T) {
	t.Parallel()
	// AC 2 already amended 4 times beyond initial; one more brings it to 5,
	// which equals the cap and is allowed.
	existing := []ContractInstance{
		{ID: "ci1", ACScope: []int{2}},
		{ID: "ci2", ACScope: []int{2}},
		{ID: "ci3", ACScope: []int{2}},
		{ID: "ci4", ACScope: []int{2}},
	}
	amended := []ContractInstance{
		{ID: "ci5", ACScope: []int{2}},
	}
	assert.NoError(t, ValidateACScope(existing, amended, 5))
}

func TestValidateACScope_OverCap(t *testing.T) {
	t.Parallel()
	existing := []ContractInstance{
		{ID: "ci1", ACScope: []int{2}},
		{ID: "ci2", ACScope: []int{2}},
		{ID: "ci3", ACScope: []int{2}},
		{ID: "ci4", ACScope: []int{2}},
		{ID: "ci5", ACScope: []int{2}},
	}
	amended := []ContractInstance{
		{ID: "ci6", ACScope: []int{2}},
	}
	err := ValidateACScope(existing, amended, 5)
	assert.True(t, errors.Is(err, ErrACIterationCap), "got %v, want ErrACIterationCap", err)
}

func TestValidateACScope_BatchPushesOver(t *testing.T) {
	t.Parallel()
	existing := []ContractInstance{
		{ID: "ci1", ACScope: []int{2}},
		{ID: "ci2", ACScope: []int{2}},
		{ID: "ci3", ACScope: []int{2}},
		{ID: "ci4", ACScope: []int{2}},
	}
	amended := []ContractInstance{
		{ID: "ci5", ACScope: []int{2}},
		{ID: "ci6", ACScope: []int{2}},
	}
	err := ValidateACScope(existing, amended, 5)
	assert.True(t, errors.Is(err, ErrACIterationCap))
}

func TestMaxACIterations_DefaultsAndOverride(t *testing.T) {
	t.Setenv("SATELLITES_MAX_AC_ITERATIONS", "")
	assert.Equal(t, DefaultMaxACIterations, MaxACIterations())
	t.Setenv("SATELLITES_MAX_AC_ITERATIONS", "garbage")
	assert.Equal(t, DefaultMaxACIterations, MaxACIterations())
	t.Setenv("SATELLITES_MAX_AC_ITERATIONS", "10")
	assert.Equal(t, 10, MaxACIterations())
}
