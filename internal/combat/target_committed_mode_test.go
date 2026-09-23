package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The live-unit adapter supplies the target's committed mover mode to both
// order admission and autonomous acquisition [06 R-WPN-05 §1]. A refused
// touchdown leaves request/mirror 1/2, while a pending takeoff may leave 2/1;
// neither request changes the anti-air answer before the movement commit
// accepts it [04 R-MOV-01 §8][04 R-COLL-01 §1].
func TestTargetAdmissionUsesCommittedMoverMode(t *testing.T) {
	for _, rule := range []struct {
		name  string
		rules Rules
	}{
		{name: "strict", rules: StrictRules{}},
		{name: "community", rules: CommunityRules{}},
		{name: "modern", rules: &ModernRules{}},
	} {
		for _, transition := range []struct {
			name               string
			request, committed uint8
			want               bool
		}{
			{name: "blocked-touchdown", request: 1, committed: airborneMoverMode, want: true},
			{name: "pending-takeoff", request: airborneMoverMode, committed: 1, want: false},
		} {
			t.Run(rule.name+"/"+transition.name, func(t *testing.T) {
				svc := &Service{
					Rules:     rule.rules,
					Community: community.Features{WeaponTargetKeys: true},
				}
				shooter := &units.Unit{
					Handle: 1,
					Owner:  0,
					Alive:  true,
					Y:      fixed(10),
					Def:    &content.UnitDef{ModelTop: 10},
				}
				target := &units.Unit{
					Handle: 2,
					Owner:  1,
					Alive:  true,
					X:      fixed(10),
					Y:      fixed(10),
					Def:    &content.UnitDef{CanFly: true, ModelTop: 10},
				}
				target.Move.Mode = transition.request
				target.Move.ModeMirror = transition.committed
				weapon := &content.WeaponDef{ID: 1, Range: 1000, ToAirWeapon: true}
				shooter.InstallWeapon(0, weapon)

				if got := svc.CanEngageSlotTarget(shooter, target, 0, nil); got != transition.want {
					t.Errorf("order admission = %v, want %v for request/mirror %d/%d", got, transition.want, transition.request, transition.committed)
				}

				candidate := acquisitionCandidate(shooter, target, 0, 0, nil)
				sim := rng.NewSimulation(1)
				attempt := base(&sim)
				attempt.Service = svc
				attempt.Weapon = weapon
				if handle, got := AcquireTarget([]Candidate{candidate}, attempt); got != transition.want {
					t.Errorf("acquisition = %v target %d, want %v for request/mirror %d/%d", got, handle, transition.want, transition.request, transition.committed)
				}
			})
		}
	}
}
