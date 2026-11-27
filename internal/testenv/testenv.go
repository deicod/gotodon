// Package testenv decides what a test that needs PostgreSQL or Redis does
// when the run does not provide one.
package testenv

import (
	"os"
	"strconv"
	"testing"
)

// RequireServicesVar names the environment variable that, set to a true
// value ("1", "true", ...), makes tests that need PostgreSQL or Redis fail
// instead of skip when the run does not provide them. A run meant to cover
// those tests sets it, so that it cannot pass with them skipped.
const RequireServicesVar = "GOTODON_TEST_REQUIRE_SERVICES"

// SkipService skips t, a test that needs PostgreSQL or Redis which this run
// does not provide, giving reason. When RequireServicesVar is set to a true
// value it fails t instead, and when it holds no boolean it fails t too.
func SkipService(t testing.TB, reason string) {
	t.Helper()
	if value := os.Getenv(RequireServicesVar); value != "" {
		required, err := strconv.ParseBool(value)
		if err != nil {
			t.Fatalf("%s=%q is not a boolean; %s", RequireServicesVar, value, reason)
		}
		if required {
			t.Fatalf("%s, but %s requires PostgreSQL- and Redis-backed tests to run", reason, RequireServicesVar)
		}
	}
	t.Skip(reason)
}
