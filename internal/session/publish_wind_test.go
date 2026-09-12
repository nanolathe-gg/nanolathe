package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Publication copies the raw committed wind; it neither redraws nor normalizes
// it, and slot reuse cannot carry wind into a replacement session [01 §7.3][I6].
func TestPublishWindOwnsValuesAndClearsReusedSlots(t *testing.T) {
	wind := &world.Wind{Heading: 49152, Strength: 7250}
	s := &Session{Snapshot: frame.NewBuffer(), Wind: wind}
	s.publishSnapshot(1)
	first := s.Snapshot.Current()
	want := frame.WindView{Heading: wind.Heading, Strength: wind.Strength}
	if first == nil || first.Wind != want {
		t.Fatalf("published wind = %+v, want %+v", first, want)
	}
	wind.Heading, wind.Strength = 8192, 130
	if first.Wind != want {
		t.Fatal("live wind mutation reached a committed frame")
	}
	s.publishSnapshot(2)
	if got := s.Snapshot.Current().Wind; got != (frame.WindView{Heading: 8192, Strength: 130}) {
		t.Fatalf("next wind publication = %+v", got)
	}
	if first.Wind != want {
		t.Fatal("next publication changed the preceding committed wind")
	}
	s.Wind = nil
	for tick := uint32(3); tick <= 5; tick++ {
		s.publishSnapshot(tick)
		if got := s.Snapshot.Current().Wind; got != (frame.WindView{}) {
			t.Fatalf("nil wind retained slot contents at tick %d: %+v", tick, got)
		}
	}
	f := frame.Frame{Wind: want}
	f.Reset()
	if f.Wind != (frame.WindView{}) {
		t.Fatalf("Reset retained wind: %+v", f.Wind)
	}
}

// The published draft and model maximum retain their different units so an
// Enhanced surface test can compare the hull top without guessing its draft
// [04 R-MOV-01 §9][I6].
func TestPublishWaterlineAndModelTopRetainDefinitionUnits(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "ship"},
		MaxDamage:        1, Floater: true, Waterline: 7,
		ModelTopFixed: 12<<16 + 32768,
	}
	w := newSessionFixtureWorld(2, nil)
	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	s := &Session{Snapshot: frame.NewBuffer(), Units: w}
	s.publishSnapshot(1)
	got := &s.Snapshot.Current().Units[0]
	if got.Waterline != def.Waterline || got.HullYExtent != numeric.Fixed(def.ModelTopFixed) {
		t.Fatalf("published draft/top = %d/%d, want %d/%d", got.Waterline, got.HullYExtent, def.Waterline, def.ModelTopFixed)
	}
	def.Waterline, def.ModelTopFixed = 1, 1
	if got.Waterline != 7 || got.HullYExtent != numeric.Fixed(12<<16+32768) {
		t.Fatal("definition mutation changed committed surface operands")
	}
}
