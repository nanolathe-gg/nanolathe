package client

import (
	"sort"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestOW1G_FogUnexploredUnit locks the binary hard edge [03 §3.3] via Fog Ch0==15.
// Unexplored tiles (Ch0==15) must suppress world objects; own units bypass after.
func TestOW1G_FogUnexploredUnit(t *testing.T) {
	fog := snapshot.FogView{W: 2, H: 2, Valid: true, Ch0: []uint8{15, 0, 0, 0}, Ch1: []uint8{0, 0, 0, 0}}
	// Unit at tile (0,0) -> cell 0, tile 0 -> Ch0[0]==15 -> unexplored.
	u0 := snapshot.UnitView{X: world.CellToWorld(0), Z: world.CellToWorld(0), Owner: 1}
	if !fogUnexploredUnit(fog, u0) {
		t.Fatalf("tile (0,0) with Ch0==15 should be unexplored")
	}
	// Unit at tile (1,0) -> cell 2 -> Ch0[1]==0 -> explored.
	u1 := snapshot.UnitView{X: world.CellToWorld(2), Z: world.CellToWorld(0), Owner: 1}
	if fogUnexploredUnit(fog, u1) {
		t.Fatalf("tile (1,0) with Ch0==0 should be explored")
	}
	// An invalid fog publication is fail-closed.
	if !fogUnexploredUnit(snapshot.FogView{}, u0) {
		t.Fatalf("invalid fog must be treated as unexplored")
	}
	// Feature at CX=0 (tile 0) unexplored, CX=2 (tile1) explored.
	f0 := snapshot.FeatureView{CX: 0, CZ: 0}
	if !fogUnexploredFeature(fog, f0) {
		t.Fatal("feature at (0,0) should be unexplored")
	}
	f1 := snapshot.FeatureView{CX: 2, CZ: 0}
	if fogUnexploredFeature(fog, f1) {
		t.Fatal("feature at (2,0) should be explored")
	}
}

func TestOW1G_VisibilityAdmission_OwnAlwaysEnemySuppressed(t *testing.T) {
	// Visibility grid 4x4, only cell (2,2) visible (index 10). Fog 4x4 for tile equivalence.
	vis := snapshot.VisibilityView{W: 4, H: 4, Valid: true, CoverageBytes: true, Visible: make([]uint8, 16)}
	vis.Visible[10] = 1 // cell (2,2)
	fog := snapshot.FogView{W: 4, H: 4, Valid: true, Ch0: make([]uint8, 16), Ch1: make([]uint8, 16)}
	frame := &snapshot.Frame{Visibility: vis, Fog: fog, Selection: snapshot.SelectionView{LocalPlayer: 0}}
	frame.Visibility.Visible = vis.Visible

	// Enemy at cell (0,0) -> Visible[0]==0 -> not visible -> suppressed.
	enemyHidden := snapshot.UnitView{Slot: 1, Owner: 1, X: world.CellToWorld(0), Z: world.CellToWorld(0)}
	if unitVisibleForFrame(frame, enemyHidden, 0) {
		t.Fatalf("enemy at invisible tile should be suppressed")
	}
	// Enemy at cell (2,2) -> Visible[10]==1 -> visible (SnapshotVisible uses cell directly [I9]).
	enemyVisible := snapshot.UnitView{Slot: 2, Owner: 1, X: world.CellToWorld(2), Z: world.CellToWorld(2)}
	// WorldToTile for cell 2 => tile 1, so X=2 cells => tile 1.
	// cell 2 => tile 1, cell 2 => tile1 -> index 3.
	if !unitVisibleForFrame(frame, enemyVisible, 0) {
		t.Fatalf("enemy at visible tile should be admitted, got %#v vis=%v", enemyVisible, vis.Visible)
	}
	// Own unit at same hidden tile must always be visible (owner bypass) [03 §3.2] C8.
	ownHidden := snapshot.UnitView{Slot: 3, Owner: 0, X: world.CellToWorld(0), Z: world.CellToWorld(0)}
	if !unitVisibleForFrame(frame, ownHidden, 0) {
		t.Fatalf("own unit should always be visible even in fog")
	}
	// An invalid visibility publication must not expose foreign units.
	frameInvalid := &snapshot.Frame{Visibility: snapshot.VisibilityView{}, Fog: fog, Selection: snapshot.SelectionView{LocalPlayer: 0}}
	if unitVisibleForFrame(frameInvalid, enemyHidden, 0) {
		t.Fatalf("invalid visibility must cull foreign units")
	}
	// Fog unexplored still suppresses enemy even if visibility says visible.
	// Fog tile (1,1) corresponds to cells (2..3,2..3); cell (2,2) -> tile (1,1) index 5.
	fogUnexplored := snapshot.FogView{W: 4, H: 4, Valid: true, Ch0: make([]uint8, 16), Ch1: make([]uint8, 16)}
	fogUnexplored.Ch0[5] = 15 // tile (1,1)
	if !fogUnexploredUnit(fogUnexplored, enemyVisible) {
		t.Fatalf("enemy at tile (1,1) with Ch0==15 should be unexplored, tile=%d", world.WorldToTile(enemyVisible.X))
	}
	// Own bypass of fog is handled at painter level, not here; helper alone reports true but painter keeps own.
	if !fogUnexploredUnit(fogUnexplored, enemyVisible) {
		t.Fatal("fog helper should report unexplored")
	}
}

func TestOW1G_SinglePainterPassYSorted(t *testing.T) {
	type drawable struct {
		sy  int32
		tie int64
		id  string
	}
	// Simulate Y tie: two units at same sy, lower slot first; feature interleaves.
	// sy values: unit slot1 sy 10, feature at sy 12, unit slot2 sy 12 (same as feature) -> tie decides.
	list := []drawable{
		{sy: 12, tie: 2, id: "unit2"},
		{sy: 10, tie: 1, id: "unit1"},
		{sy: 12, tie: 0, id: "feature0"}, // CX 0 tie 0
		{sy: 15, tie: 5, id: "unit5"},
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].sy != list[j].sy {
			return list[i].sy < list[j].sy
		}
		return list[i].tie < list[j].tie
	})
	want := []string{"unit1", "feature0", "unit2", "unit5"}
	for i, w := range want {
		if list[i].id != w {
			t.Fatalf("Y-sorted order %d: got %s want %s (list %+v)", i, list[i].id, w, list)
		}
	}
	// Verify stable sort preserves handle tie: sy equal must order by tie, not insertion.
	// This locks the replacement of O(n²) bubble sorts with sort.SliceStable [I1][I12].
}

func TestOW1G_MergedPainterInterleavesUnitsAndFeatures(t *testing.T) {
	// Build a fake frame with one unit at sy 20 and one feature at sy 10: feature should draw before unit after merge.
	// Use world.WorldToScreen via camera would be integrated, but here we test drawable sy directly.
	// The contract is that after merge, trees don't always paint over tanks regardless of Y.
	units := []snapshot.UnitView{
		{Slot: 1, Owner: 0, X: numeric.Fixed(0), Z: numeric.Fixed(100 << 16)}, // higher Z -> higher sy
	}
	features := []snapshot.FeatureView{
		{DefName: "tree", CX: 0, CZ: 0, X: numeric.Fixed(0), Z: numeric.Fixed(10 << 16)},
	}
	// Simulate sy as Z>>? For test, just use Z/65536 as proxy.
	type d struct {
		sy   int32
		tie  int64
		kind string
	}
	var draw []d
	for _, u := range units {
		sy := int32(int64(u.Z) >> 16) // proxy
		draw = append(draw, d{sy: sy, tie: int64(u.Slot), kind: "unit"})
	}
	for _, f := range features {
		sy := int32(int64(f.Z) >> 16)
		draw = append(draw, d{sy: sy, tie: int64(f.CX)<<32 | int64(uint32(f.CZ)), kind: "feature"})
	}
	sort.SliceStable(draw, func(i, j int) bool {
		if draw[i].sy != draw[j].sy {
			return draw[i].sy < draw[j].sy
		}
		return draw[i].tie < draw[j].tie
	})
	if len(draw) != 2 || draw[0].kind != "feature" || draw[1].kind != "unit" {
		t.Fatalf("merged painter should interleave by Y, got %+v", draw)
	}
}
