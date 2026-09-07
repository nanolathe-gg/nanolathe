package construction

import (
	"github.com/nanolathe/nanolathe/internal/economy"
	"math"
	"testing"
)

func TestRepairUsesUnorderedAdmissionVerdict(t *testing.T) {
	svc, builder, target, _ := reclaimFixture(t, 10, 16)
	target.Def.BuildTime = 100
	target.Def.BuildCostEnergy = 100
	b := svc.Economy.UnitBuckets(builder.Handle)
	b[economy.Energy].Carry = float32(math.NaN())
	if !svc.Repair(builder, target, 1) {
		t.Fatal("unordered carry repair denied [05 R-ECO-01 §7]")
	}
	if target.Health != 11 || b[economy.Energy].Requested != 1 || b[economy.Energy].Accepted != 1 {
		t.Fatalf("health=%d energy=%+v", target.Health, b[economy.Energy])
	}
}
