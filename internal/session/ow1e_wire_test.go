package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestOW1E_EffectsFromEventsOnePerVisual(t *testing.T) {
	c := presentation.NewCollector(presentation.Limits{})
	if !c.EmitNanolathe(presentation.Event{Tick: 4, Source: 2, Target: 3, Piece: 6, EffectID: 6, X: numeric.Fixed(11 << 16), TargetX: numeric.Fixed(21 << 16)}) {
		t.Fatal("admit nanolathe")
	}
	if !c.EmitExplosion(presentation.Event{Tick: 4, Graphic: "explosion", X: numeric.Fixed(100 << 16)}) {
		t.Fatal("admit explosion")
	}
	if !c.EmitShake(presentation.Event{Tick: 4, Magnitude: 5, Lifetime: 10}) {
		t.Fatal("admit shake")
	}
	if !c.EmitSound(presentation.Event{Tick: 4, Alias: "boom"}) {
		t.Fatal("admit sound")
	}
	s := &Session{Snapshot: &snapshot.Buffer{}, Presentation: c}
	s.publishSnapshot(4)
	_, cur, ok := s.Snapshot.Read()
	if !ok {
		t.Fatal("no frame")
	}
	if len(cur.Events) != 4 {
		t.Fatalf("events %d want 4", len(cur.Events))
	}
	if len(cur.Effects) != 2 {
		t.Fatalf("effects %d want 2 (shake/sound excluded) got %+v", len(cur.Effects), cur.Effects)
	}
	// Nano must preserve selector 6, piece, endpoints.
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
	s.publishSnapshot(5)
	_, nxt, ok := s.Snapshot.Read()
	if !ok {
		t.Fatal("no second frame")
	}
	if len(nxt.Events) != 0 {
		t.Fatalf("events should be one-shot, got %d", len(nxt.Events))
	}
	if len(nxt.Effects) != 0 {
		t.Fatalf("effects should be one-shot without persistence, got %d", len(nxt.Effects))
	}
}

func TestOW1E_EffectsNeverInventArtwork(t *testing.T) {
	c := presentation.NewCollector(presentation.Limits{})
	if !c.EmitExplosion(presentation.Event{Tick: 1, Graphic: ""}) {
		t.Fatal("admit")
	}
	s := &Session{Snapshot: &snapshot.Buffer{}, Presentation: c}
	s.publishSnapshot(1)
	_, cur, _ := s.Snapshot.Read()
	if len(cur.Effects) != 1 {
		t.Fatal("want 1 effect")
	}
	if cur.Effects[0].Graphic != "" {
		t.Fatalf("empty graphic was invented: %q", cur.Effects[0].Graphic)
	}
}

func TestOW1E_NanoReclaimReversedEndpointPreserved(t *testing.T) {
	// Reclaim emits target->builder direction; we preserve whatever the
	// producer supplied as X/TargetX without reinterpreting [R-P0-06].
	c := presentation.NewCollector(presentation.Limits{})
	if !c.EmitNanolathe(presentation.Event{Tick: 2, Source: 3, Target: 9, X: numeric.Fixed(30 << 16), TargetX: numeric.Fixed(10 << 16), EffectID: 6, Mode: 2}) {
		t.Fatal("admit reclaim nano")
	}
	s := &Session{Snapshot: &snapshot.Buffer{}, Presentation: c}
	s.publishSnapshot(2)
	_, cur, _ := s.Snapshot.Read()
	if len(cur.Effects) != 1 || cur.Effects[0].Mode != 2 {
		t.Fatalf("reclaim mode preserved %+v", cur.Effects)
	}
	if cur.Effects[0].X != numeric.Fixed(30<<16) || cur.Effects[0].TargetX != numeric.Fixed(10<<16) {
		t.Fatalf("reclaim endpoints not reversed correctly %+v", cur.Effects[0])
	}
}
