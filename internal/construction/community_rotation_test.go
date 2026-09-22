package construction

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestStructureGeometryStrictBypassAndCommunitySelection(t *testing.T) {
	def := &content.UnitDef{
		UnitName: "rotatable", BMCode: 0, FootprintX: 2, FootprintZ: 3,
		YardMap: "cooooo", Rotations: content.FacingSouth | content.FacingEast,
	}
	original := *def
	node := orders.NewMobileBuildNode(nil, def.UnitName, 0, 0, uint16(units.FacingEast), 1, 0, 1, false)
	if node.BuildFacing != units.FacingEast || node.Param3 != 0 {
		t.Fatalf("order facing/retry = %d/%d, want east/zero", node.BuildFacing, node.Param3)
	}
	for _, tc := range []struct {
		name  string
		rules Rules
		on    bool
		want  units.StructureFacing
	}{
		{"strict", StrictRules{}, true, units.FacingSouth},
		{"community-disabled", CommunityRules{}, false, units.FacingSouth},
		{"community-enabled", CommunityRules{}, true, units.FacingEast},
		{"modern-inherits", &ModernRules{}, true, units.FacingEast},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{Rules: tc.rules, Community: community.Features{StructureRotation: tc.on}}
			geometry, err := s.StructureGeometry(def, units.FacingEast)
			if err != nil {
				t.Fatal(err)
			}
			if geometry.Facing != tc.want {
				t.Fatalf("facing=%d, want %d", geometry.Facing, tc.want)
			}
			wantX, wantZ := int32(2), int32(3)
			if tc.want == units.FacingEast {
				wantX, wantZ = 3, 2
			}
			if geometry.FootprintX != wantX || geometry.FootprintZ != wantZ {
				t.Fatalf("footprint=%dx%d, want %dx%d", geometry.FootprintX, geometry.FootprintZ, wantX, wantZ)
			}
			fromHeading, err := s.StructureGeometryFromHeading(def, 32768+16384)
			if err != nil {
				t.Fatal(err)
			}
			if fromHeading.Facing != tc.want {
				t.Fatalf("heading-derived facing=%d, want %d", fromHeading.Facing, tc.want)
			}
		})
	}
	if !reflect.DeepEqual(*def, original) {
		t.Fatal("geometry selection mutated the shared UnitDef")
	}
	queued := &Service{Rules: CommunityRules{}, Community: community.Features{StructureRotation: true}}
	selected, err := queued.StructureGeometry(def, units.FacingEast)
	if err != nil {
		t.Fatal(err)
	}
	queued.Rules = StrictRules{}
	retained, err := queued.structureGeometryApplied(def, selected.Facing)
	if err != nil {
		t.Fatal(err)
	}
	if retained.Facing != units.FacingEast {
		t.Fatalf("rule rebind rewrote queued facing to %d", retained.Facing)
	}
}

func TestRotatedLiveYardOpenUsesCreationGeometry(t *testing.T) {
	terrain := completedOccupancyTerrain(16, 16)
	def := &content.UnitDef{
		UnitName: "rotated-yard", BMCode: 0, FootprintX: 2, FootprintZ: 3,
		YardMap: "cooooo", Rotations: content.FacingSouth | content.FacingEast,
	}
	u := &units.Unit{
		Handle: pool.Handle(7), Def: def, Alive: true,
		X: numeric.FixedFromInt(96), Z: numeric.FixedFromInt(96),
		StructureFacing: units.FacingEast,
		FootprintSizeX:  3, FootprintSizeZ: 2,
	}
	s := NewService(terrain, nil, nil, nil)
	if err := s.RegisterBuildingPlacement(u); err != nil {
		t.Fatal(err)
	}
	rect, ok := s.PlacementForProduct(u.Handle)
	if !ok || rect.Width() != 3 || rect.Depth() != 2 {
		t.Fatalf("live placement=%v %dx%d, want present 3x2", ok, rect.Width(), rect.Depth())
	}
	geometry, err := s.StructureGeometryForUnit(u)
	if err != nil {
		t.Fatal(err)
	}
	closedOnly := -1
	for i, cell := range geometry.Yard {
		if cell.Selects(false) && !cell.Selects(true) {
			closedOnly = i
			break
		}
	}
	if closedOnly < 0 {
		t.Fatal("fixture has no closed-only yard cell")
	}
	x := rect.MinX() + int32(closedOnly)%rect.Width()
	z := rect.MinZ() + int32(closedOnly)/rect.Width()
	if got := terrain.PlotAt(x, z).OccupantA(); got != int16(u.Handle) {
		t.Fatalf("rotated closed cell %d,%d occupant=%d, want %d", x, z, got, u.Handle)
	}
	if !s.YardOpenTransactionAt(u, true, 1) {
		t.Fatal("rotated live yard refused to open")
	}
	if got := terrain.PlotAt(x, z).OccupantA(); got != 0 {
		t.Fatalf("rotated open cell %d,%d occupant=%d, want released", x, z, got)
	}
}
