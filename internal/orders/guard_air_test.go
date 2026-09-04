package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The air twin `VTOL_Follow` of [04 R-ORD-02 §3], whose leg 4 is what
// [04 R-UNIT-06 §1] calls "airspace circling (radius `0x80` arrival)". Every
// number asserted here is that section's: phase 0 draws ONE `RNG(0x10000)` into
// p1 with its low bit into p2, every visit copies the ward's position into the
// record goal, and the maintenance leg installs an air marker at
// `wardPos − offset(p1, r)` with arrival radius `0x80`, radius `r` the slot-0
// weapon `Range + 0xA0` for an armed guard and a flat 320 otherwise, on a
// deadline-30 cadence behind gate `0xF8`.

type airGuardFixture struct {
	guard *units.Unit
	ward  *units.Unit
	sim   *rng.Simulation
	air   []AirGoalRequest
}

// newAirGuardFixture builds an already-airborne air guard and a ground ward
// that declines every assist leg, so each phase-1 visit reaches the orbit.
func newAirGuardFixture(t *testing.T) *airGuardFixture {
	t.Helper()
	f := &airGuardFixture{}
	f.guard = &units.Unit{
		Handle: 1,
		Def: &content.UnitDef{
			UnitName: "fighter", CanFly: true, CanMove: true, BMCode: true,
			FootprintX: 1, FootprintZ: 1, MaxDamage: 100, CruiseAlt: 120,
		},
		Alive: true,
		X:     numeric.Fixed(200 << 16),
		Y:     numeric.Fixed(150 << 16),
		Z:     numeric.Fixed(300 << 16),
	}
	f.guard.MaxHealth, f.guard.Health = 100, 100
	// Committed airborne (mode 2): the preamble's takeoff arm is a phase-0
	// concern of [04 R-AIR-01 §6] and not this row's subject.
	f.guard.Move.Mode = 2
	f.ward = &units.Unit{
		Handle: 2,
		Def:    &content.UnitDef{UnitName: "ward", FootprintX: 1, FootprintZ: 1, MaxDamage: 100},
		Alive:  true,
		X:      numeric.Fixed(500 << 16),
		Y:      numeric.Fixed(11 << 16),
		Z:      numeric.Fixed(700 << 16),
	}
	f.ward.MaxHealth, f.ward.Health = 100, 100
	f.ward.Remaining = 0

	sim := rng.NewSimulation(7)
	f.sim = &sim
	binding := &QueueBinding{
		SimRNG: f.sim,
		Lookup: func(h pool.Handle) *units.Unit {
			if h == 2 {
				return f.ward
			}
			return nil
		},
		Hostility: func(_, _ *units.Unit) bool { return false },
		Movement: &MovementGoalAdapter{
			InstallAir: func(req AirGoalRequest) bool {
				f.air = append(f.air, req)
				return true
			},
			InstallPoint: func(PointGoalRequest) bool {
				t.Fatal("the air guard must not install a GROUND point goal [04 R-UNIT-06 §1]")
				return false
			},
			Release: func(*Node) bool { return true },
		},
	}
	QueueForUnit(f.guard).SetBinding(binding)
	QueueForUnit(f.ward).SetBinding(&QueueBinding{})
	return f
}

func airGuardNode(f *airGuardFixture) *Node {
	return &Node{ID: Lookup("VTOL_Follow"), Owner: f.guard.Handle, Target: f.ward.Handle, Deadline: -1}
}

// wantOrbitPoint is `wardPos − offset(p1, r)`, the leg-4 expression, formed
// from the same helper the handler uses so the assertion is on the SIGN and the
// operands rather than on a re-derivation of the sine table.
func wantOrbitPoint(bearing uint32, r int32, ward *units.Unit) (numeric.Fixed, numeric.Fixed) {
	ox, oz := bearingOffset(uint16(bearing), numeric.Fixed(int64(r)<<16))
	return ward.X - ox, ward.Z - oz
}

// TestVTOLFollowAdmitDrawsTheOrbitBearing locks phase 0 of [04 R-ORD-02 §3]:
// one `RNG(0x10000)` into p1, its low bit into p2, and the entry's copy of the
// ward's position into the record goal — the air row keeps a POSITION where the
// ground row keeps the anchor OFFSET [04 R-ORD-01 §8 point 2].
func TestVTOLFollowAdmitDrawsTheOrbitBearing(t *testing.T) {
	f := newAirGuardFixture(t)
	n := airGuardNode(f)

	shadow := rng.NewSimulation(7)
	wantBearing := shadow.Uint32n(0x10000)

	before := f.sim.Draws()
	if code := guardHandler(f.guard, n, 0, 100); code != Code(1) {
		t.Fatalf("air admit want *advance* (1), got %d", code)
	}
	if got := f.sim.Draws() - before; got != 1 {
		t.Fatalf("air admit draws = %d, want exactly one RNG(0x10000) [04 R-ORD-02 §3][I4]", got)
	}
	if n.Param1 != wantBearing {
		t.Fatalf("p1 = %d, want the drawn orbit bearing %d", n.Param1, wantBearing)
	}
	if n.Param2 != wantBearing&1 {
		t.Fatalf("p2 = %d, want the bearing's low bit %d", n.Param2, wantBearing&1)
	}
	if n.GoalX != f.ward.X || n.GoalY != f.ward.Y || n.GoalZ != f.ward.Z {
		t.Fatalf("record goal = (%v,%v,%v), want the ward's position (%v,%v,%v) [04 R-ORD-02 §3]",
			n.GoalX, n.GoalY, n.GoalZ, f.ward.X, f.ward.Y, f.ward.Z)
	}
}

// TestVTOLFollowOrbitsAMovingWard is the unit's own contract: an air guard
// following a moving ward re-installs the circling marker with arrival radius
// `0x80`, re-anchored on the ward each time, on the 30-tick cadence.
func TestVTOLFollowOrbitsAMovingWard(t *testing.T) {
	f := newAirGuardFixture(t)
	n := airGuardNode(f)
	if code := guardHandler(f.guard, n, 0, 100); code != Code(1) {
		t.Fatalf("air admit want *advance* (1), got %d", code)
	}
	bearing := n.Param1
	// *advance* is the pump's to apply; the fixture dispatches the handler
	// directly, so the phase is stepped here.
	n.Phase = 1

	// Visit one, no arrival bits: the leg installs, takes no draw, and holds.
	n.DynamicGate = 0
	before := f.sim.Draws()
	if code := guardHandler(f.guard, n, 0, 200); code != Code(2) {
		t.Fatalf("orbit want *hold* (2), got %d", code)
	}
	if got := f.sim.Draws() - before; got != 0 {
		t.Fatalf("an orbit visit with no arrival draws %d times, want 0 [04 R-ORD-02 §3][I4]", got)
	}
	if len(f.air) != 1 {
		t.Fatalf("air installs = %d, want one marker per visit", len(f.air))
	}
	first := f.air[0]
	if first.Radius != 0x80 {
		t.Fatalf("arrival radius = %d, want 0x80 [04 R-UNIT-06 §1][04 R-ORD-02 §3]", first.Radius)
	}
	if first.Node != n || first.Owner != f.guard.Handle {
		t.Fatalf("the marker must carry the guard's own record identity, got owner %d node %p", first.Owner, first.Node)
	}
	if first.Target != 0 {
		t.Fatalf("leg 4 installs a POINT marker, not a follow-unit marker: target = %d", first.Target)
	}
	// Unarmed: the flat 320, not a weapon range the guard does not have.
	wantX, wantZ := wantOrbitPoint(bearing, 320, f.ward)
	if first.X != wantX || first.Z != wantZ {
		t.Fatalf("marker = (%v,%v), want ward − offset(p1, 320) = (%v,%v) [04 R-ORD-02 §3]", first.X, first.Z, wantX, wantZ)
	}
	if n.Deadline != int32(200+30) {
		t.Fatalf("deadline = %d, want tick+30 = 230 [04 R-ORD-02 §3]", n.Deadline)
	}
	if n.DynamicGate != 0xF8|1 {
		t.Fatalf("gate = %#x, want 0xF8 plus the deadline setter's bit 0 [04 R-ORD-02 §3][04 R-ORD-01 §1]", n.DynamicGate)
	}
	if n.Phase != 1 {
		t.Fatalf("the orbit holds with the phase left where it was, got %d", n.Phase)
	}

	// The ward moves and the marker's arrival lands: the bearing steps
	// subtractively by 0x4000 + RNG(0x2000) and the marker follows the ward.
	f.ward.X = numeric.Fixed(900 << 16)
	f.ward.Z = numeric.Fixed(120 << 16)
	shadow := rng.NewSimulation(7)
	shadow.Uint32n(0x10000) // the admit draw
	wantBearing := uint32(uint16(bearing) - uint16(0x4000+shadow.Uint32n(0x2000)))

	n.DynamicGate = 0
	before = f.sim.Draws()
	if code := guardHandler(f.guard, n, 0xE0, 230); code != Code(2) {
		t.Fatalf("second orbit visit want *hold* (2), got %d", code)
	}
	if got := f.sim.Draws() - before; got != 1 {
		t.Fatalf("an arrival visit draws %d times, want exactly one RNG(0x2000) [04 R-ORD-02 §3]", got)
	}
	if n.Param1 != wantBearing {
		t.Fatalf("stepped bearing = %d, want p1 − (0x4000 + RNG(0x2000)) = %d", n.Param1, wantBearing)
	}
	if len(f.air) != 2 {
		t.Fatalf("air installs = %d, want a fresh marker on the second visit", len(f.air))
	}
	second := f.air[1]
	if second.Radius != 0x80 {
		t.Fatalf("second arrival radius = %d, want 0x80", second.Radius)
	}
	wantX, wantZ = wantOrbitPoint(wantBearing, 320, f.ward)
	if second.X != wantX || second.Z != wantZ {
		t.Fatalf("second marker = (%v,%v), want the MOVED ward − offset = (%v,%v)", second.X, second.Z, wantX, wantZ)
	}
	if n.Deadline != int32(230+30) {
		t.Fatalf("second deadline = %d, want 260 — the cadence is a fixed 30", n.Deadline)
	}
	// It circles rather than hovering on the ward: the marker stands off by the
	// orbit radius, up to the sine table's own quantisation.
	dx := int64((second.X - f.ward.X).Raw() >> 16)
	dz := int64((second.Z - f.ward.Z).Raw() >> 16)
	if d2 := dx*dx + dz*dz; d2 < 319*319 || d2 > 321*321 {
		t.Fatalf("stand-off² = %d, want ≈ 320² = %d [04 R-ORD-02 §3]", d2, 320*320)
	}
}

// TestVTOLFollowOrbitRadiusReadsTheArmedBit locks the radius fork: "r = my
// slot-0 weapon `Range + 0xA0` world units when my state word has bit 31, else
// 320" [04 R-ORD-02 §3]. Bit 31 is the armed bit [04 R-ORD-01 §3], carried here
// as units.ArmedStatus.
func TestVTOLFollowOrbitRadiusReadsTheArmedBit(t *testing.T) {
	f := newAirGuardFixture(t)
	f.guard.InstallWeapon(0, &content.WeaponDef{Name: "cannon", Range: 400})
	f.guard.Flags |= units.ArmedStatus

	n := airGuardNode(f)
	if code := guardHandler(f.guard, n, 0, 100); code != Code(1) {
		t.Fatalf("air admit want *advance* (1), got %d", code)
	}
	n.Phase = 1
	n.DynamicGate = 0
	if code := guardHandler(f.guard, n, 0, 200); code != Code(2) {
		t.Fatalf("orbit want *hold* (2), got %d", code)
	}
	if len(f.air) != 1 {
		t.Fatalf("air installs = %d, want one", len(f.air))
	}
	wantX, wantZ := wantOrbitPoint(n.Param1, 400+airFollowOrbitBonus, f.ward)
	if f.air[0].X != wantX || f.air[0].Z != wantZ {
		t.Fatalf("armed marker = (%v,%v), want ward − offset(p1, Range + 0xA0) = (%v,%v)",
			f.air[0].X, f.air[0].Z, wantX, wantZ)
	}

	// Clearing the bit puts the same guard back on the flat 320, so the fork is
	// the armed bit and not the mere presence of a weapon.
	f.guard.Flags &^= units.ArmedStatus
	f.air = nil
	n.DynamicGate = 0
	if code := guardHandler(f.guard, n, 0, 230); code != Code(2) {
		t.Fatalf("orbit want *hold* (2), got %d", code)
	}
	wantX, wantZ = wantOrbitPoint(n.Param1, 320, f.ward)
	if len(f.air) != 1 || f.air[0].X != wantX || f.air[0].Z != wantZ {
		t.Fatalf("unarmed marker must fall back to the flat 320 [04 R-ORD-02 §3]")
	}
}
