package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The phase-3 wait has no timer: only a real engine-port write wakes it
// [04 R-ORD-01 §5][04 R-COB-06]. Exercise the production ordering of an
// ordinary queue pump before the construction-owned work window.
func TestUnitReclaimStanceWaitThroughCOBAndConstructionWindow(t *testing.T) {
	s, builder, target, node := reclaimFixture(t, 100, 10)
	node.Phase, node.Param1 = 2, 15
	builder.InBuildStance = false
	builder.ScriptState, builder.Script = nil, nil
	builder.Def.Script = &cob.Program{
		Pieces:  []string{"piece0"},
		Scripts: map[string]int{"StartBuilding": 0, "Wake": 3}, ScriptsByID: []int{0, 3},
		Code: []uint32{0x10021001, 0, 0x10065000,
			0x10021001, 5, 0x10021001, 1, 0x10082000,
			0x10021001, 0, 0x10065000},
	}
	// Use the production binder so port 5 and the script-touched writer are
	// installed by units, rather than simulating the event in the fixture.
	binding, err := units.BindCOBWithPortsAndVisibilityForUnit(nil, builder, trivialModel(1, nil), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink := &countingNanoSink{}
	s.Presentation = sink
	q := orders.QueueForUnit(builder)
	workCues := 0
	q.SetBinding(&orders.QueueBinding{Lookup: func(h pool.Handle) *units.Unit { return s.World.Unit(h) }, Presentation: &orders.PresentationAdapter{Status: func(_ *units.Unit, kind uint8, text string) bool {
		if kind == 11 {
			if node.Phase != 4 || text != "" {
				t.Fatal("work cue outside phase 4")
			}
			workCues++
		}
		return true
	}}})
	s.RegisterOrderHandlers(q)
	step := func(tick uint32) { s.StepUnit(TickContext{Tick: tick}, builder.Handle) }
	step(0)
	if node.Phase != 3 || node.Param2 != 0 || sink.count != 0 || target.Health != 100 || workCues != 0 {
		t.Fatalf("stance false: phase=%d cadence=%d spray=%d health=%d", node.Phase, node.Param2, sink.count, target.Health)
	}
	if node.DynamicGate != 0x1000c || node.Deadline != -1 {
		t.Fatalf("stance wait gate/deadline=%#x/%d", node.DynamicGate, node.Deadline)
	}
	for tick := uint32(1); tick <= 40; tick++ {
		q.Pump(builder, tick)
		step(tick)
	}
	if node.Phase != 3 || node.Param2 != 0 || sink.count != 0 || target.Health != 100 || workCues != 0 {
		t.Fatal("work advanced while stance was false")
	}
	binding.VM.Threads[0].Status, binding.VM.Threads[0].PC = cob.ThreadRunning, 3
	binding.VM.Drain(41)
	if !builder.InBuildStance || builder.Pending&units.PendingScriptTouched == 0 {
		t.Fatal("COB did not write stance and wake")
	}
	q.Pump(builder, 41)
	if node.Phase != 3 || node.Param2 != 0 || sink.count != 0 {
		t.Fatal("ordinary pump ran construction work")
	}
	step(41)
	if node.Phase != 5 || node.Param2 != 2 || sink.count != 1 || workCues != 1 {
		t.Fatalf("COB wake did not reach work: phase/counter/spray=%d/%d/%d", node.Phase, node.Param2, sink.count)
	}
	if (builder.Pending|node.Satisfied)&units.PendingScriptTouched != 0 {
		t.Fatal("COB wake was not consumed")
	}
	q.Pump(builder, 42)
	step(42)
	if node.Param2 != 2 || sink.count != 1 {
		t.Fatal("wake executed work twice")
	}
	q.Pump(builder, 43)
	step(43)
	if node.Param2 != 4 || sink.count != 2 {
		t.Fatal("next due work did not run")
	}
}

// Both phase-2 no-route arms return the ordinary re-arm code; target-removal
// notices instead complete without admitting work [04 R-ORD-01 §5, §7].
func TestUnitReclaimApproachEventOutcomes(t *testing.T) {
	for _, name := range []string{"ReclaimUnit", "VTOL_ReclaimUnit"} {
		t.Run(name, func(t *testing.T) {
			s, b, _, n := reclaimFixture(t, 100, 10)
			n.ID = orders.Lookup(name)
			n.Phase, n.DynamicGate, n.Satisfied = 2, 0x10048, 0x40
			q := orders.QueueForUnit(b)
			sim := rng.NewSimulation(1)
			q.SetBinding(&orders.QueueBinding{Lookup: s.World.Unit, SimRNG: &sim})
			s.RegisterOrderHandlers(q)
			before := sim.Draws()
			q.Pump(b, 10)
			if n.Phase != 2 {
				t.Fatal("earlier pump ran re-arm")
			}
			s.StepUnit(TickContext{Tick: 10}, b.Handle)
			if n.Phase != 0 || n.DynamicGate != 1 || n.Deadline < 40 || n.Deadline > 69 || sim.Draws() != before+1 || n.Param2 != 0 {
				t.Fatalf("no-route did not use queue re-arm: %+v", n)
			}
			n.Phase, n.DynamicGate, n.Satisfied, n.Deadline = 2, 0x10008, 0x10000, -1
			q.Pump(b, 11)
			s.StepUnit(TickContext{Tick: 11}, b.Handle)
			if q.Head() == n || sim.Draws() != before+1 {
				t.Fatal("target-removal event did not complete without work/re-arm")
			}
		})
	}
}

// Construction resumes the primary work record only: the standing segment
// already ran in the ordinary queue visit [04 R-ORD-01 §10].
func TestUnitReclaimDoesNotRepeatSecondaryPump(t *testing.T) {
	s, b, _, _ := reclaimFixture(t, 100, 10)
	q := orders.QueueForUnit(b)
	q.PushSecondary(orders.Lookup("SelfDestruct"), orders.Node{Owner: b.Handle, Deadline: -1})
	q.Pump(b, 0)
	if q.LenSecondary() != 1 || q.Secondary()[0].Phase != 1 {
		t.Fatal("secondary countdown did not arm its first wait")
	}
	// Make the existing countdown due so its gate cannot conceal an extra
	// rear-segment walk. Construction owns only the primary work window
	// [04 R-ORD-01 §10]; SelfDestruct's next visit is due after thirty ticks
	// [04 R-SPEC-01 §13].
	s.StepUnit(TickContext{Tick: 30}, b.Handle)
	if q.LenSecondary() != 1 || q.Secondary()[0].Phase != 1 {
		t.Fatal("construction repeated the secondary pump")
	}
}
