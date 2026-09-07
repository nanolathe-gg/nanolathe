package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestAutonomousScanRequiresTheArmedStatusBit locks the autonomous scan's third
// per-unit clause, [06 §3.2 "The third clause is the armed bit"]: the visited
// unit must carry the ARMED status bit, the one unit creation derives from the
// definition's three weapon links. The clause was left unmodelled until
// WU-19-147, so the gate visited every weaponless unit as well.
//
// The two rows are two DEFINITIONS on either side of the gate — one with a
// resolved weapon link, one with all three links empty — because the bit is a
// property of the definition, written once at creation and never again
// [08 "Classifier eligibility, destinations, and order"]. Everything else the
// predicate reads is held equal: both are fully built and both stand at
// fire-at-will.
func TestAutonomousScanRequiresTheArmedStatusBit(t *testing.T) {
	// Weapon record 0 is the inactive sentinel a missed link resolves to and is
	// not a weapon [02 §5 R-CONTENT-02], so it belongs on the unarmed side.
	armedDef := &content.UnitDef{
		UnitName:          "armedfixture",
		MaxDamage:         100,
		Limit:             -1,
		StandingFireOrder: 2, // FIRE AT WILL [04 R-STANCE-01 §6]
		Weapon1Def:        &content.WeaponDef{ID: 1},
	}
	unarmedDef := &content.UnitDef{
		UnitName:          "unarmedfixture",
		MaxDamage:         100,
		Limit:             -1,
		StandingFireOrder: 2,
		Weapon1Def:        &content.WeaponDef{ID: 0}, // the inactive sentinel
	}

	for _, tc := range []struct {
		name       string
		def        *content.UnitDef
		wantArmed  bool
		wantVisits bool
	}{
		{"a definition with a resolved weapon link is visited", armedDef, true, true},
		{"a definition with no resolved weapon link is not visited", unarmedDef, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newCombatFixtureWorld(4, nil)
			h, err := w.Create(tc.def, 0, numeric.FixedFromInt(10), numeric.FixedFromInt(10), numeric.FixedFromInt(10))
			if err != nil {
				t.Fatalf("create %s: %v", tc.def.UnitName, err)
			}
			u := w.Unit(h)
			if got := u.Flags&units.ArmedStatus != 0; got != tc.wantArmed {
				t.Fatalf("armed bit = %v, want %v: creation derives it from the definition's three weapon links", got, tc.wantArmed)
			}
			// Hold the other three clauses equal, so the only thing separating
			// the rows is the armed bit.
			if u.Remaining != 0 {
				t.Fatalf("fixture is not fully built: remaining %v", u.Remaining)
			}
			if got := u.Flags >> units.StandingFireShift & units.StandingFieldMask; got != stanceFireAtWill {
				t.Fatalf("stance field = %d, want %d (fire at will)", got, stanceFireAtWill)
			}
			if got := autonomousScanAdmitsUnit(u); got != tc.wantVisits {
				t.Fatalf("scan visits = %v, want %v [06 §3.2]", got, tc.wantVisits)
			}
		})
	}
}

// TestAutonomousScanArmedBitSenseIsSet fixes the SENSE of the clause, which is
// the half a wrong reading would invert: the bit must be SET for the scan to
// proceed. Clearing it on an otherwise admissible unit ends the visit; raising
// it again restores the visit. A gate that tested the bit clear — or that
// tested the other high bit, building class — would silence every mobile unit
// in the game while looking exactly as plausible.
func TestAutonomousScanArmedBitSenseIsSet(t *testing.T) {
	_, _, shooter, _ := newTestWorldAndUnits(t)
	// All three probes run on the same tick, so the round-robin window is held
	// fixed and the armed bit is the only thing that moves. The window is a
	// range of RECORD indices over a fixed per-player slice [06 §3.2], so a
	// probe that stepped the tick would also step the cursor off this record and
	// read the cursor's advance as the armed bit's effect (WU-19-154; the
	// previous version of this test stepped ticks 1, 2, 3).
	if !autonomousScanAdmitsUnit(shooter) {
		t.Fatalf("the armed fixture must be visited [06 §3.2]")
	}
	shooter.Flags &^= units.ArmedStatus
	if autonomousScanAdmitsUnit(shooter) {
		t.Fatalf("a cleared armed bit must end the visit [06 §3.2]")
	}
	shooter.Flags |= units.ArmedStatus
	if !autonomousScanAdmitsUnit(shooter) {
		t.Fatalf("restoring the armed bit must restore the visit [06 §3.2]")
	}
}
