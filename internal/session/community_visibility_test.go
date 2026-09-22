package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestCommunityVisibilityUsesCanonicalFilingAndPublishesAnswer(t *testing.T) {
	s := wreckOrientationSession(t)
	s.Vis.SetMode(visibility.ModeCurrentEnabled)
	for i := range s.Vis.ByteGrid(0) {
		s.Vis.ByteGrid(0)[i] = 1
	}
	def := s.Catalog.Units["wreckvictim"]
	def.CanFly = true
	h, err := s.Units.Create(def, 1, world.CellToWorld(s.World.CellW)+(8<<16), 64<<16, 64<<16)
	if err != nil {
		t.Fatal(err)
	}
	u := s.Units.Unit(h)
	s.Movement.EnsureUnit(u)
	u.Move.Mode, u.Move.ModeMirror = 2, 2
	if !s.visibilityOffMap(uint16(h)) {
		t.Fatal("fixture was not filed in the canonical off-map bucket")
	}
	for i, mode := range []string{StrictRuleSetName, CommunityRuleSetName, ModernRuleSetName} {
		if err := s.SetRules(mode); err != nil {
			t.Fatal(err)
		}
		want := mode != StrictRuleSetName
		if got := s.IsUnitVisible(0, u); got != want {
			t.Fatalf("%s direct visibility = %v, want %v", mode, got, want)
		}
		s.publishSnapshot(uint32(i + 1))
		found := false
		for _, view := range s.Snapshot.Current().Units {
			if view.Slot == h {
				found = true
				if !view.DirectVisibilityKnown || view.DirectlyVisible != want {
					t.Fatalf("%s published visibility = %v/%v", mode, view.DirectVisibilityKnown, view.DirectlyVisible)
				}
			}
		}
		if !found {
			t.Fatal("unit absent from publication")
		}
	}
	if got := testing.AllocsPerRun(100, s.RebindRules); got != 0 {
		t.Fatalf("rebind allocated %v", got)
	}
}
