package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestPhase2PlayerGateAdmitsOneTwoAndThree locks "The player gate" of
// [04 R-MOV-03 §1] as [06 R-DMG-01 §8] restates it: the sweep processes a slot
// whose record exists and whose control byte is 1, 2 or 3, and inside the visit
// a narrower test admits the water damage, self-repair, order pumps, mover tick
// and post-move correction for control bytes 1 and 2 only. A row with no record
// is not swept at all.
func TestPhase2PlayerGateAdmitsOneTwoAndThree(t *testing.T) {
	s := &Session{Econ: &economy.Service{}}
	s.Econ.Players[0] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	s.Econ.Players[1] = economy.Player{Exists: true, ControllerState: combat.ControlByteComputer}
	s.Econ.Players[2] = economy.Player{Exists: true, ControllerState: combat.ControlByteRemote}
	s.Econ.Players[3] = economy.Player{Exists: true, ControllerState: 0}
	// Player 4 keeps its zero value: no record at all.

	for _, tc := range []struct {
		owner       uint8
		visit, work bool
		why         string
	}{
		{0, true, true, "a locally controlled human is swept and does the work"},
		{1, true, true, "a computer player is swept and does the work"},
		{2, true, false, "a remote peer is swept but skips the 1-or-2 block"},
		{3, false, false, "a control byte outside 1..3 is not swept"},
		{4, false, false, "a row with no record is not swept"},
		{9, false, false, "an unoccupied row is not swept"},
		{10, false, false, "a row past the ten records is not swept"},
	} {
		visit, work := s.sweepPlayerGate(tc.owner)
		if visit != tc.visit || work != tc.work {
			t.Fatalf("owner %d gate = (%v,%v), want (%v,%v): %s", tc.owner, visit, work, tc.visit, tc.work, tc.why)
		}
	}
}

// TestSingleplayerSlotsAreAllOneOrTwo is the assertion the parity note asks for
// rather than assumes: a composed single-player battle occupies its rows only
// with control bytes 1 and 2, so the gate above changes nothing in the shipped
// configuration [06 R-DMG-01 §8].
func TestSingleplayerSlotsAreAllOneOrTwo(t *testing.T) {
	s := &Session{Econ: &economy.Service{}}
	s.Econ.Players[0] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	s.Econ.Players[1] = economy.Player{Exists: true, ControllerState: combat.ControlByteComputer}
	for i := range s.Econ.Players {
		p := &s.Econ.Players[i]
		if !p.Exists {
			continue
		}
		visit, work := s.sweepPlayerGate(uint8(i))
		if !visit || !work {
			t.Fatalf("occupied single-player row %d (control byte %d) fails the sweep gate", i, p.ControllerState)
		}
	}
}

// TestPhase2SweepSkipsUngatedOwnersInOrder drives the real phase-2 sweep. The
// slot-end death handler of [04 R-MOV-03 §1] step 10 sits inside the visit, so
// a unit the gate refuses is never finalized; the order the finalized units
// arrive in is the sweep's own players-ascending, slots-ascending order (I1),
// which the gate filters but never reorders.
func TestPhase2SweepSkipsUngatedOwnersInOrder(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "gatetest"},
		UnitName:         "gatetest",
		MaxDamage:        100,
		Script:           fixtureCOBProgram(),
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newSessionFixtureWorld(4, cat)

	s := &Session{Units: w, Catalog: cat, Econ: &economy.Service{}}
	s.Econ.Players[0] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	s.Econ.Players[1] = economy.Player{Exists: true, ControllerState: combat.ControlByteComputer}
	s.Econ.Players[2] = economy.Player{Exists: true, ControllerState: combat.ControlByteRemote}
	// Player 3 has no record.

	want := make([]pool.Handle, 0, 2)
	for owner := uint8(0); owner < 4; owner++ {
		h, err := w.Create(def, owner, 0, 0, 0)
		if err != nil {
			t.Fatalf("create for owner %d: %v", owner, err)
		}
		u := w.Unit(h)
		u.Dying = true // the visit's step 10 finalizes it
		u.DeathCause = units.DeathKilled
		// Control bytes 1, 2 and 3 are all swept; only the row with no record
		// is refused.
		if owner <= 2 {
			want = append(want, h)
		}
	}

	var visited []pool.Handle
	w.OnDeath = func(h pool.Handle, _ units.DeathCause, _ *units.Unit) { visited = append(visited, h) }
	s.stepUnitPhase(1)

	if len(visited) != len(want) {
		t.Fatalf("sweep finalized %v, want exactly the control-byte 1/2 owners %v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Fatalf("sweep order %v, want ascending %v", visited, want)
		}
	}
}
