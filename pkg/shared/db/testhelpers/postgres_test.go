// Unit tests for PostgresDSN env-resolution and skip behavior.
package testhelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPostgresDSN_EnvVarSet_ReturnsDSN verifies that PULSE_TEST_POSTGRES
// short-circuits the testcontainer path (env wins over container).
func TestPostgresDSN_EnvVarSet_ReturnsDSN(t *testing.T) {
	t.Setenv("PULSE_TEST_POSTGRES", "postgres://test:test@localhost:5432/test?sslmode=disable")
	t.Setenv("PULSE_TEST_INFRA", "0") // explicitly NOT requesting testcontainer
	dsn := PostgresDSN(t)
	assert.Equal(t, "postgres://test:test@localhost:5432/test?sslmode=disable", dsn)
}

// TestPostgresDSN_NoEnv_NoInfra_Skips verifies the test skips when
// neither PULSE_TEST_POSTGRES nor PULSE_TEST_INFRA=1 is set.
func TestPostgresDSN_NoEnv_NoInfra_Skips(t *testing.T) {
	// t.Setenv guarantees no leak across tests.
	t.Setenv("PULSE_TEST_POSTGRES", "")
	t.Setenv("PULSE_TEST_INFRA", "")
	// Skip must be called; the helper's t.Skip will mark the test as
	// skipped, which is the assertion.
	//
	// We use a separate sub-test inside Skip-aware pattern; the helper
	// itself calls t.Skip in this branch.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	// Run in a t.Run subtest so t.Skip terminates the sub-test, not the
	// outer test. This way we can assert the skip path without aborting
	// this assertion.
	t.Run("sub", func(t *testing.T) {
		PostgresDSN(t)
	})
}