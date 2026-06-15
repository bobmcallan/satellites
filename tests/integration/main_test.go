//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestMain boots the one Postgres container shared by every test in this
// package (sty_0c98760e), then tears it down after the suite. Reusing a single
// container — instead of one per test — removes the Docker-daemon churn that
// timed out `docker inspect` under full-tier load; per-test clean data is
// preserved by testbootstrap.Reset (a full TRUNCATE) at each SetUp.
func TestMain(m *testing.M) {
	if err := testbootstrap.StartShared(); err != nil {
		fmt.Fprintln(os.Stderr, "integration bootstrap failed:", err)
		os.Exit(1)
	}
	code := m.Run()
	testbootstrap.Shutdown()
	os.Exit(code)
}
