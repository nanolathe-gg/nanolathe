package airdiag

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const (
	diagMap      = "ashap plateau"
	diagAircraft = "ARMFIG" // ARM fighter: canfly, not a builder
	diagAirBase  = "ARMASP" // ARM air repair pad: an authored isairbase definition
	diagAnchor   = "ARMCOM" // the placed commander, used only as a spawn anchor
)

func newHarness(t *testing.T) *Harness {
	t.Helper()
	root := testsupport.RetailRoot(t)
	h, err := New(root, diagMap, 12345, 67890)
	if err != nil {
		t.Skipf("compose %q: %v", diagMap, err)
	}
	t.Cleanup(h.Close)
	return h
}

// spawnAircraft puts an aircraft on open ground near the placed commander, so
// the terrain under it is terrain the placement pass already accepted.
func spawnAircraft(t *testing.T, h *Harness, key string, dx, dz int32) *units.Unit {
	t.Helper()
	anchor := h.Unit(0, diagAnchor)
	if anchor == nil {
		t.Skip("the ARM commander was not placed on this map")
	}
	x := anchor.X.Add(world.CellToWorld(dx))
	z := anchor.Z.Add(world.CellToWorld(dz))
	u, err := h.Spawn(0, key, x, z)
	if err != nil {
		t.Skipf("spawn %s: %v", key, err)
	}
	if u.Def == nil || !u.Def.CanFly {
		t.Fatalf("%s is not a can-fly definition", key)
	}
	return u
}

func spawnPad(t *testing.T, h *Harness) *units.Unit {
	t.Helper()
	anchor := h.Unit(0, diagAnchor)
	if anchor == nil {
		t.Skip("the ARM commander was not placed on this map")
	}
	def, ok := h.Catalog.Unit(diagAirBase)
	if !ok || def == nil {
		t.Skipf("%s absent", diagAirBase)
	}
	if !def.IsAirBase {
		t.Skipf("%s is not an isairbase definition", diagAirBase)
	}
	x := anchor.X.Add(world.CellToWorld(-14))
	z := anchor.Z.Add(world.CellToWorld(-14))
	pad, err := h.Spawn(0, diagAirBase, x, z)
	if err != nil {
		t.Skipf("spawn %s: %v", diagAirBase, err)
	}
	return pad
}

func dump(t *testing.T, rows []Row, every int) {
	t.Helper()
	for i, r := range rows {
		if i%every == 0 || i == len(rows)-1 {
			t.Log(r.Format())
		}
	}
}

// TestAirMoveFromGroundedReachesItsGoal is the first symptom, isolated. A plain
// Move order given to a grounded aircraft must fly it to the ordered point:
// `VTOL_Move` phase 1 snaps the record's goal onto the unit's own footprint and
// installs a point marker THERE, and phase 2 completes on the arrival bit that
// marker raises [04 R-ORD-02 §2][04 R-AIR-01 §1] step 6. The takeoff preamble's
// own marker is an initial climb goal at the unit's own X/Z with altitude
// offset `cruisealt / 2` [04 R-AIR-01 §6] step 4 — it is not the leg the record
// completes on.
func TestAirMoveFromGroundedReachesItsGoal(t *testing.T) {
	h := newHarness(t)
	u := spawnAircraft(t, h, diagAircraft, 6, 6)
	t.Logf("spawn: def=%s canfly=%v cruisealt=%d maxvel=%d accel=%d brake=%d turnrate=%d bankscale=%d pitchscale=%d defaultmission=%q",
		u.Def.UnitName, u.Def.CanFly, u.Def.CruiseAlt, u.Def.MaxVelocity, u.Def.Acceleration,
		u.Def.BrakeRate, u.Def.TurnRate, u.Def.BankScale, u.Def.PitchScale, u.Def.DefaultMissionType)
	t.Logf("start: pos=(%.2f,%.2f,%.2f) heading=%d mode=%d", fx(u.X), fx(u.Y), fx(u.Z), u.Move.Heading, u.Move.Mode)

	goalX := u.X.Add(world.CellToWorld(40))
	goalZ := u.Z
	if err := h.Order(u, 2, 0, goalX, goalHeight(h, goalX, goalZ), goalZ); err != nil {
		t.Fatalf("submit move: %v", err)
	}
	rows := h.Trace(u, 200)
	dump(t, rows, 10)

	last := rows[len(rows)-1]
	startX, startZ := rows[0].X, rows[0].Z
	t.Logf("goal=(%.2f,%.2f) start=(%.2f,%.2f) end=(%.2f,%.2f)",
		fx(goalX), fx(goalZ), fx(startX), fx(startZ), fx(last.X), fx(last.Z))
	if last.X == startX && last.Z == startZ {
		t.Errorf("the aircraft did not move horizontally at all in 200 ticks: it climbed to the "+
			"takeoff preamble's cruisealt/2 goal at its own X/Z and the record completed there "+
			"(final Y %.2f, terrain %.2f)", fx(last.Y), fx(goalHeight(h, startX, startZ)))
	}
}

// TestAirLandOnPad is the second symptom, isolated: an explicit landing order at
// an authored air base must end with the aircraft ATTACHED to the pad owner on
// the chosen pad piece, which is what `VTOL_Landing` phase 6 does
// [04 R-AIR-01 §6].
func TestAirLandOnPad(t *testing.T) {
	h := newHarness(t)
	pad := spawnPad(t, h)
	u := spawnAircraft(t, h, diagAircraft, 6, 6)

	if err := h.Order(u, 2, pad.Handle, pad.X, pad.Y, pad.Z); err != nil {
		t.Fatalf("submit landing: %v", err)
	}
	rows := h.Trace(u, 600)
	dump(t, rows, 25)

	last := rows[len(rows)-1]
	t.Logf("pad=(%.2f,%.2f) final=(%.2f,%.2f,%.2f) mode=%d head=%q exec phase=%d carrier=%d",
		fx(pad.X), fx(pad.Z), fx(last.X), fx(last.Y), fx(last.Z), last.Mode, last.HeadName, last.ExecPhase, u.Attachment.Carrier)
	if u.Attachment.Carrier == 0 {
		t.Errorf("the aircraft was never attached to the pad owner: carrier=0, mode=%d. "+
			"The seven-phase VTOL_Landing machine never ran; the record was completed on its first "+
			"pump dispatch", last.Mode)
	}
}
