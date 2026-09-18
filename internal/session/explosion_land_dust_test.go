package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestExplosionLandDustFollowsTheAllocatorsGate locks the explosion-pool
// allocator's land-dust gate [06 R-WFX-01 §2 step 4][06 R-WFX-01 §5]: the
// strip-9 dust puffer is appended only when the water flag the allocator was
// handed is CLEAR and the point's whole Y word is STRICTLY above the sea-level
// byte. No weapon flag appears in that gate: the weapon's start-smoke flag
// gates the muzzle puff instead, and the explosion event no longer carries it.
//
// The last row is the one the event kind cannot express: a direct hit on a unit
// standing in a water cell takes the land arm (hit sound, land art holder, so
// EventExplosion, not EventWaterExplosion), and the central impact still hands
// the allocator the raised water-CELL flag, so it raises no dust [06 §13.2].
func TestExplosionLandDustFollowsTheAllocatorsGate(t *testing.T) {
	const seaLevel = 10
	// Cell (4,4) is dry land, cell (8,4) is under water. Y is compared against
	// the sea byte as a whole word, so 11 is above the plane and 10 is not.
	const (
		landCell  = 4
		waterCell = 8
	)
	for _, tc := range []struct {
		name string
		cell int32
		kind combat.EventKind
		y    int64
		want bool
	}{
		{"land cell, point above sea level", landCell, combat.EventExplosion, 11, true},
		{"land cell, point at sea level", landCell, combat.EventExplosion, seaLevel, false},
		{"water cell, point above sea level", waterCell, combat.EventWaterExplosion, 11, false},
		{"water cell, point below sea level", waterCell, combat.EventWaterExplosion, 2, false},
		// The direct hit: a land-arm explosion event inside a water cell.
		{"direct hit on a unit in a water cell", waterCell, combat.EventExplosion, 11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := strictNewSessionWithUnits(t, 0, 17, 19)
			if s == nil || s.Combat == nil || s.strips == nil {
				t.Fatal("strict session did not compose combat presentation")
			}
			s.World.SeaLevel = seaLevel
			s.World.PlotAt(waterCell, 4).SetMaxHeight(seaLevel - 1)

			x := world.CellToWorld(tc.cell) + numeric.FixedFromInt(1)
			z := world.CellToWorld(4) + numeric.FixedFromInt(1)
			before := len(s.strips.strips[9])
			s.Combat.Events(combat.Event{
				Kind: tc.kind, Tick: 7, Source: 4,
				Position: combat.Vec3{X: x, Y: numeric.FixedFromInt(tc.y), Z: z},
			})
			got := len(s.strips.strips[9]) > before
			if got != tc.want {
				t.Fatalf("land dust raised=%v, want %v [06 R-WFX-01 §2 step 4]", got, tc.want)
			}
		})
	}
}
