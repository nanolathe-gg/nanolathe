package construction

import (
	"math"
	"testing"
)

// The maximum is read unsigned; each cumulative product keeps the low word
// after signed-64 truncation, before subtraction [05 R-WORK-01 §1]
// [01 R-DET-01 §1]. These cases distinguish the live path from saturating
// host int32 conversion and from signed interpretation of the definition.
func TestConstructionStepHealthUsesLowWordProducts(t *testing.T) {
	remaining, gain, _, _ := ConstructionStep(1, 1, 2, -2, 0, 0)
	// 4294967294*1 retains -2; 4294967294*0.5 retains 2147483647.
	// Their signed-word subtraction wraps to 2147483647.
	if remaining != 0.5 || gain != 2147483647 {
		t.Fatalf("remaining/gain = %v/%d", remaining, gain)
	}
	remaining, gain, _, _ = ConstructionStep(float32(math.Inf(1)), 1, 2, 100, 0, 0)
	// The old non-finite product retains zero. The new fraction clamps to 1,
	// so the second product is 100 and the difference is -100.
	if remaining != 1 || gain != -100 {
		t.Fatalf("non-finite old remaining/gain = %v/%d", remaining, gain)
	}
}
