package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestKamikazeShooterBypassesThePhysicalGate locks check 3 of the
// picked-candidate order [06 §3.2]: "one definition flag of the shooter
// bypasses the §3.1 physical gate entirely; otherwise that gate must accept.
// The bypass flag is `kamikaze`", which [04 "`kamikaze` and
// `kamikazedistance`"] restates as "a kamikaze definition bypasses the §3.1
// physical gate (range, arc, minimum range) for every candidate".
//
// Entirely means every clause of the gate, so the two clauses below — the
// non-water height pair and the `toairweapon` mover-mode test — are both
// bypassed, and neither is bypassed for an ordinary shooter.
func TestKamikazeShooterBypassesThePhysicalGate(t *testing.T) {
	sunk := hostileAt(1, 10, 2) // below sea level: the height clause refuses it
	ground := hostileAt(2, 10, 9)
	ground.MoverMode = 1 // not airborne: the `toairweapon` clause refuses it

	for _, tc := range []struct {
		name string
		cand Candidate
		gate func(a *Acquisition)
	}{
		{"belowSeaLevel", sunk, func(*Acquisition) {}},
		{"notAirborne", ground, func(a *Acquisition) { a.ToAir = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ordinaryRNG := rng.NewSimulation(1)
			ordinary := base(&ordinaryRNG)
			tc.gate(&ordinary)
			if _, ok := AcquireTarget([]Candidate{tc.cand}, ordinary); ok {
				t.Fatalf("the §3.1 gate admitted a candidate it refuses; the fixture no longer proves anything")
			}

			kamikazeRNG := rng.NewSimulation(1)
			kamikaze := base(&kamikazeRNG)
			tc.gate(&kamikaze)
			kamikaze.KamikazeShooter = true
			if _, ok := AcquireTarget([]Candidate{tc.cand}, kamikaze); !ok {
				t.Fatalf("a `kamikaze` shooter did not acquire a candidate the §3.1 gate refuses; check 3 bypasses that gate entirely [06 §3.2]")
			}
		})
	}
}

// TestKamikazeBypassLeavesTheStunnedRejectionStanding keeps check 3 in its
// place in the order [06 §3.2]: it bypasses the §3.1 physical gate, and
// nothing else. Check 5 — "a paralyzer weapon rejects a candidate already
// carrying the stunned bit" — is a separate check and still runs.
func TestKamikazeBypassLeavesTheStunnedRejectionStanding(t *testing.T) {
	stunned := hostileAt(1, 10, 9)
	stunned.Stunned = true

	r := rng.NewSimulation(1)
	a := base(&r)
	a.KamikazeShooter = true
	a.Paralyzer = true
	if _, ok := AcquireTarget([]Candidate{stunned}, a); ok {
		t.Fatalf("a paralyzer slot on a `kamikaze` shooter acquired an already-stunned candidate; check 5 is not part of the bypassed gate [06 §3.2]")
	}

	ordinary := rng.NewSimulation(1)
	b := base(&ordinary)
	b.KamikazeShooter = true
	if _, ok := AcquireTarget([]Candidate{stunned}, b); !ok {
		t.Fatalf("a non-paralyzer slot refused a stunned candidate; only a paralyzer reads the mark [06 §3.2][06 R-DMG-01 §11]")
	}
}

// TestReactionOfferDoesNotTakeTheKamikazeBypass is the boundary the same
// section draws. Check 3 belongs to the picked-candidate order of the shared
// unit-level target search; the damage-reaction offer's admission is "the §3.1
// acquisition physical gate accepts the attacker for that slot" and nothing
// else [06 R-WPN-04 §2 part 3]. A kamikaze victim must therefore not be handed
// an attacker its slot cannot physically engage.
func TestReactionOfferDoesNotTakeTheKamikazeBypass(t *testing.T) {
	f := newReactionFixture(t)
	f.victim.Def.Kamikaze = true
	installSlotWeapon(f.victim, 0, &content.WeaponDef{ID: 1, Range: 1}) // the attacker is 4 world units away

	if SlotAcquisitionAdmits(f.victim, 0, f.attacker, f.w, nil, nil, nil, nil) {
		t.Fatalf("the reaction offer's §3.1 admission took the picked-candidate order's `kamikaze` bypass [06 R-WPN-04 §2 part 3]")
	}

	// The same gate with a range that reaches admits, so the refusal above is
	// the range clause and not a broken fixture.
	installSlotWeapon(f.victim, 0, &content.WeaponDef{ID: 1, Range: 400})
	if !SlotAcquisitionAdmits(f.victim, 0, f.attacker, f.w, nil, nil, nil, nil) {
		t.Fatalf("the reaction offer refused an attacker inside the slot's range")
	}
	if f.victim.SlotAt(0).Flags&units.SlotFlagEnabled == 0 {
		t.Fatal("fixture slot is not enabled")
	}
}
