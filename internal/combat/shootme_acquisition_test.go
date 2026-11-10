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

	draws := func(control uint8, shootAll bool) uint64 {
		r := rng.NewSimulation(9)
		a := base(&r)
		a.ShooterControlByte = control
		a.ShootAll = shootAll
		before := r.Draws()
		AcquireTarget(population(), a)
		return r.Draws() - before
	}

	// Two samples, of which the bound-one last draws nothing, plus two scores.
	const admittedDraws = 3
	// The same one sampling draw, and no score for either rejected pick.
	const refusedDraws = 1

	if got := draws(ControlByteComputer, false); got != admittedDraws {
		t.Fatalf("computer owner drew %d, want %d [06 §3.2]", got, admittedDraws)
	}
	if got := draws(ControlByteHuman, false); got != refusedDraws {
		t.Fatalf("human owner drew %d, want %d: a check-2 rejection removes the scoring draw [06 §3.2]", got, refusedDraws)
	}
	// Toggling the option bit changes the draw sequence from the next
	// acquisition on [06 §3.2 "The option bit of check 2"]: the human's
	// picks now survive check 2 and take their scoring draws.
	if got := draws(ControlByteHuman, true); got != admittedDraws {
		t.Fatalf("human owner under +ShootAll drew %d, want %d [06 §3.2]", got, admittedDraws)
	}
}

// The service's own search reads its battle-local +ShootAll gate: a fresh
// service starts clear, so a human's slot refuses an unmarked hostile; one
// toggle admits it and a second restores the refusal
// [06 §3.2 "The option bit of check 2"][07 R-CAM-01 §6].
func TestServiceShootAllGateFeedsCheck2(t *testing.T) {
	f := newRegistryFixture(t, false)
	f.onProjectedGrid()
	unmarked := *f.enemy.Def
	unmarked.ShootMe = false
	f.enemy.Def = &unmarked
	f.shooter.InstallWeapon(0, &content.WeaponDef{ID: 99, Range: 1000})
	f.shooter.SlotAt(0).Flags |= 0x02

	s := &Service{}
	s.SetControlByte(func(uint8) uint8 { return ControlByteHuman })
	f.sensorTick(targetRegistryPeriod)
	rebuildEverySlot(s, targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	acquire := func() (pool.Handle, bool) {
		return s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ)
	}

	if h, ok := acquire(); ok {
		t.Fatalf("a fresh service acquired unmarked %d for a human shooter; the gate starts clear [06 §3.2]", h)
	}
	if !s.ToggleShootAll() {
		t.Fatal("the first toggle did not set the gate")
	}
	if h, ok := acquire(); !ok || h != f.enemy.Handle {
		t.Fatalf("with the gate set acquired %d, %v; want the unmarked hostile %d [06 §3.2]", h, ok, f.enemy.Handle)
	}
	if s.ToggleShootAll() {
		t.Fatal("the second toggle did not clear the gate")
	}
	if h, ok := acquire(); ok {
		t.Fatalf("typing it again did not restore check 2: acquired %d [06 §3.2]", h)
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
