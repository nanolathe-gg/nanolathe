//go:build pathbench && retail

package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func init() {
	pbRegister(
		pbCase{"naval-strait-patrol", "naval", "Patrol boats traverse a 20-cell deep-water strait", []int{1, 8}, 1200, pbNaval("strait", "armpt")},
		pbCase{"naval-strait-cruiser", "naval", "Larger cruisers traverse the same strait", []int{1, 8}, 1400, pbNaval("strait", "armcrus")},
		pbCase{"naval-four-cell-patrol", "naval", "Four-cell passage admits patrol boat footprint", []int{1}, 1000, pbNaval("narrow", "armpt")},
		pbCase{"naval-four-cell-cruiser", "naval", "Four-cell passage excludes five-cell cruiser footprint", []int{1}, 800, pbNaval("narrow", "armcrus")},
		pbCase{"naval-shallow-surface", "naval", "Surface ship traverses ten-depth shallows", []int{1, 8}, 1200, pbNaval("shallow", "armpt")},
		pbCase{"naval-shallow-sub", "naval", "Submarine detours around ten-depth shallows", []int{1, 8}, 1400, pbNaval("shallow", "armsub")},
		pbCase{"naval-islands", "naval", "Patrol boats thread several land islands", []int{1, 8}, 1500, pbNaval("islands", "armpt")},
		pbCase{"naval-shore-hover", "naval", "Hovercraft crosses water and shoreline", []int{1, 8}, 1200, pbNaval("shore", "armsh")},
		pbCase{"naval-shore-amphibious", "naval", "Amphibious walker crosses water and shoreline", []int{1, 8}, 1300, pbNaval("shore", "armseap")},
		pbCase{"naval-mixed-locomotion", "naval", "Surface, hover and amphibious profiles share a shoreline command", []int{3}, 1500, pbNavalMixed},
	)
}

// Land is authored by changing raw heights and their derived extrema together.
func pbLand(t *world.Terrain, x0, x1, z0, z1 int32) { pbRidge(t, x0, x1, z0, z1, 100) }

func pbShore(t *world.Terrain) {
	pbLand(t, 70, 144, 0, 96)
	for x := int32(63); x < 70; x++ {
		pbRidge(t, x, x+1, 0, 96, uint8(20+(x-62)*10))
	}
}

func pbNaval(kind, key string) func(*testing.T, string, int) *pbScene {
	return func(t *testing.T, rules string, size int) *pbScene {
		ter := pbTerrain(t, 144, 96, 20, 80)
		switch kind {
		case "strait":
			pbLand(ter, 34, 100, 0, 40)
			pbLand(ter, 34, 100, 60, 96)
		case "narrow":
			pbLand(ter, 60, 68, 0, 44)
			pbLand(ter, 60, 68, 49, 96)
		case "shallow":
			pbRidge(ter, 54, 62, 20, 76, 70)
		case "islands":
			pbLand(ter, 36, 52, 20, 65)
			pbLand(ter, 70, 86, 35, 80)
			pbLand(ter, 102, 115, 13, 58)
		case "shore":
			pbShore(ter)
		}
		sc := pbNew(t, rules, ter)
		actors := make([]*pbActor, 0, size)
		for i := 0; i < size; i++ {
			actors = append(actors, pbAdd(t, sc, key, "cross", 0, 10+int32(i%4)*7, 42+int32(i/4)*8))
		}
		goalX := int32(126)
		goalZ := int32(48)
		if kind == "shore" {
			goalX = 110
		}
		if kind == "narrow" {
			goalZ = 44
		}
		pbAssertRoute(t, sc, actors[0], goalX, goalZ, !(kind == "narrow" && key == "armcrus"))
		pbMove(t, sc, actors, goalX, goalZ, false, false)
		sc.Regions = []pbRegion{{Name: "center-crossing", X0: 52, Z0: 18, X1: 87, Z1: 78}, {Name: "north-detour", X0: 50, Z0: 5, X1: 90, Z1: 20}, {Name: "south-detour", X0: 50, Z0: 76, X1: 90, Z1: 91}}
		if kind == "narrow" && key == "armcrus" {
			sc.Notes = append(sc.Notes, "Goal intentionally unreachable through the four-cell slit for a five-cell footprint")
		}
		if kind == "shore" && key != "armsh" && key != "armseap" {
			t.Fatal("shore fixture requires hover or amphibious profile")
		}
		p := sc.S.Movement.ProfileFor(actors[0].Handle)
		if kind == "narrow" && p.IsPassableFootprint(ter, 61, 44) != (key == "armpt") {
			t.Fatalf("four-cell slit does not distinguish %s", key)
		}
		if kind == "shallow" && p.IsPassableFootprint(ter, 56, 40) != (key == "armpt") {
			t.Fatalf("ten-depth shallows do not distinguish %s", key)
		}
		if kind == "shore" && !p.IsPassableFootprint(ter, 90, 48) {
			t.Fatalf("%s cannot occupy authored shore goal", key)
		}
		sc.Inputs = append(sc.Inputs, fmt.Sprintf("synthetic sea=80, base height=20 (depth 60), %s geometry; %s footprint %d,%d depth limits %d,%d", kind, key, p.FootPrintX, p.FootPrintZ, p.MaxWaterDepth, p.MinWaterDepth))
		return sc
	}
}

func pbNavalMixed(t *testing.T, rules string, size int) *pbScene {
	ter := pbTerrain(t, 144, 96, 20, 80)
	pbShore(ter)
	sc := pbNew(t, rules, ter)
	actors := []*pbActor{pbAdd(t, sc, "armpt", "surface", 0, 10, 38), pbAdd(t, sc, "armsh", "hover", 0, 10, 48), pbAdd(t, sc, "armseap", "amphibious", 0, 10, 58)}
	pbAssertRoute(t, sc, actors[0], 110, 48, false)
	pbAssertRoute(t, sc, actors[1], 110, 48, true)
	pbAssertRoute(t, sc, actors[2], 110, 48, true)
	if size != len(actors) {
		t.Fatalf("mixed size %d unsupported", size)
	}
	pbMove(t, sc, actors, 110, 48, false, false)
	sc.Regions = []pbRegion{{Name: "shoreline", X0: 65, Z0: 20, X1: 78, Z1: 76}, {Name: "land-goal", X0: 100, Z0: 35, X1: 125, Z1: 65}}
	sc.Notes = append(sc.Notes, "Surface ship has intentionally impossible land goal; hover and amphibious profiles have feasible land routes")
	sc.Inputs = append(sc.Inputs, "sea=80, water height=20, ramp x=[63,70) +10 per cell, land x=[70,144) height=100; one mixed group order to (110,48)")
	return sc
}
