package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestStatusProducerGateHasThreeClauses locks the producer gate every order
// acknowledgement passes through — including the `arrived` cue (kind 6) the
// three move rows raise [04 R-ORD-01 §1][03 R-AUD-01 §3]. The gate is three
// clauses, all of which must hold: the unit's owner is the local viewing
// player, the unit is alive, and it is not dying. Any one of them failing
// admits nothing to the committed frame, so nothing reaches the audio queue or
// the message ring.
//
// The gate is authoritative state read at a presentation boundary; the cue
// itself is never played here. Sim records, presentation drains [I6].
func TestStatusProducerGateHasThreeClauses(t *testing.T) {
	const arrivedKind uint8 = 6
	for _, tc := range []struct {
		name  string
		owner uint8
		alive bool
		dying bool
		want  bool
	}{
		{"local, alive, not dying", 0, true, false, true},
		{"another player's unit", 1, true, false, false},
		{"dead unit", 0, false, false, false},
		{"dying unit", 0, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{
				Clock:       &clock.State{GlobalTick: 7},
				publication: newPublicationState(frame.NewEventBuffer(frame.Limits{}), 0),
			}
			s.LocalOwner = 0
			if got := int(s.ViewingOwner); got != 0 {
				t.Fatalf("local player = %d, want 0", got)
			}
			binding := s.newOrderBinding()
			if binding == nil || binding.Presentation == nil || binding.Presentation.Status == nil {
				t.Fatal("composition did not supply the presentation status port")
			}
			u := &units.Unit{
				Handle: pool.Handle(1),
				Owner:  tc.owner,
				Alive:  tc.alive,
				Dying:  tc.dying,
				Def:    &content.UnitDef{UnitName: "armpw"},
			}
			got := binding.Presentation.Status(u, arrivedKind, "Arrived")
			if got != tc.want {
				t.Fatalf("status admitted = %v, want %v [04 R-ORD-01 §1][03 R-AUD-01 §3]", got, tc.want)
			}
			events := s.publication.events.SnapshotEvents()
			admitted := 0
			for _, e := range events {
				if e.Kind == frame.EventKindStatus && e.StatusKind == arrivedKind {
					admitted++
				}
			}
			want := 0
			if tc.want {
				want = 1
			}
			if admitted != want {
				t.Fatalf("committed kind-6 status events = %d, want %d", admitted, want)
			}
		})
	}
}

// TestArrivedStatusIsNotAPlayedSoundAtTheProducer locks the publication
// boundary for this cue: the producer admits a value-only status event and
// touches no audio backend. The play happens when the presentation edge drains
// the committed frame [I6][03 §8.3].
func TestArrivedStatusIsNotAPlayedSoundAtTheProducer(t *testing.T) {
	s := &Session{
		Clock:       &clock.State{GlobalTick: 7},
		publication: newPublicationState(frame.NewEventBuffer(frame.Limits{}), 0),
	}
	if s.Audio != nil {
		t.Fatal("fixture bound an audio service; the producer must not need one")
	}
	u := &units.Unit{Handle: pool.Handle(1), Alive: true, Def: &content.UnitDef{UnitName: "armpw"}}
	if !s.newOrderBinding().Presentation.Status(u, 6, "Arrived") {
		t.Fatal("the status port refused a local, live unit with no audio service bound")
	}
}
