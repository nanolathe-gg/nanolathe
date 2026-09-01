package airdiag

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestLandingOrderIsFlownNotTeleported encodes the contract the pad landing
// must satisfy: `VTOL_Landing` is a seven-phase machine that takes off, loiters
// or approaches, and only then parks the aircraft on the chosen pad piece
// [04 R-AIR-01 §6]; every position it passes through is committed by the flight
// integrator's velocity step, which is bounded by the definition's MaxVelocity
// [04 §10.1]. A single-tick jump to the pad is therefore a defect no matter
// what it looks like on screen.
func TestLandingOrderIsFlownNotTeleported(t *testing.T) {
	h := newHarness(t)
	pad := spawnPad(t, h)
	u := spawnAircraft(t, h, diagAircraft, 6, 6)

	if err := h.Order(u, 2, pad.Handle, pad.X, pad.Y, pad.Z); err != nil {
		t.Fatalf("submit landing: %v", err)
	}

	// MaxVelocity is a 16.16 per-tick displacement bound; one extra world unit
	// of slack keeps the assertion about teleports rather than about rounding.
	bound := int64(u.Def.MaxVelocity) + 65536
	prevX, prevZ := u.X, u.Z
	rows := h.Trace(u, 12)
	dump(t, rows, 1)
	for _, r := range rows {
		dx := int64(r.X) - int64(prevX)
		dz := int64(r.Z) - int64(prevZ)
		if dx < 0 {
			dx = -dx
		}
		if dz < 0 {
			dz = -dz
		}
		if dx > bound || dz > bound {
			t.Errorf("tick %d moved the aircraft by (%.2f,%.2f) world units in one tick, "+
				"more than MaxVelocity %.2f: the landing order teleported it onto the pad instead of flying there",
				r.Tick, fx(dxFixed(dx)), fx(dxFixed(dz)), fx(dxFixed(int64(u.Def.MaxVelocity))))
			break
		}
		prevX, prevZ = r.X, r.Z
	}
	t.Logf("pad=(%.2f,%.2f) aircraft after 12 ticks=(%.2f,%.2f) carrier=%d head=%q",
		fx(pad.X), fx(pad.Z), fx(rows[len(rows)-1].X), fx(rows[len(rows)-1].Z), u.Attachment.Carrier, rows[len(rows)-1].HeadName)
}

// TestIdleAircraftLands is the idle chain of [04 R-AIR-01 §7]: an aircraft that
// has finished its orders is refilled with its authored `defaultmissiontype`,
// which for every stock aircraft is `VTOL_Standby`; its no-cargo arm pushes
// `VTOL_LandIfCan`, whose phase 2 calls the mover-mode setter with grounded
// mode 1 [04 R-AIR-01 §6]. An aircraft that never reaches mode 1 has not landed.
func TestIdleAircraftLands(t *testing.T) {
	h := newHarness(t)
	u := spawnAircraft(t, h, diagAircraft, 6, 6)
	t.Logf("%s defaultmissiontype=%q", u.Def.UnitName, u.Def.DefaultMissionType)

	goalX := u.X.Add(world.CellToWorld(10))
	goalZ := u.Z
	if err := h.Order(u, 2, 0, goalX, goalHeight(h, goalX, goalZ), goalZ); err != nil {
		t.Fatalf("submit move: %v", err)
	}
	rows := h.Trace(u, 600)
	dump(t, rows, 60)

	var sawAirborne, sawStandby, sawLandIfCan, sawGrounded bool
	for _, r := range rows {
		switch {
		case r.Mode == 2:
			sawAirborne = true
		case sawAirborne && r.Mode == 1:
			sawGrounded = true
		}
		switch r.HeadName {
		case "VTOL_Standby":
			sawStandby = true
		case "VTOL_LandIfCan":
			sawLandIfCan = true
		}
	}
	t.Logf("airborne=%v standby record seen=%v landifcan record seen=%v grounded again=%v final mode=%d queue=%d",
		sawAirborne, sawStandby, sawLandIfCan, sawGrounded, rows[len(rows)-1].Mode, rows[len(rows)-1].QueueLen)
	if !sawStandby {
		t.Errorf("no VTOL_Standby record was ever created for an idle aircraft: the pump's "+
			"defaultmissiontype refill never ran, so the standby -> VTOL_LandIfCan chain cannot start "+
			"(defaultmissiontype = %q)", u.Def.DefaultMissionType)
	}
	if !sawGrounded {
		t.Errorf("the aircraft never returned to grounded mode 1 in 600 ticks: it does not land when idle")
	}
}

// dxFixed re-wraps a magnitude for the shared fixed-point formatter.
func dxFixed(v int64) numeric.Fixed { return numeric.Fixed(v) }
