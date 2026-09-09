package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
			UnitName: "fighter", CanFly: true, CanMove: true, BMCode: 1,
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
	// directly, so the phase is stepped here. The air row has a phase 1 of its
	// own — "inhibit all three slots; advance" [04 R-ORD-02 §3] — so the legs
	// are reached at phase 2.
	n.Phase = 1
	if code := guardHandler(f.guard, n, 0, 150); code != Code(1) {
		t.Fatalf("air phase 1 want *advance* (1), got %d [04 R-ORD-02 §3]", code)
	}
	if len(f.air) != 0 {
		t.Fatalf("the slot-inhibit phase installs no marker, got %d", len(f.air))
	}
	n.Phase = 2

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
	if n.Phase != 2 {
		t.Fatalf("the orbit holds with the phase left where it was, got %d", n.Phase)
	}

	// The ward moves and the marker's arrival lands: the bearing steps
	// subtractively by 0x4000 + RNG(0x2000) and the marker follows the ward.
	//
	// The satisfied word here is `0x20` — *arrived* — and not the whole `0xE0`
	// the leg's own gate names, because the entry pre-check of [04 R-ORD-02 §3]
	// ends the order on `0x48`, and `0x40` is in both sets: the cannot-get-there
	// signal hands the record to `VTOL_SeekGuard` before leg 4 is ever reached.
	// So of `0xE0`'s three bits only `0x20` and `0x80` can reach the orbit step.
	f.ward.X = numeric.Fixed(900 << 16)
	f.ward.Z = numeric.Fixed(120 << 16)
	shadow := rng.NewSimulation(7)
	shadow.Uint32n(0x10000) // the admit draw
	wantBearing := uint32(uint16(bearing) - uint16(0x4000+shadow.Uint32n(0x2000)))

	n.DynamicGate = 0
	before = f.sim.Draws()
	if code := guardHandler(f.guard, n, 0x20, 230); code != Code(2) {
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
	if code := guardHandler(f.guard, n, 0, 150); code != Code(1) {
		t.Fatalf("air phase 1 want *advance* (1), got %d [04 R-ORD-02 §3]", code)
	}
	n.Phase = 2
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

// TestVTOLFollowSlotInhibitPhase locks §3's separate phase 1 — "inhibit all
// three slots; advance" — and the phase renumbering it forces
// [04 R-ORD-02 §3]. The ground row keeps its two-phase shape
// [04 R-UNIT-06 §1], which is what the last assertion checks.
func TestVTOLFollowSlotInhibitPhase(t *testing.T) {
	f := newAirGuardFixture(t)
	for idx := 0; idx < units.NumSlots; idx++ {
		f.guard.InstallWeapon(idx, &content.WeaponDef{Name: "cannon", Range: 400})
		// *release* the slot, so the inhibit below has something to do: bit 4
		// SET means the slot belongs to autonomous acquisition, and *inhibit*
		// hands it back [04 R-UNIT-06 §5 part 3].
		f.guard.Slots[idx].Flags &^= units.SlotFlagAutonomous
	}
	n := airGuardNode(f)
	if code := guardHandler(f.guard, n, 0, 100); code != Code(1) {
		t.Fatalf("air admit want *advance* (1), got %d", code)
	}
	// Phase 0 does NOT inhibit for the air row: the ground row runs the walk
	// inside its admit, the air row in a phase of its own [04 R-ORD-02 §3].
	for idx := 0; idx < units.NumSlots; idx++ {
		if f.guard.Slots[idx].Flags&units.SlotFlagAutonomous != 0 {
			t.Fatalf("slot %d was inhibited by the air ADMIT phase [04 R-ORD-02 §3]", idx)
		}
	}

	n.Phase = 1
	before := f.sim.Draws()
	if code := guardHandler(f.guard, n, 0, 150); code != Code(1) {
		t.Fatalf("air phase 1 want *advance* (1), got %d [04 R-ORD-02 §3]", code)
	}
	if got := f.sim.Draws() - before; got != 0 {
		t.Fatalf("the slot-inhibit phase draws %d times, want 0 [I4]", got)
	}
	for idx := 0; idx < units.NumSlots; idx++ {
		if f.guard.Slots[idx].Flags&units.SlotFlagAutonomous == 0 {
			t.Fatalf("slot %d not inhibited by air phase 1 [04 R-ORD-02 §3]", idx)
		}
	}
	if len(f.air) != 0 {
		t.Fatalf("the slot-inhibit phase installs no marker, got %d", len(f.air))
	}

	// Phase 2 is where the legs are, and phase 3 is "other phase: cancel-all".
	n.Phase = 2
	n.DynamicGate = 0
	if code := guardHandler(f.guard, n, 0, 200); code != Code(2) {
		t.Fatalf("air phase 2 want the orbit's *hold* (2), got %d", code)
	}
	n.Phase = 3
	if code := guardHandler(f.guard, n, 0, 230); code != Code(7) {
		t.Fatalf("air phase 3 want *cancel-all* (7), got %d [04 R-ORD-02 §3]", code)
	}

	// The ground row is untouched: its legs are still phase 1 and a phase byte
	// beyond 1 still cancels all [04 R-UNIT-06 §1].
	ground := &units.Unit{
		Handle: 3,
		Def:    &content.UnitDef{UnitName: "tank", CanMove: true, FootprintX: 1, FootprintZ: 1, MaxDamage: 100},
		Alive:  true,
	}
	ground.MaxHealth, ground.Health = 100, 100
	gq := QueueForUnit(ground)
	gq.SetBinding(QueueForUnit(f.guard).Binding())
	gn := &Node{ID: Lookup("Follow_Ground"), Owner: ground.Handle, Target: f.ward.Handle, Deadline: -1}
	if code := guardHandler(ground, gn, 0, 100); code != Code(1) {
		t.Fatalf("ground admit want *advance* (1), got %d", code)
	}
	gn.Phase = 2
	if code := guardHandler(ground, gn, 0, 130); code != Code(7) {
		t.Fatalf("ground phase 2 want *cancel-all* (7), got %d [04 R-UNIT-06 §1]", code)
	}
}

// TestVTOLFollowHandsOffToSeekGuard locks the entry pre-check and its hand-off
// [04 R-ORD-02 §3]: "target null or satisfied ∩ 0x48 → if this record has no
// successor, allocate `VTOL_SeekGuard` with this record's target (null when the
// target is gone) and goal and tail-append it; complete either way."
func TestVTOLFollowHandsOffToSeekGuard(t *testing.T) {
	seekID := Lookup("VTOL_SeekGuard")
	if seekID == 0 {
		t.Skip("VTOL_SeekGuard descriptor absent")
	}

	// Case 1: `0x40` (the cannot-get-there signal) on the tail record. The
	// hand-off is appended and the guard completes.
	f := newAirGuardFixture(t)
	q := QueueForUnit(f.guard)
	n := q.PushHead(Lookup("VTOL_Follow"), Node{Owner: f.guard.Handle, Target: f.ward.Handle, Deadline: -1})
	n.GoalX, n.GoalY, n.GoalZ = f.ward.X, f.ward.Y, f.ward.Z
	if code := guardHandler(f.guard, n, 0x40, 100); code != Code(5) {
		t.Fatalf("satisfied 0x40 want *complete* (5), got %d [04 R-ORD-02 §3]", code)
	}
	prim := q.Primary()
	if len(prim) != 2 || prim[0] != n {
		t.Fatalf("the hand-off must be a TAIL append behind the guard record, got %d records", len(prim))
	}
	seek := prim[1]
	if seek.ID != seekID {
		t.Fatalf("appended %q, want VTOL_SeekGuard [04 R-ORD-02 §3]", DescriptorFor(seek.ID).Name)
	}
	if seek.Owner != f.guard.Handle {
		t.Fatalf("the tail append sets the record's owner [04 R-ORD-02 §4], got %d", seek.Owner)
	}
	if seek.Target != f.ward.Handle {
		t.Fatalf("hand-off target = %d, want the guard record's own target %d", seek.Target, f.ward.Handle)
	}
	if seek.GoalX != n.GoalX || seek.GoalY != n.GoalY || seek.GoalZ != n.GoalZ {
		t.Fatalf("hand-off goal = (%v,%v,%v), want the guard record's goal", seek.GoalX, seek.GoalY, seek.GoalZ)
	}
	// "no flag is inherited" [04 R-ORD-02 §4]: the append never takes the head's
	// active marker away from it.
	if seek.Flags&FlagActive != 0 {
		t.Fatalf("the tail append must not take the active marker [04 R-ORD-02 §4]")
	}

	// Case 2: the same trigger, but the record HAS a successor — no hand-off.
	f2 := newAirGuardFixture(t)
	q2 := QueueForUnit(f2.guard)
	q2.PushHead(Lookup("Stop"), Node{Owner: f2.guard.Handle})
	n2 := q2.PushHead(Lookup("VTOL_Follow"), Node{Owner: f2.guard.Handle, Target: f2.ward.Handle, Deadline: -1})
	if code := guardHandler(f2.guard, n2, 0x08, 100); code != Code(5) {
		t.Fatalf("satisfied 0x08 want *complete* (5), got %d [04 R-ORD-02 §3]", code)
	}
	for _, rec := range q2.Primary() {
		if rec.ID == seekID {
			t.Fatalf("a record WITH a successor must not plant a seeker [04 R-ORD-02 §3]")
		}
	}

	// Case 3: a null target. The pre-check still fires, and the appended record
	// carries a null target — "null when the target is gone".
	f3 := newAirGuardFixture(t)
	q3 := QueueForUnit(f3.guard)
	n3 := q3.PushHead(Lookup("VTOL_Follow"), Node{Owner: f3.guard.Handle, Deadline: -1})
	n3.GoalX, n3.GoalY, n3.GoalZ = f3.ward.X, f3.ward.Y, f3.ward.Z
	if code := guardHandler(f3.guard, n3, 0, 100); code != Code(5) {
		t.Fatalf("a null target wants *complete* (5), got %d [04 R-ORD-02 §3]", code)
	}
	prim3 := q3.Primary()
	if len(prim3) != 2 || prim3[1].ID != seekID {
		t.Fatalf("a null target still hands off [04 R-ORD-02 §3]")
	}
	if prim3[1].Target != 0 {
		t.Fatalf("hand-off target = %d, want null when the target is gone", prim3[1].Target)
	}
	if prim3[1].GoalX != n3.GoalX || prim3[1].GoalZ != n3.GoalZ {
		t.Fatalf("the goal still travels across when the target does not")
	}

	// Case 4: a live guard with neither bit and a live ward runs on — the
	// pre-check is not a blanket exit.
	f4 := newAirGuardFixture(t)
	n4 := airGuardNode(f4)
	if code := guardHandler(f4.guard, n4, 0, 100); code != Code(1) {
		t.Fatalf("a satisfied word outside 0x48 must reach the phase switch, got %d", code)
	}
}

// TestVTOLFollowDivertsToTheOffMapLoiter locks the sentinel-sector diversion
// [04 R-AIR-01 §5][04 R-ORD-02 §3]: it runs BEFORE the phase switch, returns
// from it immediately with result code 2, and reaches the recovery marker
// through the air leg seam because the marker family and the sector grid both
// belong to internal/movement.
func TestVTOLFollowDivertsToTheOffMapLoiter(t *testing.T) {
	f := newAirGuardFixture(t)
	offMap := true
	calls := 0
	binding := QueueForUnit(f.guard).Binding()
	binding.Movement.RunAir = func(u *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool) {
		calls++
		if DescriptorFor(n.ID).Name != "VTOL_Follow" {
			t.Fatalf("the seam saw %q", DescriptorFor(n.ID).Name)
		}
		if !offMap {
			return 0, false // on the map: the runner declines and the handler carries on
		}
		n.DynamicGate |= 0xE0 // the recovery leg's gate [04 R-AIR-01 §5]
		return Code(2), true
	}

	n := airGuardNode(f)
	before := f.sim.Draws()
	if code := guardHandler(f.guard, n, 0, 100); code != Code(2) {
		t.Fatalf("an off-map air guard wants the recovery's *hold* (2), got %d [04 R-AIR-01 §5]", code)
	}
	if calls != 1 {
		t.Fatalf("the seam was called %d times, want once per visit", calls)
	}
	if n.Phase != 0 || n.Param1 != 0 {
		t.Fatalf("the recovery returns BEFORE the phase switch: phase %d, p1 %d [04 R-ORD-02 §3]", n.Phase, n.Param1)
	}
	if got := f.sim.Draws() - before; got != 0 {
		t.Fatalf("the recovery draws %d times, want 0 — the admit's draw is not reached [I4]", got)
	}
	if n.DynamicGate&0xE0 != 0xE0 {
		t.Fatalf("gate = %#x, want the recovery's 0xE0 [04 R-AIR-01 §5]", n.DynamicGate)
	}

	// Back over the map: the runner declines and the ordinary admit runs.
	offMap = false
	n.DynamicGate = 0
	if code := guardHandler(f.guard, n, 0, 130); code != Code(1) {
		t.Fatalf("an on-map air guard wants the admit's *advance* (1), got %d", code)
	}
	if n.Param1 == 0 {
		t.Fatalf("the admit's orbit bearing was not drawn once the runner declined")
	}
}
