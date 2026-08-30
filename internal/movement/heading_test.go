package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestHeadingFromDeltaCardinals locks the retail heading convention at the
// four cardinals [04 R-MOV-01 §4]: the position step is (-sin h, -cos h), so
// the heading that travels along a delta is 0 for -Z, 32768 for +Z, 16384 for
// -X and 49152 for +X. Before the convention was corrected every one of these
// was 180 degrees off, which left every rendered facing backwards while
// movement still reached its goal.
//
// The bisection lands within one unit of the exact bearing (see the function's
// own note on [04 R-MOV-01 §2]), so the assertion is the wrapped difference,
// not equality. One unit of slack cannot hide a half-turn.
func TestHeadingFromDeltaCardinals(t *testing.T) {
	cases := []struct {
		name   string
		dx, dz int64
		want   uint16
	}{
		{"-Z up-screen", 0, -1 << 16, 0},
		{"-X", -1 << 16, 0, 16384},
		{"+Z down-screen", 0, 1 << 16, 32768},
		{"+X", 1 << 16, 0, 49152},
	}
	for _, tc := range cases {
		got := headingFromDelta(tc.dx, tc.dz)
		off := int32(int16(got - tc.want))
		if off < -1 || off > 1 {
			t.Errorf("headingFromDelta(%s) = %d, want %d (+/-1) [04 R-MOV-01 §4]", tc.name, got, tc.want)
		}
	}
}

// TestSteerIntegrateStepsAgainstTrigTable locks the sign of the position step
// itself [04 R-MOV-01 §4]: heading 0 moves -Z and heading 16384 moves -X, with
// the negation applied after the round-to-nearest shift.
func TestSteerIntegrateStepsAgainstTrigTable(t *testing.T) {
	const speed = 2 << 16

	north := &SteerState{Heading: 0, Speed: speed}
	north.Integrate()
	if north.X != 0 {
		t.Errorf("heading 0 stepped X by %d, want 0 [04 R-MOV-01 §4]", north.X)
	}
	if north.Z >= 0 {
		t.Errorf("heading 0 stepped Z by %d, want a step toward -Z [04 R-MOV-01 §4]", north.Z)
	}
	wantZ := -int32((int64(numeric.Cos(0))*int64(speed) + 0x1000) >> 13)
	if north.Z != wantZ {
		t.Errorf("heading 0 stepped Z by %d, want %d [04 R-MOV-01 §4]", north.Z, wantZ)
	}

	west := &SteerState{Heading: 16384, Speed: speed}
	west.Integrate()
	if west.X >= 0 {
		t.Errorf("heading 16384 stepped X by %d, want a step toward -X [04 R-MOV-01 §4]", west.X)
	}
	if west.Z != 0 {
		t.Errorf("heading 16384 stepped Z by %d, want 0 [04 R-MOV-01 §4]", west.Z)
	}
}

// TestEnsureUnitKeepsAllocatedHeading locks C3: the mover records start from
// the heading the allocator already wrote from `buildangle` [04 §2.3b], so a
// tick that installs no route cannot commit a zero back over it. The old
// records were built with Heading: 0 and the first movement step published
// that zero, which is why every finished unit faced up-screen.
func TestEnsureUnitKeepsAllocatedHeading(t *testing.T) {
	const allocated uint16 = 33000 // inside the stock buildangle=4096 band [04 §2.3b]

	terrain := syntheticTerrainForIntegrate()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}
	system := NewSystem(terrain, profile, NewOccupancyGrid())

	w := newMovementFixtureWorld(10)
	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 3 * 65536, Acceleration: 3 * 65536, BrakeRate: 3 * 65536, TurnRate: 500}
	def.MaxDamage = 100
	def.FootprintX, def.FootprintZ = 1, 1
	x := world.CellToWorld(4)
	z := world.CellToWorld(4)
	h, err := w.Create(def, 0, x, terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	u.Move.Heading = allocated

	system.EnsureUnit(u)
	if got := system.Steers[h].Heading; got != allocated {
		t.Fatalf("EnsureUnit seeded SteerState.Heading = %d, want %d [04 §2.3b]", got, allocated)
	}
	if got := system.Collisions[h].Heading; got != allocated {
		t.Fatalf("EnsureUnit seeded CollisionState.Heading = %d, want %d [04 §2.3b]", got, allocated)
	}

	runMovementTick(system, 1, w)
	if u.Move.Heading != allocated {
		t.Fatalf("one step with no route published heading %d, want the allocated %d [04 §2.3b]", u.Move.Heading, allocated)
	}
}
