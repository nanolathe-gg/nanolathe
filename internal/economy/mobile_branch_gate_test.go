package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestMobileUpkeepBranchGate locks the second term of the mobile branch's gate
// [05 R-ECO-01 §2]: the branch runs when the unit is activated OR its cached
// movement-rate tier is non-zero — that is, while it is actually under way on
// its own mover [04 R-MOV-01 §6].
//
// The regression this exists to catch is reading the movement-mode mirror
// instead. The mirror is non-zero for every parked ground unit, so gating on it
// bills upkeep for every mobile definition with an authored `energyuse` on
// every tick of its life; the tier is zero while stopped.
func TestMobileUpkeepBranchGate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		activated bool
		tier      uint8
		mode      uint8
		wantSpend bool
	}{
		{name: "parked-and-inactive", wantSpend: false},
		{name: "parked-with-a-non-zero-mode-mirror", mode: 1, wantSpend: false},
		{name: "moving-at-the-lowest-tier", tier: 1, wantSpend: true},
		{name: "moving-at-the-highest-tier", tier: 3, wantSpend: true},
		{name: "activated-while-parked", activated: true, wantSpend: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := units.NewSliced(10, &content.Catalog{})
			svc := &Service{}
			svc.Players[0].Exists = true
			svc.Players[0].ControllerState = 1

			def := economyFixtureDef(&content.UnitDef{
				UnitName: "mobileupkeep", EnergyUse: 4, BuildTime: 1, MaxDamage: 1,
			})
			def.BMCode = 1
			h, err := w.Create(def, 0, 0, 0, 0)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			u := w.Unit(h)
			u.Activated = tc.activated
			u.MoveTier = tc.tier
			u.Move.Mode = tc.mode

			svc.PerUnitProductionFills(0, w)

			// The upkeep request is raised by the branch itself, before the
			// carry gate decides whether it is accepted, so it is the direct
			// witness of the branch having run.
			got := svc.UnitBuckets(h)[Energy].Requested
			want := float32(0)
			if tc.wantSpend {
				want = 4
			}
			if got != want {
				t.Fatalf("energy requested = %v, want %v (branch ran = %v)", got, want, tc.wantSpend)
			}
		})
	}
}
