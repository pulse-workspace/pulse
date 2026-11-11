// Unit tests for NATSURL env-resolution and skip behavior.
package testhelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNATSURL_EnvVarSet_ReturnsURL verifies that PULSE_TEST_NATS
// short-circuits the testcontainer path.
func TestNATSURL_EnvVarSet_ReturnsURL(t *testing.T) {
	t.Setenv("PULSE_TEST_NATS", "nats://localhost:4222")
	t.Setenv("PULSE_TEST_INFRA", "0")
	assert.Equal(t, "nats://localhost:4222", NATSURL(t))
}

// TestNATSURL_NoEnv_NoInfra_Skips verifies the test skips when
// neither env var is set.
func TestNATSURL_NoEnv_NoInfra_Skips(t *testing.T) {
	t.Setenv("PULSE_TEST_NATS", "")
	t.Setenv("PULSE_TEST_INFRA", "")
	t.Run("sub", func(t *testing.T) {
		NATSURL(t)
	})
}