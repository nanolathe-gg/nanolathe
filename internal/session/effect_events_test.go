package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestCommittedEffectsPreserveOrderedVisualEvents(t *testing.T) {
	c := frame.NewEventBuffer(frame.Limits{})
	if !c.EmitNanolathe(frame.Event{Tick: 4, Source: 2, Target: 3, Piece: 6, EffectID: 6, Lifetime: 1, X: numeric.Fixed(11 << 16), TargetX: numeric.Fixed(21 << 16)}) {
		t.Fatal("admit nanolathe")
	}
	if !c.EmitExplosion(frame.Event{Tick: 4, Graphic: "explosion", Lifetime: 1, X: numeric.Fixed(100 << 16)}) {
		t.Fatal("admit explosion")
	}
	if !c.EmitShake(frame.Event{Tick: 4, Magnitude: 5, Lifetime: 10}) {
		t.Fatal("admit shake")
	}
	s := &Session{Snapshot: &frame.Buffer{}, publication: newPublicationState(c)}
	s.publication.effects.Advance(4, c.StagingEvents())
	s.publishSnapshot(4)
	cur := s.Snapshot.Current()
	if cur == nil {
		t.Fatal("no frame")
	}
	if len(cur.Events) != 3 {
		t.Fatalf("events %d want 3 visual cues", len(cur.Events))
	}
	if len(cur.Effects) != 2 {
		t.Fatalf("effects %d want 2 (shake excluded) got %+v", len(cur.Effects), cur.Effects)
	}
	// Nanolathe preserves selector 6, piece, and endpoints [03 §5.5].
	nano := cur.Effects[0]
	if nano.EffectID != 6 || nano.Piece != 6 || nano.Kind != "nanolathe" {
		t.Fatalf("nano effect %+v", nano)
	}
	if nano.X != numeric.Fixed(11<<16) || nano.TargetX != numeric.Fixed(21<<16) {
		t.Fatalf("nano endpoints %+v", nano)
	}
	// Explosion must preserve graphic, never invent.
	exp := cur.Effects[1]
	if exp.Graphic != "explosion" || exp.Kind != "explosion" {
		t.Fatalf("explosion effect %+v", exp)
	}
	if exp.Graphic == "" {
		t.Fatal("empty graphic should stay empty, not invented")
	}
	// Next tick with no events must have empty Effects (one-shot) but Events empty.
	s.publication.effects.Advance(5, nil)
	s.publishSnapshot(5)
	nxt := s.Snapshot.Current()
	if nxt == nil {
		t.Fatal("no second frame")
	}
	if len(nxt.Events) != 0 {
		t.Fatalf("events should be one-shot, got %d", len(nxt.Events))
	}
	if len(nxt.Effects) != 0 {
		t.Fatalf("effects should be one-shot without persistence, got %d", len(nxt.Effects))
	}
}

func TestCommittedEffectsPreserveMissingArtwork(t *testing.T) {
	c := frame.NewEventBuffer(frame.Limits{})
	if !c.EmitExplosion(frame.Event{Tick: 1, Graphic: ""}) {
		t.Fatal("admit")
	}
	s := &Session{Snapshot: &frame.Buffer{}, publication: newPublicationState(c)}
	s.publication.effects.Advance(1, c.StagingEvents())
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if len(cur.Effects) != 1 {
		t.Fatal("want 1 effect")
	}
	if cur.Effects[0].Graphic != "" {
		t.Fatalf("empty graphic was invented: %q", cur.Effects[0].Graphic)
	}
}

func TestCommittedNanolatheEffectsPreserveProducerEndpoints(t *testing.T) {
	// Reclaim supplies the target-to-builder direction; publication preserves
	// the producer's X/TargetX values without reinterpretation [03 §5.5].
	c := frame.NewEventBuffer(frame.Limits{})
	if !c.EmitNanolathe(frame.Event{Tick: 2, Source: 3, Target: 9, X: numeric.Fixed(30 << 16), TargetX: numeric.Fixed(10 << 16), EffectID: 6, Mode: 2}) {
		t.Fatal("admit reclaim nano")
	}
	s := &Session{Snapshot: &frame.Buffer{}, publication: newPublicationState(c)}
	s.publication.effects.Advance(2, c.StagingEvents())
	s.publishSnapshot(2)
	cur := s.Snapshot.Current()
	if len(cur.Effects) != 1 || cur.Effects[0].Mode != 2 {
		t.Fatalf("reclaim mode preserved %+v", cur.Effects)
	}
	if cur.Effects[0].X != numeric.Fixed(30<<16) || cur.Effects[0].TargetX != numeric.Fixed(10<<16) {
		t.Fatalf("reclaim endpoints not reversed correctly %+v", cur.Effects[0])
	}
}
