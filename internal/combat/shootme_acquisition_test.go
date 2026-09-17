package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Check 2 of the picked-candidate order [06 §3.2]: "one definition flag of the
// candidate, or the shooter's owning player is a computer controller, or one
// global option bit — any of the three admits the candidate." The flag is
// `shootme` ([04 R-SPEC-01 §5]); the controller term is the owning player row's
// control byte reading exactly 2; the option bit is the session mode-flags
// word's bit 10, whose one retail writer is the `+ShootAll` chat command and
// which is clear in a stock session
// ([06 §3.2 "The option bit of check 2"][07 R-CAM-01 §6]).
//
// The stock consequence the check exists for: the 91 stock definitions that
// omit `shootme` are the non-combat buildings, so a HUMAN player's units do not
// autonomously pick the enemy's factories, power, metal, radar, storage, mines
// or walls, while a computer player's units pick everything.

// unmarkedCandidate is a hostile in range and above sea level that authors no
// `shootme`. Every other check of the picked-candidate order admits it, so the
// verdict below is check 2's alone.
func unmarkedCandidate() Candidate {
	return Candidate{Handle: 7, X: fixed(20), Z: 0, Y: fixed(10), Hostile: true}
}

func TestCheck2AdmitsOnAnyOfItsThreeDisjuncts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		control uint8
		shootMe bool
		shootID bool // the +ShootAll option bit
		want    bool
	}{
		{"a human shooter refuses an unmarked candidate", ControlByteHuman, false, false, false},
		{"an unoccupied player row refuses it too", ControlByteAbsent, false, false, false},
		{"a remote peer refuses it: only the exact value 2 admits", ControlByteRemote, false, false, false},
		{"`shootme` admits it under a human", ControlByteHuman, true, false, true},
		{"a computer owner admits it unmarked", ControlByteComputer, false, false, true},
		{"the +ShootAll bit admits it under a human", ControlByteHuman, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rng.NewSimulation(1)
			a := base(&r)
			a.ShooterControlByte = tc.control
			a.ShootAll = tc.shootID
			c := unmarkedCandidate()
			c.ShootMe = tc.shootMe
			h, ok := AcquireTarget([]Candidate{c}, a)
			if ok != tc.want {
				t.Fatalf("acquired=%v want %v [06 §3.2] check 2", ok, tc.want)
			}
			if tc.want && h != c.Handle {
				t.Fatalf("acquired %d want %d", h, c.Handle)
			}
		})
	}
}

// A check-2 rejection costs the candidate its SCORING draw but not its sampling
// draw: the sample is taken before the five checks and the score only after all
// five [06 §3.2 "Draw consequence"]. This is the determinism half of the
// contract — the same population scans to a different stream position under a
// human owner than under a computer one.
func TestCheck2RejectionSpendsSamplingButNotScoringDraws(t *testing.T) {
	// Two candidates at different distances, both with a scoring bound of at
	// least two, so every survivor draws exactly once for its score.
	population := func() []Candidate {
		return []Candidate{
			{Handle: 1, X: fixed(20), Z: 0, Y: fixed(10), Hostile: true},
			{Handle: 2, X: fixed(40), Z: 0, Y: fixed(10), Hostile: true},
		}
	}

	draws := func(control uint8) uint64 {
		r := rng.NewSimulation(9)
		a := base(&r)
		a.ShooterControlByte = control
		before := r.Draws()
		AcquireTarget(population(), a)
		return r.Draws() - before
	}

	// Two samples, of which the bound-one last draws nothing, plus two scores.
	const admittedDraws = 3
	// The same one sampling draw, and no score for either rejected pick.
	const refusedDraws = 1

	if got := draws(ControlByteComputer); got != admittedDraws {
		t.Fatalf("computer owner drew %d, want %d [06 §3.2]", got, admittedDraws)
	}
	if got := draws(ControlByteHuman); got != refusedDraws {
		t.Fatalf("human owner drew %d, want %d: a check-2 rejection removes the scoring draw [06 §3.2]", got, refusedDraws)
	}
}

// The reaction offer's admission is the §3.1 physical gate ALONE
// [06 R-WPN-04 §2 part 3]: the picked-candidate order's checks 2 and 3 belong
// to the unit-level target search, not to the gate routine. An unmarked
// candidate a human's slot cannot autonomously acquire is still a legal
// retaliation target.
func TestSlotAcquisitionAdmitsIgnoresCheck2(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	shooter.InstallWeapon(0, weaponTurret(77))

	unmarked := *target.Def
	unmarked.ShootMe = false
	target.Def = &unmarked

	if !SlotAcquisitionAdmits(shooter, 0, target, w, nil, terrain, nil, nil) {
		t.Fatal("the reaction offer refused a candidate the §3.1 gate admits; check 2 is not its business [06 R-WPN-04 §2 part 3]")
	}
}

// The candidate-side operand comes from the definition, and a candidate with no
// definition reads as not authoring the key — the same answer the absent key
// gives [04 R-SPEC-01 §5].
func TestCandidateShootMeComesFromTheDefinition(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  *content.UnitDef
		want bool
	}{
		{"authored", &content.UnitDef{UnitName: "marked", Limit: -1, ShootMe: true}, true},
		{"omitted", &content.UnitDef{UnitName: "plain", Limit: -1}, false},
		{"no definition", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shooter := &units.Unit{Handle: pool.Handle(1), Def: &content.UnitDef{UnitName: "shooter", Limit: -1}}
			cand := &units.Unit{Handle: pool.Handle(2), Def: tc.def}
			if got := acquisitionCandidate(shooter, cand, 0, 0, nil).ShootMe; got != tc.want {
				t.Fatalf("ShootMe=%v want %v [06 §3.2][04 R-SPEC-01 §5]", got, tc.want)
			}
		})
	}
}
