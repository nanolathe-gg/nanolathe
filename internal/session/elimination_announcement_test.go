package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// eliminationAnnouncementSession builds the smallest session the phase-2 death
// finalizer will run on: a unit world, a player table, a committed-event
// window and a seeded CRT stream. missionType selects the session kind gate.
func eliminationAnnouncementSession(t *testing.T, missionType mission.Type) (*Session, *units.World, *content.UnitDef) {
	t.Helper()
	w, def := eliminationFixtureWorld(t)
	s := &Session{
		Units:       w,
		Econ:        &economy.Service{},
		Mission:     &mission.Mission{Type: missionType},
		publication: newPublicationState(nil),
	}
	s.SeedSessionRNG(1, 1)
	for i := 0; i < 2; i++ {
		s.Econ.Players[i] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	}
	s.Econ.Players[1].Name = "Vermin"
	// Two units for owner 1, so the first death is not an elimination and the
	// second one is.
	for i := 0; i < 2; i++ {
		if _, err := w.Create(def, 1, 0, 0, 0); err != nil {
			t.Fatalf("create %d for owner 1: %v", i, err)
		}
	}
	return s, w, def
}

// killOneOwnerUnit finalizes one live unit of owner through the phase-2 death
// path and reports the CRT draws that death spent.
func killOneOwnerUnit(t *testing.T, s *Session, w *units.World, owner uint8, tick uint32) uint64 {
	t.Helper()
	var doomed pool.Handle
	for _, u := range w.IterSliced() { // pool slot ascending (I1)
		if u != nil && u.Alive && u.Owner == owner {
			doomed = u.Handle
			break
		}
	}
	if doomed == 0 {
		t.Fatalf("owner %d has no live unit left to kill", owner)
	}
	w.Unit(doomed).Dying = true
	before := s.CrtRNG().Draws()
	s.finalizePhase2Death(doomed, tick)
	return s.CrtRNG().Draws() - before
}

func announceEvents(s *Session) []frame.Event {
	var out []frame.Event
	for _, e := range s.publication.events.StagingEvents() {
		if e.Kind == frame.KindAnnounce {
			out = append(out, e)
		}
	}
	return out
}

// TestEliminationAnnouncementSpendsOneCRTDrawAtTheDeath locks [08 R-CAMP-01 §9]
// and the census row of [01 §7.5] "The elimination announcement": the skirmish
// elimination line is chosen by one CRT draw taken modulo three, the draw
// happens inside the tick on the death path at the moment the owner's live-unit
// count reaches zero, and the simulation stream is not touched.
//
// The draw's POSITION is the contract, not its value: the CRT stream is also
// authoritative for the wind interval, the meteor scheduler and the
// victory-timer arm, so a missing or extra draw here shifts all three
// [01 §7.5][01 §7.7].
func TestEliminationAnnouncementSpendsOneCRTDrawAtTheDeath(t *testing.T) {
	s, w, _ := eliminationAnnouncementSession(t, mission.TypeSkirmish)

	simBefore := s.SimRNG().Draws()

	// First death: owner 1 still has a unit, so the branch is not entered and
	// no draw is spent.
	if drew := killOneOwnerUnit(t, s, w, 1, 10); drew != 0 {
		t.Fatalf("a death that does not empty the owner spent %d CRT draws, want 0 [08 R-CAMP-01 §9]", drew)
	}
	if got := len(announceEvents(s)); got != 0 {
		t.Fatalf("a non-eliminating death published %d announcements, want 0", got)
	}

	// The value the elimination is about to draw, predicted from a copy of the
	// stream as it stands immediately before the death.
	predict := rng.CRTFromState(s.CrtRNG().State)
	wantTail := eliminationTails[predict.Uint32n(3)]

	// Second death: the live count reaches zero, so exactly one CRT draw is
	// spent, inside finalizePhase2Death.
	if drew := killOneOwnerUnit(t, s, w, 1, 11); drew != 1 {
		t.Fatalf("the elimination spent %d CRT draws, want exactly 1 [01 §7.5]", drew)
	}
	if got := s.SimRNG().Draws(); got != simBefore {
		t.Fatalf("the elimination advanced the simulation stream by %d draws, want 0 [08 R-CAMP-01 §9]", got-simBefore)
	}

	events := announceEvents(s)
	if len(events) != 1 {
		t.Fatalf("the elimination published %d announcements, want exactly 1", len(events))
	}
	got := events[0]
	if want := fmt.Sprintf("%s %s", "Vermin", wantTail); got.StatusText != want {
		t.Fatalf("elimination line %q, want %q: the line is sprintf(\"%%s %%s\") of the owner's name and the drawn tail [08 R-CAMP-01 §9]", got.StatusText, want)
	}
	if got.StatusClass != 4 {
		t.Fatalf("elimination line class %d, want 4 [08 R-CAMP-01 §9]", got.StatusClass)
	}
	if got.AnnounceSlot != 1 {
		t.Fatalf("elimination line attributed to slot %d, want the owner's slot 1 [08 R-CAMP-01 §9]", got.AnnounceSlot)
	}
	if got.Tick != 11 {
		t.Fatalf("elimination line stamped tick %d, want the death's tick 11", got.Tick)
	}
}

// TestEliminationAnnouncementIsSkirmishOnly locks the session-kind gate of
// [08 R-CAMP-01 §9]: "Campaign sessions post nothing." A campaign elimination
// must therefore leave the CRT stream exactly where it was, or every later CRT
// consumer in a campaign battle would shift.
func TestEliminationAnnouncementIsSkirmishOnly(t *testing.T) {
	s, w, _ := eliminationAnnouncementSession(t, mission.TypeCampaign)
	_ = killOneOwnerUnit(t, s, w, 1, 10)
	if drew := killOneOwnerUnit(t, s, w, 1, 11); drew != 0 {
		t.Fatalf("a campaign elimination spent %d CRT draws, want 0 [08 R-CAMP-01 §9]", drew)
	}
	if got := len(announceEvents(s)); got != 0 {
		t.Fatalf("a campaign elimination published %d announcements, want 0", got)
	}
}

// TestEliminationAnnouncementRepeatsOnASecondEmptying locks the absence of a
// once-only latch. Retail has no elimination flag: the test is the value of the
// counter the death handler has just decremented [05 R-SHARE-01 §3], so a slot
// given a unit after its last one died announces again when that one dies. The
// second draw's position in the stream matters as much as the first's.
func TestEliminationAnnouncementRepeatsOnASecondEmptying(t *testing.T) {
	s, w, def := eliminationAnnouncementSession(t, mission.TypeSkirmish)
	_ = killOneOwnerUnit(t, s, w, 1, 10)
	if drew := killOneOwnerUnit(t, s, w, 1, 11); drew != 1 {
		t.Fatalf("the first elimination spent %d CRT draws, want 1", drew)
	}
	if _, err := w.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatalf("re-seating owner 1: %v", err)
	}
	if drew := killOneOwnerUnit(t, s, w, 1, 12); drew != 1 {
		t.Fatalf("the second emptying spent %d CRT draws, want 1 [08 R-CAMP-01 §9]", drew)
	}
	if got := len(announceEvents(s)); got != 2 {
		t.Fatalf("two emptyings published %d announcements, want 2", got)
	}
}
