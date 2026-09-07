package features

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestRetailFeatureImageUsesRowMajorFamiliesAndExactWords(t *testing.T) {
	normal := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, FootprintX: 1, FootprintZ: 1, Damage: 10}
	anim := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "burning"}, FootprintX: 1, FootprintZ: 1, Damage: 10, Filename: "burn.gaf"}
	model := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck"}, FootprintX: 1, FootprintZ: 1, Damage: 10, Object: "wreck.3do"}
	terrain := &world.Terrain{CellW: 2, CellH: 2, Plot: make([]world.PlotCell, 4), FeatureNames: []string{"TREE", "BURNING", "WRECK"}, FeatureDefs: []*content.FeatureDef{normal, anim, model}}
	terrain.Plot[0].SetFeature(0)
	terrain.Plot[1].SetFeature(1)
	terrain.Plot[1].SetOccupied(true)
	terrain.Plot[2].SetFeature(2)
	terrain.Plot[3].SetFeature(world.PlotFeatureNone)
	s := NewService(terrain, nil, nil, nil)
	s.instances[1] = &Instance{Def: anim, Terrain: terrain, CX: 1, CZ: 0, IsAnimating: true, DamageAccumulator: 0x1234, AnimationFrame: 7, AnimationSelector: 2, AnimationCountdown: 0x0b}
	s.instances[2] = &Instance{
		Def: model, Terrain: terrain, CX: 0, CZ: 1, DamageAccumulator: 0x4567,
		X: numeric.Fixed(0x0001_0000), Y: numeric.Fixed(0x0002_0000), Z: numeric.Fixed(0x0003_0000),
		Orientation: Orientation{Bank: 0x0102, Heading: 0x0304, Pitch: 0x0506},
	}
	image, err := s.RetailFeatureImage()
	if err != nil {
		t.Fatal(err)
	}
	if len(image.Normal) != 1 || len(image.Animating) != 1 || len(image.ThreeD) != 1 {
		t.Fatalf("families = %d/%d/%d, want one each", len(image.Normal), len(image.Animating), len(image.ThreeD))
	}
	if image.Normal[0].TypeName != "TREE" || image.Animating[0].TypeName != "BURNING" || image.ThreeD[0].TypeName != "WRECK" {
		t.Fatalf("authored names were not retained: %#v", image)
	}
	if got := binary.LittleEndian.Uint16(image.Normal[0].Data[6:]); got != 0 {
		t.Fatalf("normal anchor word %#x, want 0", got)
	}
	if got := binary.LittleEndian.Uint16(image.Animating[0].Data[6:]); got != 0x1234 {
		t.Fatalf("animation state %#x, want 0x1234", got)
	}
	if image.Animating[0].Data[9] != 0xb2 {
		t.Fatalf("animation selector/countdown byte %#x, want 0xb2", image.Animating[0].Data[9])
	}
	// The 3D record's five values at their documented offsets
	// [08 R-SAVE-FEATURE-01].
	d := image.ThreeD[0].Data
	if got := binary.LittleEndian.Uint16(d[6:]); got != 0x4567 {
		t.Fatalf("3D accumulator %#x, want 0x4567", got)
	}
	if x, y, z := binary.LittleEndian.Uint32(d[0x08:]), binary.LittleEndian.Uint32(d[0x0c:]), binary.LittleEndian.Uint32(d[0x10:]); x != 0x00010000 || y != 0x00020000 || z != 0x00030000 {
		t.Fatalf("3D position words %#x/%#x/%#x, want the instance's 16.16 triple", x, y, z)
	}
	if b, h, p := binary.LittleEndian.Uint16(d[0x14:]), binary.LittleEndian.Uint16(d[0x16:]), binary.LittleEndian.Uint16(d[0x18:]); b != 0x0102 || h != 0x0304 || p != 0x0506 {
		t.Fatalf("3D orientation words %#x/%#x/%#x, want bank/heading/pitch 0x0102/0x0304/0x0506", b, h, p)
	}
}

// A wreck saved mid-descent reloads at its SAVED Y with zero velocity and stays
// suspended: velocity is not serialized and the reader does not re-derive the
// sinking latch from the height/sea-level relation
// [08 R-SAVE-FEATURE-01 "3D record restore, exactly"]. The position and
// orientation go through the stamp verbatim — no footprint centre, no terrain
// re-sample.
func TestThreeDRecordRoundTripsAWreckMidDescent(t *testing.T) {
	terrain := newTestTerrainP1(8, 8)
	terrain.SeaLevel = 30
	svc := NewService(terrain, nil, nil, nil)
	wreck := defP1("savewreck", 1, 1, "savewreck.3do", "")

	// Mid-descent: above the coarse floor (height byte 10) and below the sea.
	descending := numeric.Fixed(22*65536 + 0x4000)
	saved := svc.PlaceCorpse([3]numeric.Fixed{
		world.CellToWorld(3).Add(numeric.Fixed(5 * 65536)),
		descending,
		world.CellToWorld(4).Add(numeric.Fixed(7 * 65536)),
	}, Orientation{Bank: 0x00c8, Heading: 0x8000, Pitch: 0x4001}, wreck, false)
	if saved == nil {
		t.Fatal("corpse refused")
	}
	if !saved.IsSinking || saved.Vy == 0 {
		t.Fatalf("fixture is not mid-descent: sinking=%v vy=%d", saved.IsSinking, saved.Vy.Raw())
	}
	saved.DamageAccumulator = 0x0f0f

	image, err := svc.RetailFeatureImage()
	if err != nil {
		t.Fatal(err)
	}
	if len(image.ThreeD) != 1 {
		t.Fatalf("3D records = %d, want 1", len(image.ThreeD))
	}

	// Reload into a fresh world, the way battle entry does.
	reloaded := NewService(newTestTerrainP1(8, 8), nil, nil, nil)
	reloaded.Terrain.SeaLevel = 30
	back, err := reloaded.RestoreAt(3, 4, wreck, 2, image.ThreeD[0].Data)
	if err != nil || back == nil {
		t.Fatalf("restore: inst=%v err=%v", back, err)
	}
	if back.X != saved.X || back.Y != saved.Y || back.Z != saved.Z {
		t.Fatalf("restored position (%d,%d,%d), want the saved triple (%d,%d,%d) — the stamp must not recompute the centre or re-sample terrain",
			back.X.Raw(), back.Y.Raw(), back.Z.Raw(), saved.X.Raw(), saved.Y.Raw(), saved.Z.Raw())
	}
	if back.Orientation != saved.Orientation {
		t.Fatalf("restored orientation %+v, want %+v", back.Orientation, saved.Orientation)
	}
	if back.DamageAccumulator != 0x0f0f {
		t.Fatalf("restored accumulator %#x, want 0x0f0f", back.DamageAccumulator)
	}
	if back.Vy != 0 {
		t.Fatalf("restored velocity %d, want 0 — velocity is not serialized and the sink latch is not re-derived", back.Vy.Raw())
	}
	// And it stays there: the lifecycle pass has no velocity to integrate.
	reloaded.TickLifecycle(0)
	if back.Y != saved.Y {
		t.Fatalf("reloaded wreck descended to %d from %d; a wreck saved mid-descent stays suspended", back.Y.Raw(), saved.Y.Raw())
	}
}

func TestRetailFeatureImageRefusesMissingAnimationState(t *testing.T) {
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "animated"}, Filename: "x.gaf"}
	terrain := &world.Terrain{CellW: 1, CellH: 1, Plot: make([]world.PlotCell, 1), FeatureNames: []string{"ANIMATED"}, FeatureDefs: []*content.FeatureDef{def}}
	terrain.Plot[0].SetFeature(0)
	terrain.Plot[0].SetOccupied(true)
	if _, err := NewService(terrain, nil, nil, nil).RetailFeatureImage(); err == nil {
		t.Fatal("animated feature without live state accepted")
	}
}

func TestRetailFeatureImageSkipsNonFamilyAnimationSequence(t *testing.T) {
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "animated"}, Filename: "x.gaf"}
	terrain := &world.Terrain{CellW: 1, CellH: 1, Plot: make([]world.PlotCell, 1), FeatureNames: []string{"ANIMATED"}, FeatureDefs: []*content.FeatureDef{def}}
	terrain.Plot[0].SetFeature(0)
	terrain.Plot[0].SetOccupied(true)
	service := NewService(terrain, nil, nil, nil)
	service.instances[0] = &Instance{Def: def, Terrain: terrain, CX: 0, CZ: 0, IsAnimating: true, AnimationSelector: 7, DamageAccumulator: 0x9999}
	image, err := service.RetailFeatureImage()
	if err != nil {
		t.Fatal(err)
	}
	if len(image.Animating) != 0 || len(image.Normal) != 0 || len(image.ThreeD) != 0 {
		t.Fatalf("non-family animation was emitted: %#v", image)
	}
}
