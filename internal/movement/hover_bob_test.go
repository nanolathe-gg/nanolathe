package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// hoverPlate is the ring-wound ground plate the conform reads, matching the
// stock authoring order described in [04 R-MOV-01 §5a].
func hoverPlate() *model.Model {
	return &model.Model{
		Root: 0,
		Pieces: []model.Piece{{
			Selection: true,
			Vertices: [][3]numeric.Fixed{
				{numeric.FixedFromInt(-8), 0, numeric.FixedFromInt(-8)},
				{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(-8)},
				{numeric.FixedFromInt(-8), 0, numeric.FixedFromInt(8)},
				{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(8)},
			},
			Primitives: []model.Primitive{{VertexIndices: []uint16{0, 1, 2, 3}}},
		}},
	}
}

// hoverUnit is a canhover, non-upright, non-floater mover — the shape of ten of
// this install's thirteen canhover definitions [04 R-MOV-01 §5b].
func hoverUnit(waterline int32) *units.Unit {
	return &units.Unit{
		Def:   &content.UnitDef{MaxVelocity: 100000, CanHover: true, Waterline: waterline},
		Alive: true,
		X:     numeric.FixedFromInt(32), Z: numeric.FixedFromInt(64),
		ScriptState: &units.ScriptState{Binding: &cob.Binding{Model: hoverPlate()}},
	}
}

// flatSeaTerrain is entirely below sea level, so every conform corner clamps to
// the hover floor and the committed height isolates the bob [04 R-MOV-01 §8a].
func flatSeaTerrain(seaLevel uint8) *world.Terrain {
	t := &world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64), SeaLevel: seaLevel}
	for i := range t.Plot {
		t.Plot[i].SetHeight(0)
	}
	return t
}

// TestHoverConformWritesHeight locks the branch routing. A canhover mover takes
// the four-corner conform like any other non-upright, non-floater ground mover
// [04 R-MOV-01 §8a]; it previously returned before the conform and so received
// no post-move Y write at all.
func TestHoverConformWritesHeight(t *testing.T) {
	terrain := flatSeaTerrain(100)
	u := hoverUnit(0)
	u.Y = numeric.Fixed(7<<16 | 0x1234)

	applyGroundPostMove(terrain, u, false, 1, newHoverBob(u, 0, 0, 0))

	if got := int64(u.Y) >> 16; got == 7 {
		t.Fatal("canhover unit received no post-move height write")
	}
	if got := int64(u.Y) & 0xffff; got != 0x1234 {
		t.Fatalf("conform disturbed the Y fraction: %#x", got)
	}
}

// TestHoverFloorIsSeaLevel locks the floor of [04 R-MOV-01 §8a]: a hovercraft
// rides the surface over water rather than following the sea floor. The bob is
// bounded below by -1 per [04 R-MOV-01 §5b], so sea level minus one is the
// lowest admissible committed height.
func TestHoverFloorIsSeaLevel(t *testing.T) {
	const sea = 100
	terrain := flatSeaTerrain(sea)
	u := hoverUnit(0)

	applyGroundPostMove(terrain, u, false, 1, newHoverBob(u, 0, 0, 0))

	got := int32(int64(u.Y) >> 16)
	if got < sea-1 || got > sea {
		t.Fatalf("hover height=%d, want sea level %d or one below; the sea floor is %d", got, sea, 0)
	}
}

// TestHoverBobResidueIsZeroOrMinusOne is the running form of the closure in
// [04 R-MOV-01 §5b]. Over open water every corner clamps to the same floor, so
// the committed height isolates the pairwise-truncating average of the four
// bob components. The residue must never be positive and never below -1 — that
// bound is what makes the band-2 equality the only threshold the counter can
// cross, and it is the whole basis for deriving the counter from the tick.
func TestHoverBobResidueIsZeroOrMinusOne(t *testing.T) {
	const sea = 100
	terrain := flatSeaTerrain(sea)
	seen := map[int32]bool{}
	for tick := uint32(0); tick < 256; tick++ {
		for _, speed := range []int32{0, 1000, 24999, 25000, 60000} {
			u := hoverUnit(0)
			bob := newHoverBob(u, speed, tick, tick)
			applyGroundPostMove(terrain, u, false, 1, bob)
			seen[int32(int64(u.Y)>>16)-sea] = true
		}
	}
	for offset := range seen {
		if offset != 0 && offset != -1 {
			t.Fatalf("bob residue %d escapes the {0,-1} bound of [04 R-MOV-01 §5b]", offset)
		}
	}
	if !seen[-1] {
		t.Fatal("the -1 residue never occurred; the bob is not reaching the committed height")
	}
}

// TestHoverBobFadesWhenParked locks the age fade of [04 R-MOV-01 §5]. A mover
// at rest never revalidates, so its age runs past the 60-tick clamp, the
// amplitude reaches zero and the committed height stops depending on the
// counter at all. This is why a parked hovercraft holds a stable medium band.
func TestHoverBobFadesWhenParked(t *testing.T) {
	const sea = 100
	terrain := flatSeaTerrain(sea)
	for tick := uint32(60); tick < 160; tick++ {
		u := hoverUnit(0)
		bob := newHoverBob(u, 0, tick, 0)
		if bob == nil {
			t.Fatalf("tick %d: parked hovercraft lost its hover branch entirely", tick)
		}
		if bob.amp != 0 {
			t.Fatalf("tick %d: parked hovercraft still has amplitude %d", tick, bob.amp)
		}
		applyGroundPostMove(terrain, u, false, 1, bob)
		if got := int32(int64(u.Y) >> 16); got != sea {
			t.Fatalf("tick %d: parked hover height=%d, want exactly sea level %d", tick, got, sea)
		}
	}
}

// TestHoverBobIsTickDerived locks the deliberate divergence recorded in
// [04 R-MOV-01 §5b]: the counter advances with the simulation tick and nothing
// else, so two units in identical state at the same tick commit the same
// height. Cloning retail's wall clock here would break exactly this.
func TestHoverBobIsTickDerived(t *testing.T) {
	terrain := flatSeaTerrain(100)
	for tick := uint32(0); tick < 64; tick++ {
		a, b := hoverUnit(0), hoverUnit(0)
		applyGroundPostMove(terrain, a, false, 1, newHoverBob(a, 1000, tick, tick))
		applyGroundPostMove(terrain, b, false, 1, newHoverBob(b, 1000, tick, tick))
		if a.Y != b.Y {
			t.Fatalf("tick %d: identical hovercraft committed %v and %v", tick, a.Y, b.Y)
		}
	}
}

// TestNonHoverMoverKeepsTerrainPlate guards the negative half of the routing:
// only a canhover definition gets the floor and the bob. A vehicle on the same
// terrain must still follow the sea floor [04 R-MOV-01 §8a].
func TestNonHoverMoverKeepsTerrainPlate(t *testing.T) {
	terrain := flatSeaTerrain(100)
	u := hoverUnit(0)
	u.Def.CanHover = false

	if bob := newHoverBob(u, 0, 0, 0); bob != nil {
		t.Fatal("non-hover definition produced bob inputs")
	}
	applyGroundPostMove(terrain, u, true, 1, nil)
	if got := int32(int64(u.Y) >> 16); got != 0 {
		t.Fatalf("non-hover height=%d, want the sea floor 0, not the hover floor", got)
	}
}
