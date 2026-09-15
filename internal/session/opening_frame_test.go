package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The authored intro may publish loaded state, but may not advance it or
// replace an already committed frame (DESIGN_GPU_RENDERER §36, I4, I6).
func TestOpeningPublicationPreservesClockRandomAndUnitState(t *testing.T) {
	world := newSessionFixtureWorld(2, nil)
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "commander"}, MaxDamage: 100}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Units: world, Snapshot: frame.NewBuffer(), Clock: &clock.State{Requested: 10, Active: 10}}
	s.SeedSessionRNG(7, 11)
	clockBefore, simBefore, crtBefore := *s.Clock, *s.SimRNG(), *s.CrtRNG()
	x, y, z, hp := world.Unit(h).X, world.Unit(h).Y, world.Unit(h).Z, world.Unit(h).Health
	if !s.PublishOpeningFrame() {
		t.Fatal("no loaded frame")
	}
	cur := s.Snapshot.Current()
	if cur.Tick != 0 || len(cur.Units) != 1 || cur.Units[0].Slot != h {
		t.Fatal("opening did not expose loaded unit")
	}
	if *s.Clock != clockBefore || *s.SimRNG() != simBefore || *s.CrtRNG() != crtBefore {
		t.Fatal("publication advanced scheduler or RNG")
	}
	u := world.Unit(h)
	if u.X != x || u.Y != y || u.Z != z || u.Health != hp {
		t.Fatal("publication changed authoritative unit")
	}
	if !s.PublishOpeningFrame() || s.Snapshot.Current() != cur {
		t.Fatal("opening republished the same tick")
	}
	s.Clock.GlobalTick = 1
	if s.PublishOpeningFrame() {
		t.Fatal("opening admitted a running battle")
	}
}
