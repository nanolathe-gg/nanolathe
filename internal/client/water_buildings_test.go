package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"testing"
)

func TestBuildingFoamRequiresVisibleCompletedFloater(t *testing.T) {
	c, f := wakeScene(t)
	c.terrain.SeaLevel = 10
	u := &f.Units[0]
	u.IsBuilding, u.Floater, u.CanHover = true, false, false
	u.Y = c.terrain.SeaLevelWorld() - numeric.Fixed(u.Waterline)*numeric.FixedOne
	read := func() int {
		c.list.Reset()
		c.drawBuildingFoam(f)
		var s wakeCollector
		c.list.Replay(&s)
		for _, m := range s.marks {
			if !m.Foam || m.Dust {
				t.Fatal("building emitted movement particles")
			}
		}
		return len(s.marks)
	}
	if read() != 1 {
		t.Fatal("visible floating building lost foam")
	}
	u.BuildRemaining = 1
	if read() != 0 {
		t.Fatal("unfinished building emitted foam")
	}
	u.BuildRemaining = 0
	u.Owner = 1
	if read() != 0 {
		t.Fatal("hidden enemy building revealed by foam")
	}
	u.Owner = 0
	c.terrain.LavaWorld = true
	if read() != 0 {
		t.Fatal("building foam appeared on lava")
	}
}
