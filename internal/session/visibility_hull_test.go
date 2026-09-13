package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// A northwest-only lit corner and a base below water exercise the two inputs
// lost when a direct query used the draw point [06 §3.1][03 §3.2].
func TestVisibilityHullOriginAndPublication(t *testing.T) {
	s := visibilityFixture(t, true)
	s.Vis.SetLocal(1)
	s.World.SeaLevel = 20
	def := &content.UnitDef{UnitName: "hull", MaxDamage: 100, FootprintX: 3, FootprintZ: 5, ModelTopFixed: 21 << 16}
	h, err := s.Units.Create(def, 0, 64*numeric.FixedOne, 0, 96*numeric.FixedOne)
	if err != nil {
		t.Fatal(err)
	}
	u := s.Units.Unit(h)
	u.Flags &^= visibility.SonarBit
	// First probe (40,21,56) projects to (1,1); draw base projects to (2,3).
	s.Vis.ByteGrid(1)[1*int(s.Vis.W)+1] = 1
	if !s.IsUnitVisible(1, u) {
		t.Fatal("northwest hull corner above water was rejected")
	}
	s.publishSnapshot(1)
	f := s.Snapshot.Current()
	if len(f.Units) != 1 {
		t.Fatal("unit missing from publication")
	}
	v := f.Units[0]
	if v.X != u.X || v.Y != u.Y || v.Z != u.Z {
		t.Fatal("visibility publication changed draw base")
	}
	if v.HullOffsetX != -24*numeric.FixedOne || v.HullOffsetY != 21*numeric.FixedOne || v.HullOffsetZ != -40*numeric.FixedOne || v.HullXExtent != 48*numeric.FixedOne || v.HullYExtent != 21*numeric.FixedOne || v.HullZExtent != 80*numeric.FixedOne {
		t.Fatalf("published hull origin/spans=%v/%v", [3]numeric.Fixed{v.HullOffsetX, v.HullOffsetY, v.HullOffsetZ}, [3]numeric.Fixed{v.HullXExtent, v.HullYExtent, v.HullZExtent})
	}
	if v.UnderwaterExempt || f.Visibility.SeaLevel != 20*numeric.FixedOne {
		t.Fatal("wrong published depth inputs")
	}
	// The published bytes must be detached even when the live grid is retired.
	clear(s.Vis.ByteGrid(1))
	if s.IsUnitVisible(1, u) || f.Visibility.Visible[1*int(s.Vis.W)+1] != 1 {
		t.Fatal("live/frame coverage boundary was not retained")
	}
}
