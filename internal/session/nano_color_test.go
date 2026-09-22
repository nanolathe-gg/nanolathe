package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"reflect"
	"testing"
)

// Colour metadata is captured from the work owner's player record, including
// reverse spray, and cannot change particles or the shared CRT stream.
func TestNanoOwnerColourIsCapturedWithoutChangingParticles(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		a, b := nanoSegmentFixture(t), nanoSegmentFixture(t)
		a.Econ = &economy.Service{}
		a.Econ.Players[2].Exists = true
		a.Econ.Players[2].Logo = 7
		e := buildSegmentEvent()
		e.Team = 2
		e.NanolatheBoxAtSource = reverse
		a.appendStripNanoForEvent(e)
		b.appendStripNanoForEvent(e)
		if a.CrtRNG().State != b.CrtRNG().State || !reflect.DeepEqual(a.strips.strips[6][0].particles, b.strips.strips[6][0].particles) {
			t.Fatal("metadata changed emission")
		}
		a.Econ.Players[2].Logo = 3
		a.Econ.Players[2].Exists = false
		a.strips.sweepStrip(6, 1, a.CrtRNG(), nil, 0, nil)
		b.strips.sweepStrip(6, 1, b.CrtRNG(), nil, 0, nil)
		if a.CrtRNG().State != b.CrtRNG().State || !reflect.DeepEqual(a.strips.strips[6][0].particles, b.strips.strips[6][0].particles) {
			t.Fatal("metadata changed sweep")
		}
		views := a.appendStripViews(0, nil)
		if len(views) == 0 {
			t.Fatal("missing particles")
		}
		for _, v := range views {
			if !v.NanoOwnerColorKnown || v.NanoOwnerColor != 7 || v.Fill < 0xa1 || v.Fill > 0xa7 {
				t.Fatalf("published %+v", v)
			}
		}
		for _, v := range b.appendStripViews(0, nil) {
			if v.NanoOwnerColorKnown {
				t.Fatal("unknown owner was invented")
			}
		}
		// Publication copies are values, detached from later owner mutations.
		first := append([]frame.StripView(nil), views...)
		a.appendStripNanoForEvent(e)
		if !reflect.DeepEqual(first, views) {
			t.Fatal("publication changed")
		}
	}
}

func TestScriptNanoUsesSourceOwner(t *testing.T) {
	s, sink, src, dst := emitSFXVectorFixture(t)
	s.Econ = &economy.Service{}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].Logo = 1
	s.Econ.Players[2].Exists = true
	s.Econ.Players[2].Logo = 7
	s.Units.Unit(sink.source).Owner = 2
	s.publication = newPublicationState(frame.NewEventBuffer(frame.Limits{}))
	sink.publication = s.publication
	sink.SetCOBPieceMap([]int{0})
	emit := func() {
		sink.EmitCOBEvent(cob.PresentationEvent{Kind: cob.PresentationNano, Piece: 0, Source: src, Target: dst})
	}
	emit()
	views := s.appendStripViews(0, nil)
	if len(views) == 0 || !views[0].NanoOwnerColorKnown || views[0].NanoOwnerColor != 7 {
		t.Fatalf("script source colour lost: %+v", views)
	}
	// An unresolved source must not borrow player zero's colour.
	sink.source = 0
	emit()
	last := s.strips.strips[6][len(s.strips.strips[6])-1]
	if last.nanoOwnerColorKnown {
		t.Fatal("missing source borrowed player zero")
	}
}
