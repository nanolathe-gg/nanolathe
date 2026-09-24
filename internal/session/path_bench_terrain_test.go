//go:build pathbench && retail

package session

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Authored experiments on this engine, not findings about the retail executable.
func init() {
	pbRegister(
		pbCase{"terrain-slope-flea", "terrain", "Flea crosses a 16-height ridge face", []int{1, 8}, 900, pbSlope("armflea")},
		pbCase{"terrain-slope-flash", "terrain", "Flash detours around the same face", []int{1, 8}, 1100, pbSlope("armflash")},
		pbCase{"terrain-slope-stump", "terrain", "Stump detours around the same face", []int{1, 8}, 1100, pbSlope("armstump")},
		pbCase{"terrain-clutter-mixed", "terrain", "Trees and rocks interrupt a broad route", []int{1, 8, 17}, 1100, pbClutter(false)},
		pbCase{"terrain-clutter-wrecks", "terrain", "Dense real wrecks leave winding clearance", []int{1, 8}, 1300, pbClutter(true)},
		pbCase{"terrain-winding", "terrain", "Twelve alternating barriers require over twenty turns", []int{1, 8}, 9000, pbWinding},
		pbCase{"terrain-branching-maze", "terrain", "Misleading west-open pockets and alternating gates, with repeated wave", []int{1, 8}, 6000, pbBranchingMaze},
		pbCase{"terrain-concave", "terrain", "Start within south-open U, goal north of closed face", []int{1, 8}, 1500, pbConcave(false)},
		pbCase{"terrain-concave-sealed", "terrain", "Sealed U makes goal unreachable", []int{1}, 650, pbConcave(true)},
		pbCase{"dynamic-new-wreck", "dynamic", "Wreck appears across open gate at tick 90", []int{1, 8}, 850, pbDynamic("add")},
		pbCase{"dynamic-reclaim-wreck", "dynamic", "Reclaim payout at tick 90 begins gate opening", []int{1, 8}, 850, pbDynamic("remove")},
		pbCase{"dynamic-close-reopen", "dynamic", "Wreck appears at tick 90 and reclaim payout occurs at tick 180", []int{1, 8}, 1100, pbDynamic("both")},
		pbCase{"knowledge-pioneer-same", "knowledge", "Pioneer then same-owner later group", []int{1, 8}, 1500, pbKnowledge("same")},
		pbCase{"knowledge-forced-fog", "knowledge", "Controlled twenty-tick fog reset before a pioneer and later group", []int{1, 8}, 1500, pbKnowledge("fogreset")},
		pbCase{"knowledge-mapped-control", "knowledge", "Fully mapped pioneer and later group, with no unknown terrain", []int{1, 8}, 1500, pbKnowledge("mapped")},
		pbCase{"knowledge-no-pioneer", "knowledge", "Matched tick and later group without pioneer", []int{1, 8}, 1500, pbKnowledge("none")},
		pbCase{"knowledge-allied", "knowledge", "Pioneer owner zero, later allied owner one", []int{1, 8}, 1500, pbKnowledge("allied")},
		pbCase{"knowledge-profile", "knowledge", "Flea pioneer, later Flash profile", []int{1, 8}, 1500, pbKnowledge("profile")},
		pbCase{"knowledge-reopened", "knowledge", "Reclaim removes obstacle after pioneer, before later group", []int{1, 8}, 1500, pbKnowledge("reopened")},
	)
}

// Keep raw height and its derived 2-by-2 extrema consistent.
func pbRidge(ter *world.Terrain, x0, x1, z0, z1 int32, high uint8) {
	for z := z0; z < z1; z++ {
		for x := x0; x < x1; x++ {
			ter.PlotAt(x, z).SetHeight(high)
		}
	}
	for z := int32(0); z < ter.CellH; z++ {
		for x := int32(0); x < ter.CellW; x++ {
			lo, hi := uint8(255), uint8(0)
			for dz := int32(0); dz <= 1; dz++ {
				for dx := int32(0); dx <= 1; dx++ {
					if x+dx >= ter.CellW || z+dz >= ter.CellH {
						continue
					}
					v := ter.PlotAt(x+dx, z+dz).Height()
					if v < lo {
						lo = v
					}
					if v > hi {
						hi = v
					}
				}
			}
			c := ter.PlotAt(x, z)
			c.SetMinHeight(lo)
			c.SetMaxHeight(hi)
		}
	}
}

func pbLine(t *testing.T, sc *pbScene, key, cohort string, owner uint8, size int, x, z, dx, dz int32) []*pbActor {
	t.Helper()
	out := make([]*pbActor, 0, size)
	for i := 0; i < size; i++ {
		out = append(out, pbAdd(t, sc, key, cohort, owner, x+int32(i)*dx, z+int32(i)*dz))
	}
	return out
}

func pbFeature(t *testing.T, sc *pbScene, key string, x, z int32) {
	t.Helper()
	def := sc.S.Catalog.Features[key]
	if def == nil || !def.Blocking {
		t.Fatalf("required blocking retail feature %s absent", key)
	}
	if sc.S.Features.PlaceAt(int(x), int(z), def) == nil {
		t.Fatalf("place %s at %d,%d", key, x, z)
	}
}

// Static 4-neighbor reachability independently checks authored topology. It
// does not predict scheduler, follower, occupancy, visibility, or arrival.
func pbAssertRoute(t *testing.T, sc *pbScene, a *pbActor, gx, gz int32, want bool) {
	t.Helper()
	ter := sc.S.World
	p := sc.S.Movement.ProfileFor(a.Handle)
	coll := sc.S.Movement.Collisions[a.Handle]
	if coll == nil {
		t.Fatalf("no collision state for %s", a.Key)
	}
	start := coll.CachedAnchor
	bx, bz := coll.HalfBias()
	goal := movement.QuantizedAnchor(int32(pbCell(gx)), int32(pbCell(gz)), bx, bz)
	seen := make([]bool, int(ter.CellW*ter.CellH))
	queue := [][2]int32{{start.X, start.Z}}
	seen[start.Z*ter.CellW+start.X] = true
	reached := false
	for len(queue) != 0 {
		at := queue[0]
		queue = queue[1:]
		if at[0] == goal.X && at[1] == goal.Z {
			reached = true
			break
		}
		for _, d := range [][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			x, z := at[0]+d[0], at[1]+d[1]
			if x < 0 || z < 0 || x >= ter.CellW || z >= ter.CellH {
				continue
			}
			idx := z*ter.CellW + x
			if seen[idx] || !p.IsPassableFootprint(ter, x, z) {
				continue
			}
			seen[idx] = true
			queue = append(queue, [2]int32{x, z})
		}
	}
	if reached != want {
		t.Fatalf("static footprint route %s from %v to world-center %d,%d (anchor %v) = %t, want %t", a.Key, start, gx, gz, goal, reached, want)
	}
}

func pbSlope(key string) func(*testing.T, string, int) *pbScene {
	return func(t *testing.T, rules string, size int) *pbScene {
		ter := pbTerrain(t, 144, 112, 20, 0)
		pbRidge(ter, 55, 58, 16, 96, 36)
		sc := pbNew(t, rules, ter)
		actors := pbLine(t, sc, key, "cross", 0, size, 14, 30, 0, 5)
		p := sc.S.Movement.ProfileFor(actors[0].Handle)
		if p.IsPassableFootprint(ter, 54, 45) != (key == "armflea") {
			t.Fatalf("ridge does not distinguish %s", key)
		}
		pbAssertRoute(t, sc, actors[0], 116, 55, true)
		pbMove(t, sc, actors, 116, 55, false, false)
		sc.Regions = []pbRegion{{Name: "face", X0: 53, Z0: 16, X1: 60, Z1: 96}, {Name: "north-detour", X0: 48, Z0: 4, X1: 64, Z1: 16}, {Name: "south-detour", X0: 48, Z0: 96, X1: 64, Z1: 108}}
		sc.Inputs = append(sc.Inputs, "ridge x=[55,58), z=[16,96), height 20->36; derived extrema recomputed")
		return sc
	}
}

func pbClutter(wrecks bool) func(*testing.T, string, int) *pbScene {
	return func(t *testing.T, rules string, size int) *pbScene {
		sc := pbNew(t, rules, pbTerrain(t, 152, 112, 20, 0))
		for row := int32(0); row < 5; row++ {
			for col := int32(0); col < 9; col++ {
				if (row+col)%4 == 0 {
					continue
				}
				key := "tree1"
				if wrecks {
					key = "armflash_dead"
				} else if col%3 == 0 {
					key = "rock06"
				}
				pbFeature(t, sc, key, 40+col*10, 19+row*17)
			}
		}
		actors := pbLine(t, sc, "armflea", "cross", 0, size, 12, 20, 0, 5)
		pbMove(t, sc, actors, 136, 72, false, false)
		sc.Regions = []pbRegion{{Name: "clutter", X0: 36, Z0: 14, X1: 126, Z1: 96}}
		sc.Inputs = append(sc.Inputs, fmt.Sprintf("5x9 features at (40+10*c,19+17*r), skip (r+c)%%4==0; wrecks=%t", wrecks))
		return sc
	}
}

func pbWinding(t *testing.T, rules string, size int) *pbScene {
	ter := pbTerrain(t, 272, 112, 20, 0)
	pbWall(ter, 0, 0, 272, 8)
	pbWall(ter, 0, 106, 272, 112)
	for i := int32(0); i < 12; i++ {
		x := 25 + i*19
		if i%2 == 0 {
			pbWall(ter, x, 8, x+3, 92)
		} else {
			pbWall(ter, x, 28, x+3, 106)
		}
	}
	sc := pbNew(t, rules, ter)
	actors := pbLine(t, sc, "armflea", "maze", 0, size, 8, 54, 0, 5)
	pbAssertRoute(t, sc, actors[0], 258, 54, true)
	pbMove(t, sc, actors, 258, 54, false, false)
	sc.Regions = []pbRegion{{Name: "alternating-gates", X0: 22, Z0: 8, X1: 241, Z1: 106}}
	sc.Inputs = append(sc.Inputs, "terrain 272x112, void borders z=[0,8) and [106,112); 12 attached void bars width 3 at x=25+19*i, alternating opposite 14/20-cell gaps; 11 mandatory side reversals, at least 22 right-angle turns")
	return sc
}

func pbBranchingMaze(t *testing.T, rules string, size int) *pbScene {
	ter := pbTerrain(t, 272, 112, 20, 0)
	pbWall(ter, 0, 0, 272, 8)
	pbWall(ter, 0, 106, 272, 112)
	// The alternating bars force broad detours. Four pockets point toward the
	// goal but have a closed east end; they are open on the approach side.
	for i := int32(0); i < 7; i++ {
		x := 35 + i*30
		if i%2 == 0 {
			pbWall(ter, x, 8, x+3, 87)
		} else {
			pbWall(ter, x, 27, x+3, 106)
		}
	}
	for _, x := range []int32{48, 108, 168, 228} {
		pbWall(ter, x, 44, x+14, 46)
		pbWall(ter, x, 60, x+14, 62)
		pbWall(ter, x+12, 44, x+14, 62)
	}
	sc := pbNew(t, rules, ter)
	first := pbLine(t, sc, "armflea", "first", 0, size, 8, 51, 0, 5)
	pbAssertRoute(t, sc, first[0], 256, 54, true)
	pbMove(t, sc, first, 256, 54, false, false)
	sc.Events = []pbEvent{{Tick: 1200, Label: fmt.Sprintf("spawn %d second-wave fleas at (8,51+5*i), order (256,54)", size), Apply: func(t *testing.T, s *pbScene) {
		wave := pbLine(t, s, "armflea", "second", 0, size, 8, 51, 0, 5)
		pbMove(t, s, wave, 256, 54, false, false)
	}}}
	sc.Regions = []pbRegion{{Name: "wrong-pocket-1", X0: 48, Z0: 46, X1: 60, Z1: 60}, {Name: "wrong-pocket-2", X0: 108, Z0: 46, X1: 120, Z1: 60}, {Name: "wrong-pocket-3", X0: 168, Z0: 46, X1: 180, Z1: 60}, {Name: "wrong-pocket-4", X0: 228, Z0: 46, X1: 240, Z1: 60}, {Name: "maze-detours", X0: 32, Z0: 8, X1: 219, Z1: 106}}
	sc.Inputs = append(sc.Inputs, "terrain 272x112, void borders z=[0,8) and [106,112); 7 attached void bars width 3 at x=35+30*i, alternating north/south gap; 4 west-open, east-closed pockets at x=48,108,168,228; second wave tick 1200")
	return sc
}

func pbConcave(closed bool) func(*testing.T, string, int) *pbScene {
	return func(t *testing.T, rules string, size int) *pbScene {
		ter := pbTerrain(t, 112, 112, 20, 0)
		pbWall(ter, 40, 28, 76, 31)
		pbWall(ter, 40, 28, 43, 77)
		pbWall(ter, 73, 28, 76, 77)
		if closed {
			pbWall(ter, 40, 74, 76, 77)
		}
		sc := pbNew(t, rules, ter)
		actors := make([]*pbActor, 0, size)
		for i := 0; i < size; i++ {
			actors = append(actors, pbAdd(t, sc, "armflea", "inside", 0, 53+int32(i%4)*4, 56+int32(i/4)*5))
		}
		pbAssertRoute(t, sc, actors[0], 58, 13, !closed)
		pbMove(t, sc, actors, 58, 13, false, false)
		sc.Regions = []pbRegion{{Name: "blind-pocket", X0: 43, Z0: 31, X1: 73, Z1: 74}, {Name: "escape-mouth", X0: 39, Z0: 74, X1: 77, Z1: 87}}
		if closed {
			sc.Notes = append(sc.Notes, "Goal intentionally unreachable from sealed U")
		}
		sc.Inputs = append(sc.Inputs, fmt.Sprintf("U void walls x=[40,76), z=[28,77), south mouth closed=%t", closed))
		return sc
	}
}

func pbDynamic(kind string) func(*testing.T, string, int) *pbScene {
	return func(t *testing.T, rules string, size int) *pbScene {
		ter := pbTerrain(t, 136, 80, 20, 0)
		pbWall(ter, 48, 8, 51, 33)
		pbWall(ter, 48, 37, 51, 72)
		sc := pbNew(t, rules, ter)
		if kind != "add" {
			pbFeature(t, sc, "armwin_dead", 48, 33)
		}
		actors := pbLine(t, sc, "armflea", "cross", 0, size, 10, 17, 0, 5)
		pbAssertRoute(t, sc, actors[0], 116, 36, true)
		pbMove(t, sc, actors, 116, 36, false, false)
		if kind == "add" || kind == "both" {
			sc.Events = append(sc.Events, pbEvent{Tick: 90, Label: "place armwin_dead at (48,33)", Apply: func(t *testing.T, s *pbScene) { pbFeature(t, s, "armwin_dead", 48, 33) }})
		}
		if kind == "remove" || kind == "both" {
			tick := 90
			if kind == "both" {
				tick = 180
			}
			sc.Events = append(sc.Events, pbEvent{Tick: tick, Label: fmt.Sprintf("reclaim armwin_dead at (48,33), tick %d", tick), Apply: func(t *testing.T, s *pbScene) {
				_, _, ok := s.S.Features.ReclaimAt(48, 33)
				if !ok {
					t.Fatal("reclaim refused")
				}
			}})
		}
		sc.Regions = []pbRegion{{Name: "gate", X0: 46, Z0: 32, X1: 54, Z1: 41}, {Name: "detour", X0: 43, Z0: 70, X1: 56, Z1: 78}}
		sc.Inputs = append(sc.Inputs, "terrain 136x80; void bars x=[48,51), z=[8,33) and [37,72); armwin_dead 4x4 anchor (48,33) closes the four-cell gap")
		return sc
	}
}

func pbKnowledge(kind string) func(*testing.T, string, int) *pbScene {
	return func(t *testing.T, rules string, size int) *pbScene {
		ter := pbTerrain(t, 128, 96, 20, 0)
		if kind == "reopened" {
			pbWall(ter, 55, 19, 58, 43)
			pbWall(ter, 55, 47, 58, 72)
		} else {
			pbWall(ter, 55, 19, 58, 72)
		}
		sc := pbNew(t, rules, ter)
		if kind != "mapped" {
			var eligible [10]bool
			for i := range eligible {
				eligible[i] = true
			}
			mode := visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled
			if kind == "fogreset" {
				mode = visibility.ModeHistoryEnabled
			}
			sc.S.Vis.RefreshMode(mode, true, eligible, nil)
			if kind == "fogreset" {
				// The explicitly artificial control keeps the first request and
				// contact unmapped across entry publication and early LOS updates.
				for tick := 1; tick <= 20; tick++ {
					sc.Events = append(sc.Events, pbEvent{Tick: tick, Label: fmt.Sprintf("reset mapping history before tick %d", tick), Apply: func(t *testing.T, s *pbScene) {
						s.S.Vis.RefreshMode(visibility.ModeHistoryEnabled, true, eligible, nil)
					}})
				}
			}
		}
		if kind == "reopened" {
			pbFeature(t, sc, "armwin_dead", 55, 43)
		}
		initialFeatures := len(sc.S.Features.Instances())
		if kind != "none" {
			pioneer := pbAdd(t, sc, "armflea", "pioneer", 0, 53, 46)
			pbMove(t, sc, []*pbActor{pioneer}, 107, 46, false, false)
			pioneer.ExpectedRemoval = true
			sc.Events = append(sc.Events, pbEvent{Tick: 500, Label: "retire pioneer by unit death at tick 500", Apply: func(t *testing.T, s *pbScene) { s.S.Units.Destroy(pioneer.Handle, units.DeathUnknown) }})
			sc.Events = append(sc.Events, pbEvent{Tick: 510, Label: "verify pioneer removed and no corpse at tick 510", Apply: func(t *testing.T, s *pbScene) {
				if s.S.Units.Unit(pioneer.Handle) != nil || len(s.S.Features.Instances()) != initialFeatures {
					t.Fatal("pioneer removal changed later-route topology")
				}
			}})
		}
		if kind == "reopened" {
			sc.Events = append(sc.Events, pbEvent{Tick: 550, Label: "reclaim armwin_dead at (55,43) tick 550", Apply: func(t *testing.T, s *pbScene) {
				_, _, ok := s.S.Features.ReclaimAt(55, 43)
				if !ok {
					t.Fatal("reclaim refused")
				}
			}})
		}
		sc.Events = append(sc.Events, pbEvent{Tick: 600, Label: fmt.Sprintf("select later human owner, spawn %d units at (53,46+5*i), order (107,46)", size), Apply: func(t *testing.T, s *pbScene) {
			key, owner := "armflea", uint8(0)
			if kind == "profile" {
				key = "armflash"
			}
			if kind == "allied" {
				owner = 1
			}
			// The human adapter accepts only LocalOwner handles. Every
			// matched case makes this explicit handoff at the same tick.
			for i := range s.S.Econ.Players {
				s.S.Econ.Players[i].ControllerState = 2
			}
			s.S.Econ.Players[owner].ControllerState = 1
			s.S.LocalOwner = owner
			later := pbLine(t, s, key, "later", owner, size, 53, 46, 0, 5)
			pbMove(t, s, later, 107, 46, false, false)
		}})
		sc.Regions = []pbRegion{{Name: "blind-face", X0: 51, Z0: 19, X1: 61, Z1: 72}, {Name: "north-detour", X0: 50, Z0: 8, X1: 63, Z1: 19}, {Name: "south-detour", X0: 50, Z0: 72, X1: 63, Z1: 85}}
		sc.Inputs = append(sc.Inputs, "void bar x=[55,58), z=[19,72); pioneer/later starts x=53 adjacent to wall; later order tick 600; owners 0 and 1 allied")
		sc.Notes = append(sc.Notes, "Pioneer removed at tick 500; tick 510 checks no corpse or added feature. No-pioneer control matches later order tick; other session history may still differ.")
		if kind == "mapped" {
			sc.Notes = append(sc.Notes, "Fully mapped control retains pbNew visibility mode zero; no terrain can be newly learned.")
		} else if kind == "fogreset" {
			sc.Notes = append(sc.Notes, "Artificial fog control uses history only and resets mapping before ticks 1-20; it is separate from natural discovery cases.")
		} else {
			sc.Notes = append(sc.Notes, "Initially unmapped history and current sight enabled; ordinary observer publication discovers terrain naturally, and zero learned blocks is a valid outcome.")
		}
		sc.Notes = append(sc.Notes, "At tick 600 the player control records and host human slot are set to the later cohort's owner: owner one in the allied case, owner zero in matched controls.")
		return sc
	}
}

func TestPathBenchTerrainSmoke(t *testing.T) {
	if os.Getenv("NANOLATHE_PATH_BENCH_TERRAIN_SMOKE") == "" {
		t.Skip("opt-in terrain/naval smoke")
	}
	for _, c := range pbCases {
		if c.Family != "terrain" && c.Family != "dynamic" && c.Family != "knowledge" && c.Family != "naval" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			for _, size := range c.Sizes {
				t.Run(fmt.Sprintf("n%d", size), func(t *testing.T) {
					sc := c.Build(t, "modern", size)
					pbValidateAnchors(t, sc, 0)
					publishVisibilityForAll(sc.S)
					window := 31
					if os.Getenv("NANOLATHE_PATH_BENCH_TERRAIN_FULL") != "" {
						window = c.Ticks
					}
					for tick := 1; tick <= window; tick++ {
						if tick == 499 && c.Family == "knowledge" {
							t.Logf("owner-0 learned blocks before tick 499: %d", pbKnownCount(sc, 0))
						}
						for _, event := range sc.Events {
							if event.Tick == tick {
								before := len(sc.Actors)
								event.Apply(t, sc)
								pbValidateAnchors(t, sc, before)
							}
						}
						step := sc.S.Clock.BeginSubTick()
						sc.S.stepAuthoritativePhases(step)
					}
				})
			}
		})
	}
}

func pbValidateAnchors(t *testing.T, sc *pbScene, from int) {
	t.Helper()
	for _, a := range sc.Actors[from:] {
		if sc.S.Units.Unit(a.Handle) == nil {
			continue
		}
		coll := sc.S.Movement.Collisions[a.Handle]
		if coll == nil {
			t.Fatalf("%s has no collision state", a.Key)
		}
		anchor := coll.CachedAnchor
		p := sc.S.Movement.ProfileFor(a.Handle)
		if !p.IsPassableFootprint(sc.S.World, anchor.X, anchor.Z) {
			t.Fatalf("%s at anchor %v has blocked terrain footprint", a.Key, anchor)
		}
	}
}

func pbKnownCount(sc *pbScene, owner uint8) int {
	if sc.S.Movement.Rules == nil {
		return 0
	}
	learned := sc.S.Movement.Rules.LearnedTerrain(sc.S.Movement)
	if learned == nil {
		return 0
	}
	count := 0
	for z := int32(0); z < sc.S.World.CellH>>1; z++ {
		for x := int32(0); x < sc.S.World.CellW>>1; x++ {
			if learned.Known(x, z, owner) {
				count++
			}
		}
	}
	return count
}
