package construction

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

func TestRepairRetainsNegativeTermInUnsignedPacket(t *testing.T) {
	svc, builder, target, _ := reclaimFixture(t, 10, 16)
	target.Def.BuildTime = -10
	target.Def.BuildCostEnergy = 100
	target.BlinkSuppress = 7
	target.LastDamageCause = 6
	target.LastDamageSide = 3
	if !svc.Repair(builder, target, 1) {
		t.Fatal("negative repair term was not admitted")
	}
	// trunc(1 + 99/-10) = -8; its unsigned low word is 65528. The heal
	// dispatcher therefore clamps to max 100, with no damage effects [05 R-WORK-01 §3].
	if target.Health != 100 || target.BlinkSuppress != 7 || target.LastDamageCause != 6 || target.LastDamageSide != 3 || target.Dying {
		t.Fatalf("repair state: health=%d flash=%d cause=%d side=%d dying=%t", target.Health, target.BlinkSuppress, target.LastDamageCause, target.LastDamageSide, target.Dying)
	}
	b := svc.Economy.UnitBuckets(builder.Handle)[economy.Energy]
	if b.Requested != -8 || b.Accepted != -8 {
		t.Fatalf("repair demand=%+v, want -8 requested/accepted", b)
	}
}

func TestRepairTermsUseLowWordConversion(t *testing.T) {
	heal, energy := repairTerms(math.MaxInt32, float32(1<<32), 2, 1)
	if heal != -2 || energy != 0 {
		t.Fatalf("wide terms=%d/%d, want low words -2/0", heal, energy)
	}
	for _, cost := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		heal, energy = repairTerms(100, cost, 1, 1)
		if heal != 1 || energy != 0 {
			t.Fatalf("exceptional cost %v terms=%d/%d, want 1/0", cost, heal, energy)
		}
	}
	if heal, energy = repairTerms(100, 100, 1, 0); heal != 0 || energy != 0 {
		t.Fatalf("zero build time=%d/%d, want 0/0", heal, energy)
	}
}
