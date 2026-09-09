package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// statusCueFixture is a bare session with the publication boundary and the
// status-cue seam installed on one unit. It deliberately binds no audio
// service: the raise path must never touch a backend [I6].
func statusCueFixture(owner uint8, alive, dying bool) (*Session, *units.Unit) {
	s := &Session{
		Clock:       &clock.State{GlobalTick: 41},
		publication: newPublicationState(frame.NewEventBuffer(frame.Limits{})),
	}
	s.LocalOwner = 0
	u := &units.Unit{
		Handle: pool.Handle(1),
		Owner:  owner,
		Alive:  alive,
		Dying:  dying,
		Def:    &content.UnitDef{UnitName: "armpw", OnOffable: true},
	}
	u.SetStatusCueSink(s.raiseStatusCue)
	return s, u
}

// statusEvents returns the committed status events carrying one cue code.
func statusEvents(s *Session, code uint8) []frame.EventView {
	var out []frame.EventView
	for _, e := range s.publication.events.SnapshotEvents() {
		if e.Kind == frame.EventKindStatus && e.StatusKind == code {
			out = append(out, e)
		}
	}
	return out
}

// TestCloakEdgeRaisesSlotFourteenForTheViewSlot locks the three clauses of the
// raise helper's gate and the caption it resolves [03 R-AUD-01 §7].
//
// The code IS the slot index: bit 2 newly set raises slot 14 `cloak`, whose
// static default caption is `Cloaked` [03 §8.3]. The edge machine passes no
// override, so the slot default is what the entry carries. When the gate fails
// the helper returns having touched nothing — no queue write, no allocation.
func TestCloakEdgeRaisesSlotFourteenForTheViewSlot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner uint8
		alive bool
		dying bool
		want  int
	}{
		{"view slot, alive, latch clear", 0, true, false, 1},
		{"another player's unit", 1, true, false, 0},
		{"death latch set", 0, true, true, 0},
		{"alive bit clear", 0, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, u := statusCueFixture(tc.owner, tc.alive, tc.dying)
			u.SetCloakedInstance(true)
			got := statusEvents(s, units.StatusCueCloak)
			if len(got) != tc.want {
				t.Fatalf("committed slot-14 cues = %d, want %d [03 R-AUD-01 §7]", len(got), tc.want)
			}
			if tc.want == 0 {
				return
			}
			if got[0].StatusText != "Cloaked" {
				t.Fatalf("caption = %q, want %q [03 §8.3 static slot table]", got[0].StatusText, "Cloaked")
			}
			if got[0].Source != u.Handle {
				t.Fatalf("cue unit = %d, want %d", got[0].Source, u.Handle)
			}
			if got[0].Tick != s.Clock.GlobalTick {
				t.Fatalf("cue tick = %d, want the global tick %d [03 R-AUD-01 §7]", got[0].Tick, s.Clock.GlobalTick)
			}
		})
	}
}

// TestUncloakEdgeRaisesSlotFifteen locks the falling edge's code and caption,
// and that an unchanged write raises nothing: the change test is the edge
// [03 R-AUD-01 §7][05 R-ECO-01 §8].
func TestUncloakEdgeRaisesSlotFifteen(t *testing.T) {
	s, u := statusCueFixture(0, true, false)
	u.SetCloakedInstance(true)
	u.SetCloakedInstance(false)
	got := statusEvents(s, units.StatusCueUncloak)
	if len(got) != 1 {
		t.Fatalf("committed slot-15 cues = %d, want 1 [03 R-AUD-01 §7]", len(got))
	}
	if got[0].StatusText != "Visible" {
		t.Fatalf("caption = %q, want %q [03 §8.3 static slot table]", got[0].StatusText, "Visible")
	}
	// A pass that leaves the byte unchanged raises nothing (§7 acceptance).
	u.SetCloakedInstance(false)
	if got := statusEvents(s, units.StatusCueUncloak); len(got) != 1 {
		t.Fatalf("an unchanged write raised a second cue: %d [05 R-ECO-01 §8]", len(got))
	}
}

// TestActivationEdgesRaiseSlotsThreeAndFour locks codes 3/4 and their empty
// default caption. Slots 3 `activate` and 4 `deactivate` carry no default
// speech, so the entry text is empty and the resolve prints nothing unless the
// category authored a per-variant caption [03 R-AUD-01 §7][03 §8.3].
func TestActivationEdgesRaiseSlotsThreeAndFour(t *testing.T) {
	s, u := statusCueFixture(0, true, false)
	u.SetActivationEdge(true)
	rise := statusEvents(s, units.StatusCueActivate)
	if len(rise) != 1 {
		t.Fatalf("committed slot-3 cues = %d, want 1 [03 R-AUD-01 §7]", len(rise))
	}
	if rise[0].StatusText != "" {
		t.Fatalf("slot 3 caption = %q, want empty [03 §8.3 static slot table]", rise[0].StatusText)
	}
	u.SetActivationEdge(false)
	if fall := statusEvents(s, units.StatusCueDeactivate); len(fall) != 1 {
		t.Fatalf("committed slot-4 cues = %d, want 1 [03 R-AUD-01 §7]", len(fall))
	}
	// Only an actual change is an edge [04 R-UNIT-06 §2].
	u.SetActivationEdge(false)
	if fall := statusEvents(s, units.StatusCueDeactivate); len(fall) != 1 {
		t.Fatalf("an unchanged activation write raised a second cue: %d", len(fall))
	}
}

// TestTeardownPurgeDropsTheRemovedUnitsCues locks the queue's one other
// simulation-side interaction: when a unit is removed, every queued entry whose
// unit is that unit is dropped, and no other unit's entry is touched
// [03 R-AUD-01 §7].
func TestTeardownPurgeDropsTheRemovedUnitsCues(t *testing.T) {
	s, u := statusCueFixture(0, true, false)
	other := &units.Unit{
		Handle: pool.Handle(2),
		Owner:  0,
		Alive:  true,
		Def:    &content.UnitDef{UnitName: "armpw", OnOffable: true},
	}
	other.SetStatusCueSink(s.raiseStatusCue)

	u.SetCloakedInstance(true)
	other.SetActivationEdge(true)
	if len(statusEvents(s, units.StatusCueCloak)) != 1 || len(statusEvents(s, units.StatusCueActivate)) != 1 {
		t.Fatal("fixture did not queue one cue per unit")
	}

	s.purgeStatusCues(u.Handle)
	if got := statusEvents(s, units.StatusCueCloak); len(got) != 0 {
		t.Fatalf("the teardown purge left %d entries for the removed unit [03 R-AUD-01 §7]", len(got))
	}
	if got := statusEvents(s, units.StatusCueActivate); len(got) != 1 {
		t.Fatalf("the teardown purge dropped another unit's entry: %d remain [03 R-AUD-01 §7]", len(got))
	}
}

// TestStatusCueRaisePathDrawsFromNeitherStream locks §7's "Random draws: none
// on the raise path": the gate, the caption lookup and the insert draw from
// neither the simulation stream nor the CRT stream. The only draw anywhere in
// the cue path is the CRT variant pick at resolve time, once per pop, on the
// presentation side [03 §8.3][I4].
func TestStatusCueRaisePathDrawsFromNeitherStream(t *testing.T) {
	s, u := statusCueFixture(0, true, false)
	sim, crt := s.SimRNG(), s.CrtRNG()
	simBefore, crtBefore := sim.Draws(), crt.Draws()

	u.SetCloakedInstance(true)
	u.SetCloakedInstance(false)
	u.SetActivationEdge(true)
	u.SetActivationEdge(false)
	// A gate failure must be free too: no queue write, no allocation, no draw.
	foreign := &units.Unit{Handle: pool.Handle(3), Owner: 4, Alive: true}
	foreign.SetStatusCueSink(s.raiseStatusCue)
	foreign.SetCloakedInstance(true)

	if got := sim.Draws() - simBefore; got != 0 {
		t.Fatalf("the raise path advanced the simulation stream %d times [03 R-AUD-01 §7][I4]", got)
	}
	if got := crt.Draws() - crtBefore; got != 0 {
		t.Fatalf("the raise path advanced the CRT stream %d times [03 R-AUD-01 §7][I4]", got)
	}
}
