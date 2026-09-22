package units

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestFacingFromHeadingRoundsToNearestQuarter(t *testing.T) {
	for _, tc := range []struct {
		heading uint16
		want    StructureFacing
	}{
		{32768, FacingSouth},
		{32768 + 8191, FacingSouth},
		{32768 + 8192, FacingEast},
		{65535, FacingNorth},
		{0, FacingNorth},
		{8192, FacingWest},
	} {
		if got := FacingFromHeading(tc.heading); got != tc.want {
			t.Fatalf("FacingFromHeading(%d)=%d, want %d", tc.heading, got, tc.want)
		}
	}
}

func TestCreateFacingOrientsInstanceWithoutMutatingDefinition(t *testing.T) {
	def := &content.UnitDef{
		UnitName: "rotated-lab", BMCode: 0, FootprintX: 2, FootprintZ: 3,
		YardMap: "cooooo", Rotations: content.FacingSouth | content.FacingEast,
		BuildAngle: 4096, MaxDamage: 100, Limit: -1,
	}
	original := *def
	eastRNG := rng.NewSimulation(7)
	w := newFixtureWorld(4, nil)
	w.SetSimulationRNG(&eastRNG)
	bound := false
	w.SetCOBBinder(func(u *Unit) error {
		bound = true
		if u.StructureFacing != FacingEast || u.FootprintSizeX != 3 || u.FootprintSizeZ != 2 {
			t.Fatalf("COB binder saw facing %d footprint %dx%d, want east 3x2", u.StructureFacing, u.FootprintSizeX, u.FootprintSizeZ)
		}
		return nil
	})
	h, err := w.CreateNanoframeFacing(def, 0, 0, 0, 0, FacingEast)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	if !bound {
		t.Fatal("oriented geometry was not observed at the pre-Create binding boundary")
	}
	if u.StructureFacing != FacingEast || u.FootprintSizeX != 3 || u.FootprintSizeZ != 2 {
		t.Fatalf("oriented instance = facing %d footprint %dx%d, want east 3x2", u.StructureFacing, u.FootprintSizeX, u.FootprintSizeZ)
	}
	southRNG := rng.NewSimulation(7)
	southWorld := newFixtureWorld(4, nil)
	southWorld.SetSimulationRNG(&southRNG)
	southHandle, err := southWorld.CreateNanoframe(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := u.Move.Heading, southWorld.Unit(southHandle).Move.Heading+16384; got != want {
		t.Fatalf("heading=%d, want unrotated heading plus one quarter %d", got, want)
	}
	if eastRNG.State != southRNG.State || eastRNG.Draws() != southRNG.Draws() {
		t.Fatalf("rotation changed RNG: east=(%d,%d) south=(%d,%d)", eastRNG.State, eastRNG.Draws(), southRNG.State, southRNG.Draws())
	}
	if !reflect.DeepEqual(*def, original) {
		t.Fatal("oriented creation mutated the shared UnitDef")
	}
}

func TestOrientedYardMapQuarterAndHalfTurns(t *testing.T) {
	def := &content.UnitDef{BMCode: 0, FootprintX: 2, FootprintZ: 3, YardMap: "cyogCY"}
	south, err := OrientedYardMap(def, FacingSouth)
	if err != nil {
		t.Fatal(err)
	}
	east, err := OrientedYardMap(def, FacingEast)
	if err != nil {
		t.Fatal(err)
	}
	north, err := OrientedYardMap(def, FacingNorth)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := east, []world.YardCell{south[1], south[3], south[5], south[0], south[2], south[4]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("east yard=%v, want %v", got, want)
	}
	if got, want := north, []world.YardCell{south[5], south[4], south[3], south[2], south[1], south[0]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("north yard=%v, want %v", got, want)
	}
}
