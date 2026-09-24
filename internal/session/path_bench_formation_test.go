//go:build pathbench && retail

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// This is the versioned command trace produced by the pure Modern drag
// geometry in cmd/nanolathe. It is an authored benchmark input, not retail data.
type pbFormationExport struct {
	Version int               `json:"version"`
	Cases   []pbFormationCase `json:"cases"`
}
type pbFormationCase struct {
	ID                  string             `json:"id"`
	Family              string             `json:"family"`
	Description         string             `json:"description"`
	Size                int                `json:"size"`
	Ticks               int                `json:"ticks"`
	Terrain             string             `json:"terrain"`
	Width               int32              `json:"width_cells"`
	Height              int32              `json:"height_cells"`
	Actors              []pbFormationActor `json:"actors"`
	Stroke              [][2]int32         `json:"stroke_world"`
	Sampled             [][2]int32         `json:"sampled_world"`
	AssignmentNS        []int64            `json:"assignment_ns"`
	AssignmentAllocs    float64            `json:"assignment_allocs"`
	AssignmentBytes     uint64             `json:"assignment_bytes"`
	TotalDirectDistance float64            `json:"total_direct_distance_world"`
	DuplicateGoals      int                `json:"duplicate_goals"`
	IdentitySHA256      string             `json:"identity_sha256"`
}
type pbFormationActor struct {
	Key   string   `json:"key"`
	Start [2]int32 `json:"start_cell"`
	Goal  [2]int32 `json:"assigned_world"`
}

func init() {
	path := os.Getenv("NANOLATHE_PATH_BENCH_FORMATIONS")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("pathbench formations: %v", err))
	}
	var export pbFormationExport
	if err := json.Unmarshal(data, &export); err != nil {
		panic(fmt.Sprintf("pathbench formations: %v", err))
	}
	if export.Version != 1 {
		panic(fmt.Sprintf("pathbench formations: unsupported version %d", export.Version))
	}
	for _, fixture := range export.Cases {
		fixture := fixture
		pbRegister(pbCase{
			ID:          fmt.Sprintf("formation_%s_%d", fixture.ID, fixture.Size),
			Family:      fixture.Family,
			Description: fixture.Description,
			Sizes:       []int{fixture.Size},
			Ticks:       fixture.Ticks,
			Build:       func(t *testing.T, rules string, size int) *pbScene { return pbBuildFormation(t, rules, size, fixture) },
		})
	}
}

func pbBuildFormation(t *testing.T, rules string, size int, f pbFormationCase) *pbScene {
	t.Helper()
	if size != f.Size || size != len(f.Actors) || len(f.Sampled) != size || f.Width <= 0 || f.Height <= 0 {
		t.Fatalf("formation %s invalid dimensions/count: size=%d actors=%d sampled=%d terrain=%dx%d", f.ID, size, len(f.Actors), len(f.Sampled), f.Width, f.Height)
	}
	input, err := json.Marshal(struct {
		Actors          []pbFormationActor
		Stroke, Sampled [][2]int32
	}{f.Actors, f.Stroke, f.Sampled})
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(input)
	if hex.EncodeToString(h[:]) != f.IdentitySHA256 {
		t.Fatalf("formation %s command identity mismatch", f.ID)
	}
	if len(f.Stroke) < 2 || len(f.AssignmentNS) == 0 {
		t.Fatalf("formation %s lacks stroke or timing samples", f.ID)
	}
	height, sea := uint8(20), uint8(10)
	if f.Terrain == "water" {
		height, sea = 240, 220
	}
	terrain := pbTerrain(t, f.Width, f.Height, height, sea)
	endCellX, endCellZ := f.Stroke[len(f.Stroke)-1][0]/16, f.Stroke[len(f.Stroke)-1][1]/16
	if endCellX < 2 || endCellX+2 >= f.Width || endCellZ < 3 || endCellZ+3 >= f.Height {
		t.Fatalf("formation %s endpoint patch outside terrain", f.ID)
	}
	switch f.Terrain {
	case "flat":
	case "choke":
		pbWall(terrain, 120, 0, 124, 112)
		pbWall(terrain, 120, 144, 124, f.Height)
	case "blocked":
		pbWall(terrain, endCellX-1, endCellZ-3, endCellX+3, endCellZ+4)
	case "water", "slope":
		for z := endCellZ - 3; z < endCellZ+4; z++ {
			for x := endCellX - 1; x < endCellX+3; x++ {
				p := terrain.PlotAt(x, z)
				if f.Terrain == "water" {
					p.SetHeight(0)
					p.SetMinHeight(0)
					p.SetMaxHeight(0)
				} else {
					p.SetHeight(90)
					p.SetMinHeight(20)
					p.SetMaxHeight(200)
				}
			}
		}
	default:
		t.Fatalf("formation %s unknown terrain %q", f.ID, f.Terrain)
	}
	sc := pbNew(t, rules, terrain)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("alt-drag fixture=%s size=%d identity_sha256=%s stroke_world=%v sampled_world=%v total_direct_distance_world=%g duplicate_goals=%d", f.ID, f.Size, f.IdentitySHA256, f.Stroke, f.Sampled, f.TotalDirectDistance, f.DuplicateGoals))
	if f.ID == "short_overfull" {
		sc.Notes = append(sc.Notes, "Overfull destination control: repeated or too-close assigned goals are intentionally impossible to occupy simultaneously.")
	}
	if f.Terrain == "blocked" || f.Terrain == "water" || f.Terrain == "slope" {
		sc.Notes = append(sc.Notes, "Terrain endpoint control: goals on the authored impassable patch are expected to remain unfulfilled.")
	}
	if f.Terrain == "choke" {
		sc.Regions = append(sc.Regions, pbRegion{Name: "choke", X0: 120, Z0: 112, X1: 124, Z1: 144})
	}
	invalidGoals := 0
	for i, a := range f.Actors {
		if a.Start[0] < 0 || a.Start[1] < 0 || a.Start[0] >= f.Width || a.Start[1] >= f.Height {
			t.Fatalf("formation %s actor %d start out of terrain", f.ID, i)
		}
		if a.Goal[0] < 0 || a.Goal[1] < 0 || a.Goal[0] >= f.Width*16 || a.Goal[1] >= f.Height*16 {
			t.Fatalf("formation %s actor %d goal out of terrain", f.ID, i)
		}
		actor := pbAdd(t, sc, a.Key, "formation", 0, a.Start[0], a.Start[1])
		anchor := pbFormationGoalAnchor(t, sc, actor, a.Goal)
		if !sc.S.Movement.ProfileFor(actor.Handle).IsPassableFootprint(terrain, anchor.X, anchor.Z) {
			invalidGoals++
		}
		// One actor per order preserves the exact destination assigned on release.
		pbMoveWorld(t, sc, []*pbActor{actor}, numeric.FixedFromInt(int64(a.Goal[0])), numeric.FixedFromInt(int64(a.Goal[1])), true, false)
	}
	if f.Terrain == "blocked" || f.Terrain == "water" || f.Terrain == "slope" {
		if invalidGoals == 0 {
			t.Fatalf("formation %s lacks an impassable assigned goal", f.ID)
		}
	} else if invalidGoals != 0 {
		t.Fatalf("formation %s has %d unintended impassable assigned goals", f.ID, invalidGoals)
	}
	overlaps := pbFormationOverlaps(t, sc, f)
	if (f.ID == "line" || f.ID == "reverse" || f.ID == "long_line") && overlaps != 0 {
		t.Fatalf("formation %s has %d overlapping profile goal footprints", f.ID, overlaps)
	}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("assigned_goal_footprint_overlaps=%d impassable_assigned_goals=%d", overlaps, invalidGoals))
	return sc
}

func pbFormationOverlaps(t *testing.T, sc *pbScene, f pbFormationCase) int {
	t.Helper()
	type rectangle struct{ x, z, w, h int32 }
	rects := make([]rectangle, len(f.Actors))
	for i, a := range f.Actors {
		p := sc.S.Movement.ProfileFor(sc.Actors[i].Handle)
		anchor := pbFormationGoalAnchor(t, sc, sc.Actors[i], a.Goal)
		rects[i] = rectangle{anchor.X, anchor.Z, int32(p.FootPrintX), int32(p.FootPrintZ)}
	}
	count := 0
	for i, a := range rects {
		for _, b := range rects[i+1:] {
			if a.x < b.x+b.w && b.x < a.x+a.w && a.z < b.z+b.h && b.z < a.z+a.h {
				count++
			}
		}
	}
	return count
}

func pbFormationGoalAnchor(t *testing.T, sc *pbScene, actor *pbActor, goal [2]int32) movement.Cell {
	t.Helper()
	p := sc.S.Movement.ProfileFor(actor.Handle)
	if p.FootPrintX <= 0 || p.FootPrintZ <= 0 {
		t.Fatalf("formation actor %s lacks movement footprint", actor.Key)
	}
	state := movement.CollisionState{X: int32(numeric.FixedFromInt(int64(goal[0]))), Z: int32(numeric.FixedFromInt(int64(goal[1]))), FootPrintX: p.FootPrintX, FootPrintZ: p.FootPrintZ}
	return state.ProposedAnchor(1)
}

func TestPathBenchFormationSmoke(t *testing.T) {
	if os.Getenv("NANOLATHE_PATH_BENCH_FORMATION_SMOKE") == "" {
		t.Skip("opt-in formation session smoke")
	}
	if os.Getenv("NANOLATHE_PATH_BENCH_FORMATIONS") == "" {
		t.Fatal("formation fixture path required")
	}
	count := 0
	for _, c := range pbCases {
		if c.Family != "alt_drag" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			sc := c.Build(t, "modern", c.Sizes[0])
			publishVisibilityForAll(sc.S)
			for i := 0; i < 3; i++ {
				tick := sc.S.Clock.BeginSubTick()
				sc.S.stepAuthoritativePhases(tick)
			}
		})
		count++
	}
	if count == 0 {
		t.Fatal("no formation cases registered")
	}
}

func TestPathBenchFormationLineWindow(t *testing.T) {
	if os.Getenv("NANOLATHE_PATH_BENCH_FORMATION_WINDOW") == "" {
		t.Skip("opt-in full line window")
	}
	if os.Getenv("NANOLATHE_PATH_BENCH_FORMATIONS") == "" {
		t.Fatal("formation fixture path required")
	}
	var line *pbCase
	for i := range pbCases {
		if pbCases[i].ID == "formation_line_8" {
			line = &pbCases[i]
			break
		}
	}
	if line == nil {
		t.Fatal("line8 fixture not registered")
	}
	sc := line.Build(t, "modern", 8)
	publishVisibilityForAll(sc.S)
	completed := make([]int, len(sc.Actors))
	for tick := 1; tick <= line.Ticks; tick++ {
		now := sc.S.Clock.BeginSubTick()
		sc.S.stepAuthoritativePhases(now)
		for i, a := range sc.Actors {
			if completed[i] != 0 {
				continue
			}
			u := sc.S.Units.Unit(a.Handle)
			if u == nil || !u.Alive {
				t.Fatalf("actor %d removed at tick %d", i, tick)
			}
			q := orders.QueueForUnit(u)
			if (q == nil || q.Head() == nil || q.Head().ID != orders.Lookup("Move_Ground")) && pbNear(a, u.X, u.Z) {
				completed[i] = tick
			}
		}
	}
	arrived := 0
	for _, tick := range completed {
		if tick > 0 {
			arrived++
		}
	}
	t.Logf("line8 window=%d completed=%d/%d completion_ticks=%v", line.Ticks, arrived, len(completed), completed)
	if arrived != len(completed) {
		t.Fatalf("line8 full window too short or orders stalled: %d/%d complete", arrived, len(completed))
	}
}
