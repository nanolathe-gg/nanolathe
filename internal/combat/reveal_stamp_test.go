package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestShotStampsShooterRevealDeadlineOutright locks the fill's real-shooter
// branch [06 §4.1]: every shot writes the shooter's "fired recently" deadline
// at current tick + 600.
//
// The write is OUTRIGHT, not a maximum. The deadline is one shared word with
// twelve gameplay writers and retail takes no maximum, so a shot can SHORTEN a
// longer reveal already in progress just as a sensor breach's `tick + 90` can
// [03 R-VIS-01 §6 "Writer census of the shared deadline"]. A larger prior
// value must therefore not survive — which is what this test's seeded 999999
// is for.
func TestShotStampsShooterRevealDeadlineOutright(t *testing.T) {
	var svc Service
	w := &content.WeaponDef{
		ID:             5,
		LineOfSight:    true, // ordinary creation family [06 §6.2]
		WeaponVelocity: int32(numeric.FixedFromInt(4)),
		Range:          1200,
	}
	shooter := &units.Unit{Handle: 3, Owner: 2}
	shooter.RevealDeadline = 999999 // a prior, larger value must not survive.

	r := rng.NewSimulation(1)
	const tick = uint32(4200)
	h, ok := TryFire(&svc, &Slot{Weapon: w}, 0,
		Target{Kind: TargetPoint, X: numeric.FixedFromInt(500)}, tick,
		FirePorts{ShooterSide: 2, Shooter: shooter, RNG: &r})
	if !ok || h == 0 {
		t.Fatalf("shot did not spawn: ok=%v h=%d", ok, h)
	}
	if want := tick + 600; shooter.RevealDeadline != want {
		t.Fatalf("RevealDeadline=%d want %d (tick+600, written outright) [06 §4.1]",
			shooter.RevealDeadline, want)
	}
	// The same branch writes the shooter reference, and it is now written by
	// the fill rather than bound by the caller after the spawner returned.
	if got := svc.Records[int(h)-1].Shooter; got != shooter.Handle {
		t.Fatalf("record shooter=%d want %d [06 §4.1]", got, shooter.Handle)
	}
}

// TestShooterlessShotStampsNothing is the null-shooter half of the same
// branch: the meteor creator writes the neutral side byte and a null shooter
// reference, and stamps no deadline [06 §4.1] [06 §6.5].
func TestShooterlessShotStampsNothing(t *testing.T) {
	var svc Service
	w := &content.WeaponDef{
		ID:             5,
		LineOfSight:    true,
		WeaponVelocity: int32(numeric.FixedFromInt(4)),
		Range:          1200,
	}
	r := rng.NewSimulation(1)
	h, ok := TryFire(&svc, &Slot{Weapon: w}, 0,
		Target{Kind: TargetPoint, X: numeric.FixedFromInt(500)}, 4200,
		FirePorts{ShooterSide: 10, RNG: &r})
	if !ok || h == 0 {
		t.Fatalf("shot did not spawn: ok=%v h=%d", ok, h)
	}
	if got := svc.Records[int(h)-1].Shooter; got != 0 {
		t.Fatalf("shooterless record shooter=%d want 0 [06 §6.5]", got)
	}
}

// TestCombatCloakPredicateReadsInstanceBitOnly locks the acquisition gate's
// cloak reject to the INSTANCE cloaked bit [06 §3.1] step 2, [03 R-VIS-01 §6].
//
// Both definition flags used to be ORed into it:
//
//   - `init_cloaked` is consumed exactly once, by the unit constructor, which
//     seeds the cloak-REQUESTED bit from it; the instance bit is written only
//     by the settlement's transition service, on a pass the owner actually
//     paid for [05 R-ECO-01 §9]. The two bits are separate fields
//     (WU-19-92) — Unit.IsCloaked requests, Unit.Hidden hides — so the case
//     below is written the way the settlement leaves it when the owner cannot
//     pay: the definition flag set, the instance bit clear.
//   - `stealth` never touches line of sight at all: it is the contact
//     callback's third reject, suppressing radar and sonar detection outright
//     [03 R-VIS-01 §5].
func TestCombatCloakPredicateReadsInstanceBitOnly(t *testing.T) {
	cases := []struct {
		name        string
		initCloaked bool
		stealth     bool
		instance    bool
		want        bool
	}{
		{name: "plain", want: false},
		{name: "instance cloak paid", instance: true, want: true},
		{name: "init_cloaked, cloak pass unpaid", initCloaked: true, want: false},
		{name: "stealth in the open", stealth: true, want: false},
		{name: "stealth and cloaked", stealth: true, instance: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &units.Unit{
				Handle: 1,
				Def:    &content.UnitDef{InitCloaked: tc.initCloaked, Stealth: tc.stealth},
			}
			u.Hidden = tc.instance
			if got := isCloakedUnit(u); got != tc.want {
				t.Fatalf("isCloakedUnit=%v want %v [06 §3.1][03 R-VIS-01 §6]", got, tc.want)
			}
		})
	}
}
