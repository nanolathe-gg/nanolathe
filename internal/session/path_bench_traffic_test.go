//go:build pathbench && retail

package session

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These are authored loads on the current engine, not assertions about retail
// trajectories. Every build starts with a new session and immutable catalog.
func init() {
	pbRegister(
		pbCase{"traffic/open_flea", "open", "Same-type open group, eastbound", []int{1, 8, 15, 16, 17, 64, 256}, 600, pbOpenFlea},
		pbCase{"traffic/open_mixed", "open", "Mixed retail ground profiles in open space", []int{8, 16, 64}, 700, pbOpenMixed},
		pbCase{"traffic/control_flash", "open_control", "Single armflash at its mixed-group start", []int{1}, 700, pbControlFlash},
		pbCase{"traffic/control_stump", "open_control", "Single armstump at its mixed-group start", []int{1}, 700, pbControlStump},
		pbCase{"traffic/control_ck", "open_control", "Single armck at its mixed-group start", []int{1}, 700, pbControlCK},
		pbCase{"traffic/control_peep", "open_control", "Single armpeep at its mixed-group start", []int{1}, 700, pbControlPeep},
		pbCase{"traffic/open_away", "open", "Westbound initial command from east-side starts", []int{1, 8, 16}, 600, pbOpenAway},
		pbCase{"traffic/heading_zero", "open", "Flea heading zero before eastbound command", []int{1, 8}, 600, pbHeadingZero},
		pbCase{"traffic/heading_halfturn", "open", "Flea heading half-turn before eastbound command", []int{1, 8}, 600, pbHeadingHalfturn},
		pbCase{"traffic/dense", "open", "Touching-footprint starts at fixed group center", []int{8, 16, 64}, 700, pbDense},
		pbCase{"traffic/spaced", "open", "Spaced starts at the same group center and click", []int{8, 16, 64}, 700, pbSpaced},
		pbCase{"traffic/choke_one", "choke", "One flea-footprint aperture, one direction", []int{1, 8, 16}, 900, pbChokeOne},
		pbCase{"traffic/choke_two_opposing", "choke", "Two flea-footprint aperture, opposing commands", []int{8, 16, 64}, 1200, pbChokeOpposing},
		pbCase{"traffic/choke_wide", "choke", "Eight-cell aperture, opposing control", []int{8, 16, 64}, 900, pbChokeWide},
		pbCase{"traffic/cross", "crossing", "Perpendicular flows through an open center", []int{8, 16, 64}, 900, pbCross},
		pbCase{"traffic/merge", "crossing", "Two inlet streams merge through a four-cell aperture", []int{8, 16}, 1000, pbMerge},
		pbCase{"traffic/split", "crossing", "One stream splits to north and south destinations", []int{8, 16}, 1000, pbSplit},
		pbCase{"traffic/passing_bays", "choke", "One-footprint corridor with two passing bays", []int{8, 16}, 1200, pbPassingBays},
		pbCase{"traffic/packed_goal", "arrival", "Many actors converge on one legal point", []int{8, 16, 64}, 1000, pbPackedGoal},
		pbCase{"traffic/capacity_control", "arrival", "Assigned point has insufficient physical capacity", []int{8, 16}, 500, pbCapacityControl},
		pbCase{"traffic/idle_blockers", "arrival", "Stationary units fill the destination neighborhood", []int{8, 16}, 900, pbIdleBlockers},
		pbCase{"traffic/air_ground", "selection", "Same click mixes air and ground movement", []int{8, 16}, 700, pbAirGround},
		pbCase{"traffic/waves", "load", "Three owners issue repeated scripted group waves", []int{16, 64, 256, 1500}, 1500, pbWaves},
	)
}

func pbCheckSize(t *testing.T, size int) {
	t.Helper()
	if size <= 0 || size > 256 {
		t.Fatalf("unsupported traffic size %d", size)
	}
}

func pbGroundGrid(t *testing.T, sc *pbScene, key, cohort string, owner uint8, n int, x0, z0, cols, stride int32) []*pbActor {
	t.Helper()
	actors := make([]*pbActor, 0, n)
	for i := 0; i < n; i++ {
		cx := x0 + int32(i)%cols*stride
		cz := z0 + int32(i)/cols*stride
		actors = append(actors, pbAdd(t, sc, key, cohort, owner, cx, cz))
	}
	return actors
}

func pbOpenFlea(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	actors := pbGroundGrid(t, sc, "armflea", "east", 0, size, 8, 8, 16, 2)
	pbMove(t, sc, actors, 102, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("start grid x=8+2*(i%%16), z=8+2*(i/16), count=%d; click=(102,48)", size))
	return sc
}

func pbOpenMixed(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	keys := []string{"armflea", "armflash", "armstump", "armck"}
	actors := make([]*pbActor, 0, size)
	for i := 0; i < size; i++ {
		key := keys[i%len(keys)]
		actors = append(actors, pbAdd(t, sc, key, "mixed", 0, 8+int32(i%8)*5, 8+int32(i/8)*5))
	}
	pbMove(t, sc, actors, 102, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("keys cycle armflea,armflash,armstump,armck; grid stride=5 cols=8 count=%d; click=(102,48)", size))
	return sc
}

func pbTypeControl(t *testing.T, rules, key string, cx, cz int32) *pbScene {
	t.Helper()
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	a := pbAdd(t, sc, key, "single", 0, cx, cz)
	pbMove(t, sc, []*pbActor{a}, 102, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("single %s start=(%d,%d), retail spawn facing, click=(102,48)", key, cx, cz))
	return sc
}
func pbControlFlash(t *testing.T, rules string, size int) *pbScene {
	return pbTypeControl(t, rules, "armflash", 13, 8)
}
func pbControlStump(t *testing.T, rules string, size int) *pbScene {
	return pbTypeControl(t, rules, "armstump", 18, 8)
}
func pbControlCK(t *testing.T, rules string, size int) *pbScene {
	return pbTypeControl(t, rules, "armck", 23, 8)
}
func pbControlPeep(t *testing.T, rules string, size int) *pbScene {
	return pbTypeControl(t, rules, "armpeep", 12, 8)
}

func pbOpenAway(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	actors := pbGroundGrid(t, sc, "armflea", "west", 0, size, 90, 25, 8, 3)
	pbMove(t, sc, actors, 10, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("start grid x=90+3*(i%%8), z=25+3*(i/8), count=%d; click=(10,48); facing is retail spawn default", size))
	return sc
}

func pbHeading(t *testing.T, rules string, size int, heading uint16) *pbScene {
	t.Helper()
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	actors := pbGroundGrid(t, sc, "armflea", "heading", 0, size, 8, 8, 8, 2)
	for _, a := range actors {
		u := sc.S.Units.Unit(a.Handle)
		if u == nil || int(a.Handle) >= len(sc.S.Movement.Steers) || int(a.Handle) >= len(sc.S.Movement.Collisions) {
			t.Fatalf("missing mover state for %d", a.Handle)
		}
		steer, coll := sc.S.Movement.Steers[a.Handle], sc.S.Movement.Collisions[a.Handle]
		if steer == nil || coll == nil {
			t.Fatalf("missing ground mover state for %d", a.Handle)
		}
		u.Move.Heading, steer.Heading, steer.PendingHeading, coll.Heading = heading, heading, heading, heading
	}
	pbMove(t, sc, actors, 102, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("explicit initial heading=%d in unit, steer and collision; grid (8,8) stride2 cols8 count=%d; click=(102,48)", heading, size))
	return sc
}
func pbHeadingZero(t *testing.T, rules string, size int) *pbScene {
	return pbHeading(t, rules, size, 0)
}
func pbHeadingHalfturn(t *testing.T, rules string, size int) *pbScene {
	return pbHeading(t, rules, size, 32768)
}

func pbDensity(t *testing.T, rules string, size int, stride int32, cohort string) *pbScene {
	t.Helper()
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	rows := (size + 7) / 8
	x0 := int32(40) - 7*stride/2
	z0 := int32(40) - int32(rows-1)*stride/2
	actors := pbGroundGrid(t, sc, "armflea", cohort, 0, size, x0, z0, 8, stride)
	pbMove(t, sc, actors, 102, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("%s flea grid x=%d+%d*(i%%8), z=%d+%d*(i/8), rows=%d count=%d; same group center (40,40), click=(102,48)", cohort, x0, stride, z0, stride, rows, size))
	return sc
}
func pbDense(t *testing.T, rules string, size int) *pbScene {
	return pbDensity(t, rules, size, 2, "dense")
}
func pbSpaced(t *testing.T, rules string, size int) *pbScene {
	return pbDensity(t, rules, size, 4, "spaced")
}

func pbChoke(t *testing.T, rules string, size int, gap0, gap1 int32, opposing bool) *pbScene {
	t.Helper()
	pbCheckSize(t, size)
	ter := pbTerrain(t, 128, 96, 20, 0)
	pbWall(ter, 63, 0, 65, gap0)
	pbWall(ter, 63, gap1, 65, 96)
	sc := pbNew(t, rules, ter)
	var left []*pbActor
	if !opposing {
		left = pbGroundGrid(t, sc, "armflea", "east", 0, size, 12, 46, 8, 2)
	} else {
		left = pbGroundGrid(t, sc, "armflea", "east", 0, size/2, 12, 18, 8, 2)
		right := pbGroundGrid(t, sc, "armflea", "west", 1, size-size/2, 91, 46, 8, 2)
		pbScriptedOwnerMove(t, sc, right, 14, 48)
	}
	pbMove(t, sc, left, 110, 48, false, false)
	sc.Regions = append(sc.Regions, pbRegion{"aperture", 63, gap0, 65, gap1})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("wall x=[63,65), aperture z=[%d,%d); size=%d; opposing=%t; left click=(110,48), right click=(14,48)", gap0, gap1, size, opposing))
	return sc
}
func pbChokeOne(t *testing.T, rules string, size int) *pbScene {
	return pbChoke(t, rules, size, 47, 49, false)
}
func pbChokeOpposing(t *testing.T, rules string, size int) *pbScene {
	return pbChoke(t, rules, size, 46, 50, true)
}
func pbChokeWide(t *testing.T, rules string, size int) *pbScene {
	return pbChoke(t, rules, size, 44, 52, true)
}

func pbCross(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 128, 20, 0))
	east := pbGroundGrid(t, sc, "armflea", "east", 0, size/2, 10, 59, 8, 2)
	south := pbGroundGrid(t, sc, "armflea", "south", 1, size-size/2, 60, 10, 8, 2)
	pbMove(t, sc, east, 110, 64, false, false)
	pbScriptedOwnerMove(t, sc, south, 64, 110)
	sc.Regions = append(sc.Regions, pbRegion{"cross", 56, 56, 72, 72})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("east start (10,59) grid stride2 cols8 click=(110,64); south start (60,10) click=(64,110); each count=%d/%d", len(east), len(south)))
	return sc
}

func pbMerge(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	ter := pbTerrain(t, 128, 96, 20, 0)
	pbWall(ter, 63, 0, 65, 46)
	pbWall(ter, 63, 50, 65, 96)
	sc := pbNew(t, rules, ter)
	a := pbGroundGrid(t, sc, "armflea", "north_inlet", 0, size/2, 12, 18, 8, 2)
	b := pbGroundGrid(t, sc, "armflea", "south_inlet", 0, size-size/2, 12, 68, 8, 2)
	pbMove(t, sc, a, 110, 44, false, false)
	pbMove(t, sc, b, 110, 52, false, false)
	sc.Regions = append(sc.Regions, pbRegion{"merge", 63, 46, 65, 50})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("wall x=[63,65) except z=[46,50), inlet starts (12,18)/(12,68), stride2 cols8; counts=%d/%d; clicks=(110,44)/(110,52)", len(a), len(b)))
	return sc
}

func pbSplit(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	ter := pbTerrain(t, 128, 96, 20, 0)
	pbWall(ter, 63, 0, 65, 46)
	pbWall(ter, 63, 50, 65, 96)
	sc := pbNew(t, rules, ter)
	all := pbGroundGrid(t, sc, "armflea", "split", 0, size, 12, 46, 8, 2)
	a := all[:size/2]
	b := all[size/2:]
	pbMove(t, sc, a, 110, 22, false, false)
	pbMove(t, sc, b, 110, 74, false, false)
	sc.Regions = append(sc.Regions, pbRegion{"split", 63, 46, 65, 50})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("wall x=[63,65) except z=[46,50), grid start (12,46) stride2 cols8 count=%d, split clicks=(110,22)/(110,74)", size))
	return sc
}

func pbPassingBays(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	ter := pbTerrain(t, 128, 96, 20, 0)
	pbWall(ter, 35, 0, 93, 47)
	pbWall(ter, 35, 49, 93, 96)
	// Two authored bays add room to let opposite traffic pass.
	for _, x := range []int32{51, 75} {
		for z := int32(45); z < 47; z++ {
			for xx := x; xx < x+4; xx++ {
				ter.PlotAt(xx, z).SetFeature(world.PlotFeatureNone)
			}
		}
	}
	sc := pbNew(t, rules, ter)
	a := pbGroundGrid(t, sc, "armflea", "east", 0, size/2, 12, 46, 4, 2)
	b := pbGroundGrid(t, sc, "armflea", "west", 1, size-size/2, 103, 46, 4, 2)
	pbMove(t, sc, a, 109, 47, false, false)
	pbScriptedOwnerMove(t, sc, b, 17, 47)
	sc.Regions = append(sc.Regions, pbRegion{"corridor", 35, 47, 93, 49}, pbRegion{"bay_west", 51, 45, 55, 49}, pbRegion{"bay_east", 75, 45, 79, 49})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("corridor x=[35,93), z=[47,49); bays x=[51,55),[75,79), z=[45,47); opposing counts=%d/%d clicks=(109,47)/(17,47)", len(a), len(b)))
	return sc
}

func pbPackedGoal(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	a := pbGroundGrid(t, sc, "armflea", "packed", 0, size, 8, 8, 16, 2)
	pbMove(t, sc, a, 90, 48, false, false)
	sc.Regions = append(sc.Regions, pbRegion{"destination", 84, 42, 97, 55})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("count=%d grid x=8+2*(i%%16), z=8+2*(i/16); ordinary group click=(90,48)", size))
	return sc
}

func pbCapacityControl(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	ter := pbTerrain(t, 128, 96, 20, 0)
	// A two-cell entrance keeps the assigned point reachable while the void
	// around it prevents a large formation from occupying that target area.
	pbWall(ter, 88, 46, 93, 51)
	for z := int32(48); z < 50; z++ {
		for x := int32(88); x < 92; x++ {
			ter.PlotAt(x, z).SetFeature(world.PlotFeatureNone)
		}
	}
	sc := pbNew(t, rules, ter)
	a := pbGroundGrid(t, sc, "armflea", "over_capacity", 0, size, 8, 8, 8, 2)
	pbMove(t, sc, a, 90, 48, true, false)
	sc.Regions = append(sc.Regions, pbRegion{"one_footprint", 90, 48, 92, 50})
	sc.Notes = append(sc.Notes, "Intentionally impossible simultaneous occupancy: all actors receive the same assigned cell. An order may terminate by an engine failure path; pending is not automatically a defect.")
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("void box x=[88,93), z=[46,51) except entry lane x=[88,92),z=[48,50); assigned click=(90,48); actors=%d", size))
	return sc
}

func pbIdleBlockers(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	for z := int32(45); z <= 51; z += 2 {
		for x := int32(87); x <= 93; x += 2 {
			pbAdd(t, sc, "armflea", "idle_blocker", 0, x, z)
		}
	}
	a := pbGroundGrid(t, sc, "armflea", "arriving", 0, size, 8, 8, 8, 2)
	pbMove(t, sc, a, 90, 48, false, false)
	sc.Regions = append(sc.Regions, pbRegion{"blockers", 87, 45, 95, 53})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("16 passive blockers at odd cells x=87..93,z=45..51 stride2; %d commanded actors click=(90,48)", size))
	return sc
}

func pbAirGround(t *testing.T, rules string, size int) *pbScene {
	pbCheckSize(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	actors := make([]*pbActor, 0, size)
	for i := 0; i < size; i++ {
		key := "armflea"
		if i%2 == 1 {
			key = "armpeep"
		}
		actors = append(actors, pbAdd(t, sc, key, "mixed_air_ground", 0, 8+int32(i%8)*4, 8+int32(i/8)*4))
	}
	pbMove(t, sc, actors, 100, 48, false, false)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("alternating armflea/armpeep, x=8+4*(i%%8), z=8+4*(i/8), count=%d; common click=(100,48)", size))
	sc.Notes = append(sc.Notes, "Aircraft have separate flight and occupancy rules; ground congestion metrics apply only to ground actors.")
	return sc
}

func pbWaves(t *testing.T, rules string, size int) *pbScene {
	if size < 3 || size > 1500 {
		t.Fatal("wave count outside three 500-unit owner pools")
	}
	sc := pbNew(t, rules, pbTerrain(t, 192, 160, 20, 0))
	cohorts := [3][]*pbActor{}
	for owner := 0; owner < 3; owner++ {
		cohorts[owner] = pbGroundGrid(t, sc, "armflea", fmt.Sprintf("owner_%d", owner), uint8(owner), size/3+boolInt(owner < size%3), 8+int32(owner)*44, 8+int32(owner)*32, 16, 2)
		if owner == 0 {
			pbMove(t, sc, cohorts[owner], 160, 96, false, false)
		} else {
			pbScriptedOwnerMove(t, sc, cohorts[owner], 160-int32(owner)*48, 96-int32(owner)*28)
		}
	}
	for _, tick := range []int{150, 300, 450, 600, 750, 900, 1050, 1200} {
		tick := tick
		sc.Events = append(sc.Events, pbEvent{Tick: tick, Label: fmt.Sprintf("scripted owner wave tick=%d; three long-distance groups plus one two-cell request per owner", tick), Apply: func(t *testing.T, scene *pbScene) {
			for owner := 0; owner < 3; owner++ {
				gx := int32(160 - owner*48)
				gz := int32(96 - owner*28)
				if tick%300 == 150 {
					gx, gz = 12+int32(owner)*44, 12+int32(owner)*32
				}
				if owner == 0 {
					pbMove(t, scene, cohorts[owner], gx, gz, false, false)
				} else {
					pbScriptedOwnerMove(t, scene, cohorts[owner], gx, gz)
				}
				probe := cohorts[owner][0]
				u := scene.S.Units.Unit(probe.Handle)
				if u == nil {
					t.Fatalf("short request actor %d disappeared", probe.Handle)
				}
				shortX, shortZ := world.WorldToCell(u.X)+2, world.WorldToCell(u.Z)
				if owner == 0 {
					pbMove(t, scene, []*pbActor{probe}, shortX, shortZ, false, false)
				} else {
					pbScriptedOwnerMove(t, scene, []*pbActor{probe}, shortX, shortZ)
				}
			}
		}})
	}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("three owner grids, 16 columns stride2, count=%d, repeated commands at ticks 150,300,450,600,750,900,1050,1200; each tick one actor per owner receives a second request to its committed cell + (2,0); owner0 uses human group orders, owners1/2 use explicit order queue submissions with production human group offsets", size))
	sc.Notes = append(sc.Notes, "Scripted requests from three owners. This does not measure the AI planner; owners 1 and 2 use production order queues without the AI planner's selection logic .")
	return sc
}

func pbScriptedOwnerMove(t *testing.T, sc *pbScene, actors []*pbActor, gx, gz int32) {
	t.Helper()
	x, z := pbCell(gx), pbCell(gz)
	handles := make([]pool.Handle, len(actors))
	for i, a := range actors {
		handles[i] = a.Handle
	}
	localOwner := sc.S.LocalOwner
	if len(actors) > 0 {
		sc.S.LocalOwner = sc.S.Units.Unit(actors[0].Handle).Owner
	}
	center, count := sc.S.humanOrderCentroid(handles, 0)
	sc.S.LocalOwner = localOwner
	for _, a := range actors {
		u := sc.S.Units.Unit(a.Handle)
		if u == nil {
			t.Fatalf("scripted owner actor %d disappeared", a.Handle)
		}
		sc.S.bindOrderQueue(u)
		q := orders.QueueForUnit(u)
		if q == nil {
			t.Fatalf("scripted owner actor %d has no queue", a.Handle)
		}
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
		id := orders.Lookup("Move_Ground")
		goal := humanFormationGoal(orders.ResolvePos{X: x, Y: sc.S.World.HeightAt(x, z), Z: z}, u, center, count)
		q.Push(id, orders.NewNodeForOrder(id, 0, goal.X, goal.Y, goal.Z, sc.S.Clock.GlobalTick+1, u.Handle, false))
		a.Goal = [2]numeric.Fixed{goal.X, goal.Z}
		a.GoalActive = true
		a.GoalTick = int(sc.S.Clock.GlobalTick) + 1
		a.CaptureGoal = false
	}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("scripted owner queue tick=%d handles=%v goal=(%d,%d) with production human group offsets", sc.S.Clock.GlobalTick+1, handles, gx, gz))
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func pbAssertStartOrders(t *testing.T, sc *pbScene, caseID string) {
	t.Helper()
	for _, a := range sc.Actors {
		if !a.GoalActive {
			continue
		}
		expected := "Move_Ground"
		if a.Key == "armpeep" {
			expected = "VTOL_Move"
		}
		switch caseID {
		case "lifecycle/patrol":
			expected = "Patrol"
		case "lifecycle/moving_targets":
			switch a.Cohort {
			case "guard":
				expected = "Follow_Ground"
			case "attack":
				expected = "Attack_Chase"
			case "repair":
				expected = "RepairUnit"
			}
		case "lifecycle/builder_work":
			if a.Cohort == "builder" {
				expected = "MobileBuild"
			}
		}
		u := sc.S.Units.Unit(a.Handle)
		if u == nil || orders.QueueForUnit(u) == nil {
			t.Fatalf("commanded actor %d (%s/%s) has no queue after first tick", a.Handle, a.Key, a.Cohort)
		}
		found := false
		for _, n := range orders.QueueForUnit(u).Primary() {
			if orders.DescriptorFor(n.ID).Name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("commanded actor %d (%s/%s) lacks %s after first tick", a.Handle, a.Key, a.Cohort, expected)
		}
	}
}

// The opt-in check exercises each registered traffic builder and enough phase
// cycles to catch illegal placement and input-boundary failures early.
func TestPathBenchTrafficSmoke(t *testing.T) {
	if os.Getenv("NANOLATHE_PATH_BENCH_SMOKE") == "" {
		t.Skip("opt-in traffic smoke")
	}
	for _, c := range pbCases {
		if len(c.ID) < len("traffic/") || c.ID[:len("traffic/")] != "traffic/" {
			continue
		}
		c := c
		t.Run(c.ID, func(t *testing.T) {
			sizes := c.Sizes[:1]
			if os.Getenv("NANOLATHE_PATH_BENCH_SMOKE_FULL") != "" {
				sizes = c.Sizes
			}
			for _, size := range sizes {
				t.Run(fmt.Sprintf("n%d", size), func(t *testing.T) {
					for _, rules := range []string{"modern", "strict-3.1"} {
						t.Run(rules, func(t *testing.T) {
							sc := c.Build(t, rules, size)
							publishVisibilityForAll(sc.S)
							cycles := 32
							if c.ID == "traffic/waves" && os.Getenv("NANOLATHE_PATH_BENCH_SMOKE_FULL") != "" {
								cycles = 152 // exercise the first repeated long and short request
							}
							for i := 1; i <= cycles; i++ {
								for _, e := range sc.Events {
									if e.Tick == i {
										e.Apply(t, sc)
									}
								}
								stamp := sc.S.Clock.BeginSubTick()
								sc.S.stepAuthoritativePhases(stamp)
								if i == 1 {
									pbAssertStartOrders(t, sc, c.ID)
								}
							}
						})
					}
				})
			}
		})
	}
}
