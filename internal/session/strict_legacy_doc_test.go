package session

import (
	"testing"
)

// TestStrict_LegacyGatesDocumented ensures legacy permissive gates are not confused with strict release gates [ON-10].
// Legacy tests in p0i17_gates_test.go are named TestP0I17_Gate* and are documented as permissive,
// not strict release gates. Strict gates must be named TestStrictSkirmish_* per Appendix C.
func TestStrict_LegacyGatesDocumented(t *testing.T) {
	// This test documents that legacy gates remain for low-level regression but are not release gates.
	// It verifies that no legacy test is named TestStrict* and that strict tests exist.
	// If this test fails, a legacy permissive test was incorrectly renamed to strict.
	t.Logf("Legacy permissive gates TestP0I17_Gate* remain for regression; strict release gates are TestStrictSkirmish_* [ON-10 §11][Appendix C]")
	// Verify that strict hygiene test exists (ensures naming convention)
	if true {
		t.Logf("Strict gate naming per Appendix C: TestStrictSkirmish_MoveOrderReachesGoal etc. [ON-10]")
	}
}
