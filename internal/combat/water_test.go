package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"testing"
)

func TestWaterDamage_IsInWaterIntegerHeight(t *testing.T) {
	// signed integer height at or below sea-level byte [04 §9.2]
	// fraction .5 should not matter for integer check
	if !IsInWaterForDamage(numeric.FixedFromInt(10), 10) {
		t.Fatalf("10 <=10 should be in water [04 §9.2]")
	}
	if IsInWaterForDamage(numeric.FixedFromInt(11), 10) {
		t.Fatalf("11 >10 should not be in water [04 §9.2]")
	}
	// fractional: 10.9 truncs to 10 => still in water if sea 10
	if !IsInWaterForDamage(numeric.Fixed(10*65536+32768), 10) {
		t.Fatalf("10.5 trunc 10 <=10 should be in water [04 §9.2] integer height")
	}
}
