package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The null-target entry guard precedes every landing phase [04 R-AIR-01 §6].
// A follow marker cannot arrive after its pad dies [04 R-AIR-01 §4], so the
// removal notification must reach that guard independently of marker arrival.
func TestLandingPadRemovalInterruptsDescent(t *testing.T) {
	sys, w, u := airFixture(t)
	pad := spawnAirBasePadFor(t, w, sys.Terrain, "pad", u.Owner, u.X+numeric.Fixed(48<<16), u.Z)
	sys.EnsureUnit(pad)
	u.Move.Mode = 2
	u.Y += numeric.Fixed(100 << 16)
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("VTOL_Landing"), orders.Node{Owner: u.Handle, Target: pad.Handle})
	n := q.Head()
	m := sys.newFollowPieceMarker(u, pad.Handle, airNoPiece)
	m.setAltitudeOffset(0)
	sys.installAirGoal(u, n, m)
	n.Phase = 5
	n.DynamicGate = 0xE9
	n.Deadline = 15
	sys.BindAirOrderLegs()
	var messages []string
	q.Binding().Presentation = &orders.PresentationAdapter{Status: func(_ *units.Unit, _ uint8, message string) bool {
		messages = append(messages, message)
		return true
	}}
	orders.TargetRemoved(w, pad.Handle)
	pad.Alive = false
	for tick := uint32(1); tick <= 3; tick++ {
		runLandingTick(sys, tick, w)
	}
	for _, remaining := range q.Primary() {
		if remaining == n {
			t.Fatal("landing still waits for arrival at its removed pad [04 R-AIR-01 §6]")
		}
	}
	found := false
	for _, message := range messages {
		found = found || message == "Landing aborted"
	}
	if !found {
		t.Fatalf("status messages = %q, want Landing aborted [04 R-AIR-01 §6]", messages)
	}
	if u.Attachment.Carrier != 0 {
		t.Fatal("aborted landing attached to its removed pad")
	}
}

func TestLandingDescentDeadlineRechecksTakenPad(t *testing.T) {
	sys, w, u := airFixture(t)
	pad := spawnAirBasePadFor(t, w, sys.Terrain, "pad", u.Owner, u.X, u.Z)
	u.Move.Mode = 2
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("VTOL_Landing"), orders.Node{Owner: u.Handle, Target: pad.Handle})
	n := q.Head()
	n.Phase = 5
	code, ok := sys.runAirOrderLeg(u, n, 0, 100)
	if !ok || code != 2 || n.Deadline != 115 || n.DynamicGate != 0xE9 {
		t.Fatalf("descent code=%d recognized=%v deadline=%d gate=%#x; want hold, tick+15, 0xE9 [04 R-AIR-01 §6]", code, ok, n.Deadline, n.DynamicGate)
	}
	if code, _ := sys.runAirOrderLeg(u, n, 0x20, 101); code != 1 {
		t.Fatalf("arrival code=%d, want phase advance [04 R-AIR-01 §6]", code)
	}
	// Occupy the original piece after descent starts. The deadline must re-query
	// the pad and install a marker on the next free piece without arrival.
	h, err := w.Create(defForCargo("padguest", 1), u.Owner, u.X, u.Y, u.Z)
	if err != nil {
		t.Fatal(err)
	}
	if !AttachCargo(w, pad.Handle, h, 0) {
		t.Fatal("occupy pad")
	}
	n.DynamicGate = 0
	if code, _ := sys.runAirOrderLeg(u, n, 1, 115); code != 2 || n.Param1 != 1 || n.Deadline != 130 {
		t.Fatalf("pad recheck code=%d piece=%d deadline=%d, want hold on next piece [04 R-AIR-01 §6]", code, n.Param1, n.Deadline)
	}
}

func TestLoadedLandingTransfersCargoAndEmptyLandingRejectsNoRoute(t *testing.T) {
	sys, w, u := airFixture(t)
	pad := spawnAirBasePadFor(t, w, sys.Terrain, "pad", u.Owner, u.X, u.Z)
	u.Move.Mode = 2
	n := &orders.Node{Owner: u.Handle, Target: pad.Handle, ID: orders.Lookup("VTOL_Landing"), Phase: 6}
	if code, _ := sys.runAirOrderLeg(u, n, 0x40, 1); code != 8 || u.Attachment.Carrier != 0 {
		t.Fatalf("failed final leg code=%d carrier=%d, want abandon without attachment [04 R-AIR-01 §6]", code, u.Attachment.Carrier)
	}
	h, err := w.Create(defForCargo("padcargo", 1), u.Owner, u.X, u.Y, u.Z)
	if err != nil {
		t.Fatal(err)
	}
	if !AttachCargo(w, u.Handle, h, 0) {
		t.Fatal("load carrier")
	}
	if code, _ := sys.runAirOrderLeg(u, n, 0x20, 2); code != 5 {
		t.Fatalf("loaded landing code=%d, want complete [04 R-AIR-01 §6]", code)
	}
	if u.Attachment.Carrier != 0 || len(u.Attachment.Cargo) != 0 || w.Unit(h).Attachment.Carrier != pad.Handle {
		t.Fatal("loaded landing must park its cargo and leave the carrier free [04 R-AIR-01 §6]")
	}
}
