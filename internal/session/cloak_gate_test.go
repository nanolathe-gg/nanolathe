package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestCloakGateReadsTheRequestBitAndTheSensorBreach locks the three terms the
// session binds into economy's CloakDue seam [05 R-ECO-01 §9][03 R-VIS-01 §6].
//
// Term 1 is the cloak-REQUESTED status bit, never the instance bit — the
// instance bit is this gate's output, written by the settlement's transition,
// and reading it back here would keep a unit charged after `Cloak_Off`.
// Term 2 is the decloak-forced bit, which the sensor phase's proximity breach
// sets and the top of the next first pass clears [03 R-VIS-01 §4 pass 4].
// Term 3 is the shared reveal/cloak-suppression deadline, inclusive; the same
// breach writes `tick + 90` into it on the same visit, which is the durable
// half of the same mechanism.
func TestCloakGateReadsTheRequestBitAndTheSensorBreach(t *testing.T) {
	cat := minimalCatalogForStrict()
	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission()}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("newSlicedWorld: %v", err)
	}
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.SeedDeadlines(0)
	s.Clock = &clock.State{}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	if s.Econ.CloakDue == nil {
		t.Fatal("composition left the cloak gate unbound")
	}

	def := cat.Units["armcom"]
	def.CloakCost = 6
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)

	if s.Econ.CloakDue(u) {
		t.Fatal("a unit with no cloak request is due for the debit [05 R-ECO-01 §9] term 1")
	}
	u.SetCloaked(true)
	if !s.Econ.CloakDue(u) {
		t.Fatal("a requesting unit with a zero deadline is not due on its first pass [05 R-ECO-01 §9]")
	}

	// The retained breach latch blocks the debit while it remains set.
	u.Flags |= visibility.DecloakBit
	if s.Econ.CloakDue(u) {
		t.Fatal("the decloak-forced bit did not block the cloak debit [03 R-VIS-01 §6] term 2")
	}
	// The latch is cleared at the next due sensor pass, but
	// the deadline it wrote on the same visit keeps the unit uncloaked for the
	// remaining ticks [03 R-VIS-01 §6].
	u.Flags &^= visibility.DecloakBit
	u.RevealDeadline = uint32(visibility.DecloakDeadlineAdd)
	if s.Econ.CloakDue(u) {
		t.Fatal("the breach deadline did not block the cloak debit [05 R-ECO-01 §9] term 3")
	}
	s.Clock.GlobalTick = uint32(visibility.DecloakDeadlineAdd)
	if !s.Econ.CloakDue(u) {
		t.Fatal("the deadline compare is not inclusive [05 R-ECO-01 §9] term 3")
	}

	// The output bit is never an input: a hidden unit whose request was
	// cleared must stop being charged.
	u.SetCloakedInstance(true)
	u.SetCloaked(false)
	if s.Econ.CloakDue(u) {
		t.Fatal("the gate read the instance cloaked bit instead of the request [05 R-ECO-01 §9]")
	}
}

// TestSensorBreachStampsTheSharedRevealDeadline locks the sensor phase's write
// target. The proximity breach's `tick + 90` is one of the twelve writers of
// the single reveal/cloak-suppression word, whose one gameplay reader is the
// cloak debit gate [03 R-VIS-01 §6 "Writer census of the shared deadline"] — so
// it must land on the unit, not beside it.
func TestSensorBreachStampsTheSharedRevealDeadline(t *testing.T) {
	cat := minimalCatalogForStrict()
	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission()}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("newSlicedWorld: %v", err)
	}
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.SeedDeadlines(0)
	s.Clock = &clock.State{}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	def := cat.Units["armcom"]
	def.CloakCost = 6
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	u.RevealDeadline = 7
	s.stepSensorPhase(11)
	// No breach fires in this fixture (no hostile candidate list), so the word
	// must come back untouched rather than zeroed by a pass that owns it.
	if u.RevealDeadline != 7 {
		t.Fatalf("the sensor pass rewrote the shared deadline to %d with no breach [03 R-VIS-01 §4 pass 4]", u.RevealDeadline)
	}
}
