package orders

// Exhaustive result-code table test for BOTH pump segments [04 §3.3],
// plan C6/C7/C8: the primary pump (head-blocking, restart-from-head after
// each dispatch) and the secondary pump (front-to-back, skip not-due) share
// the code values but NOT the effects, so every code is exercised in both
// segments (ORD-03). Each case asserts the resulting queue shape (which
// records remain, their order, tombstones), the deadline and its delta from
// the tick where the code sets one, gate/bit transitions, and the simulation
// draw delta read from the injected stream's Draws() counter [04 §3.3]
// "Audit note — completion-wait ranges": only the code-3 arm draws RNG(15)
// (wait 30..44) and only the PRIMARY code-9 last-record arm draws RNG(30)
// (wait 30..59); the secondary code-9 arm is a plain removal with no re-arm
// and no draw.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

const probeTick uint32 = 1000

// injectTestSim swaps in a private simulation stream with a fixed state and
// restores the previous injection afterwards, so Draws() deltas measure
// exactly this case's draws [I4][DET-01].
func injectTestSim(t *testing.T) *rng.Simulation {
	t.Helper()
	sim := rng.SimulationFromState(0x2A5F17)
	return &sim
}

// expectedDraw reports the exact value the stream's next bound-b draw will
// produce, taken on a clone so the injected stream is not consumed.
func expectedDraw(sim *rng.Simulation, bound uint32) uint32 {
	clone := rng.SimulationFromState(sim.State)
	return clone.Uint32n(bound)
}

// probe is a per-record counting handler. On a record's second dispatch it
// arms an unsatisfiable gate and continues, so the head-blocking rule (step 3
// of [04 §3.3]) ends the cascade without any draw — keeping each case's draw
// delta attributable to the code under test alone.
type probe struct {
	calls map[uint32]int
}

func newProbe() *probe { return &probe{calls: map[uint32]int{}} }

func (p *probe) install(t *testing.T, id ID, first func(n *Node) Code) {
	t.Helper()
	restore := setHandler(id, func(u *units.Unit, n *Node, s uint32) Code {
		p.calls[n.Param1]++
		if p.calls[n.Param1] > 1 {
			n.DynamicGate = 0x400 // nonzero gate, nothing satisfied [04 §3.3] step 3
			return 2
		}
		return first(n)
	})
	t.Cleanup(restore)
}

func (p *probe) callsFor(param uint32) int { return p.calls[param] }

// secNode builds a ready secondary record assigned directly into the segment
// (dynamic gate 0 dispatches immediately, deadline -1 = none) [04 §3.3].
func secNode(id ID, param uint32, phase uint8) *Node {
	return &Node{ID: id, Param1: param, Phase: phase, DynamicGate: 0, Deadline: -1}
}

func activeMarkerCount(q *Queue) int {
	c := 0
	for _, n := range q.primary {
		if n.Flags&FlagActive != 0 {
			c++
		}
	}
	return c
}

func TestPumpResultCodeTables(t *testing.T) {
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	if moveID == 0 || buildID == 0 {
		t.Fatalf("descriptor lookup failed")
	}

	t.Run("primary", func(t *testing.T) {
		t.Run("code0 resets phase and continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code {
				n.Phase = 4
				return 0 // [04 §3.3] reset the phase to zero and continue walking
			})
			q.Push(moveID, Node{Phase: 4, Param1: 1})
			clearGates(q)
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 1 || q.primary[0].Phase != 0 {
				t.Fatalf("code0: phase %d len %d, want reset to 0 and record kept", q.primary[0].Phase, len(q.primary))
			}
			if p.callsFor(1) != 2 {
				t.Fatalf("code0: calls %d, want 2 (continue restarts from head)", p.callsFor(1))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code0: draw delta %d, want 0", d)
			}
		})
		t.Run("code1 advances phase and continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code { return 1 }) // [04 §3.3] advance the phase by one
			q.Push(moveID, Node{Phase: 7, Param1: 1})
			clearGates(q)
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 1 || q.primary[0].Phase != 8 {
				t.Fatalf("code1: phase %d, want 8", q.primary[0].Phase)
			}
			if p.callsFor(1) != 2 {
				t.Fatalf("code1: calls %d, want 2", p.callsFor(1))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code1: draw delta %d, want 0", d)
			}
		})
		t.Run("code2 continues unchanged and consumes satisfied bits", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code { return 2 }) // [04 §3.3] continue walking unchanged
			q.Push(moveID, Node{Phase: 7, Param1: 1})
			clearGates(q)
			q.primary[0].Satisfied = 0x2
			q.primary[0].DynamicGate = 0x2
			before := sim.Draws()
			q.Pump(u, probeTick)
			n := q.primary[0]
			if len(q.primary) != 1 || n.Phase != 7 {
				t.Fatalf("code2: len %d phase %d, want record kept, phase unchanged 7", len(q.primary), n.Phase)
			}
			if n.Satisfied != 0 {
				t.Fatalf("code2: satisfied %x, want consumed bits cleared", n.Satisfied)
			}
			if p.callsFor(1) != 2 {
				t.Fatalf("code2: calls %d, want 2", p.callsFor(1))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code2: draw delta %d, want 0", d)
			}
		})
		t.Run("code3 draws RNG(15), gate bit, deadline tick+30+draw", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code { return 3 }) // [04 §3.3] wait 30..44, only this arm draws below 15
			q.Push(moveID, Node{Phase: 7, Param1: 1})
			clearGates(q)
			want := expectedDraw(sim, 15)
			before := sim.Draws()
			q.Pump(u, probeTick)
			n := q.primary[0]
			if len(q.primary) != 1 {
				t.Fatalf("code3: record removed")
			}
			if n.DynamicGate != 1 {
				t.Fatalf("code3: gate %x, want lowest bit set (1)", n.DynamicGate)
			}
			if n.Deadline != int32(probeTick+30+want) {
				t.Fatalf("code3: deadline %d, want exactly tick+30+RNG(15) = %d [04 §3.3]", n.Deadline, probeTick+30+want)
			}
			if n.Deadline < int32(probeTick+30) || n.Deadline > int32(probeTick+44) {
				t.Fatalf("code3: deadline %d outside 30..44 range", n.Deadline)
			}
			if d := sim.Draws() - before; d != 1 {
				t.Fatalf("code3: draw delta %d, want exactly 1", d)
			}
		})
		t.Run("code4 continues unchanged", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code { return 4 }) // [04 §3.3] continue walking unchanged
			q.Push(moveID, Node{Phase: 7, Param1: 1})
			clearGates(q)
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 1 || q.primary[0].Phase != 7 {
				t.Fatalf("code4: len %d phase %d, want unchanged", len(q.primary), q.primary[0].Phase)
			}
			if p.callsFor(1) != 2 {
				t.Fatalf("code4: calls %d, want 2", p.callsFor(1))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code4: draw delta %d, want 0", d)
			}
		})
		t.Run("code5 unlinks and frees the head, continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 5 // [04 §3.3] unlink, free, and continue
				}
				n.DynamicGate = 0x400
				return 2
			})
			q.Push(moveID, Node{Param1: 1})
			q.Push(moveID, Node{Param1: 2})
			clearGates(q)
			a := q.primary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 1 || q.primary[0].Param1 != 2 {
				t.Fatalf("code5: head not removed, len %d", len(q.primary))
			}
			if a.Flags&FlagTombstone != 0 {
				t.Fatalf("code5: primary head must not be tombstoned [04 §3.3]")
			}
			if p.callsFor(2) != 1 {
				t.Fatalf("code5: successor not dispatched after removal (restart-from-head), calls %d", p.callsFor(2))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code5: draw delta %d, want 0", d)
			}
		})
		t.Run("code6 moves head to segment tail and continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 6 // [04 §3.3] move the record to the tail of its segment and continue (primary)
				}
				return 2
			})
			q.Push(moveID, Node{Param1: 1})
			q.Push(moveID, Node{Param1: 2})
			clearGates(q)
			a := q.primary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 2 || q.primary[0].Param1 != 2 || q.primary[1].Param1 != 1 {
				t.Fatalf("code6: order [%d %d], want [2 1] (head moved to tail)", q.primary[0].Param1, q.primary[1].Param1)
			}
			if a.Flags&FlagTombstone != 0 {
				t.Fatalf("code6: tail-rotated record must not be tombstoned")
			}
			if activeMarkerCount(q) != 1 {
				t.Fatalf("code6: active markers %d, want exactly one", activeMarkerCount(q))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code6: draw delta %d, want 0", d)
			}
		})
		t.Run("code7 frees every record on both segments and returns", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code { return 7 }) // [04 §3.3] cancel-all, exclusively primary code 7
			q.Push(moveID, Node{Param1: 1})
			q.Push(moveID, Node{Param1: 2})
			q.secondary = []*Node{secNode(buildID, 10, 0)}
			clearGates(q)
			a2, s1 := q.primary[1], q.secondary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 0 || len(q.secondary) != 0 {
				t.Fatalf("code7: primary %d secondary %d, want both segments empty", len(q.primary), len(q.secondary))
			}
			if a2.Flags&FlagTombstone == 0 || s1.Flags&FlagTombstone == 0 {
				t.Fatalf("code7: non-head records must be tombstoned [04 §3.3]")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code7: draw delta %d, want 0", d)
			}
		})
		t.Run("code8 unlinks and frees the head, continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 8 // [04 §3.3] unlink, free, and continue
				}
				n.DynamicGate = 0x400
				return 2
			})
			q.Push(moveID, Node{Param1: 1})
			q.Push(moveID, Node{Param1: 2})
			clearGates(q)
			a := q.primary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 1 || q.primary[0].Param1 != 2 {
				t.Fatalf("code8: head not removed, len %d", len(q.primary))
			}
			if a.Flags&FlagTombstone != 0 {
				t.Fatalf("code8: primary head must not be tombstoned [04 §3.3]")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code8: draw delta %d, want 0", d)
			}
		})
		t.Run("code9 last record re-arms with RNG(30), phase reset", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code { return 9 }) // [04 §3.3][R-P0-01] wait 30..59, only the primary last-record arm draws
			q.Push(moveID, Node{Phase: 5, Param1: 1})
			clearGates(q)
			want := expectedDraw(sim, 30)
			before := sim.Draws()
			q.Pump(u, probeTick)
			n := q.primary[0]
			if len(q.primary) != 1 {
				t.Fatalf("code9 last: record removed, want kept")
			}
			if n.Phase != 0 {
				t.Fatalf("code9 last: phase %d, want reset to 0", n.Phase)
			}
			if n.Flags&FlagRetryMark == 0 {
				t.Fatalf("code9 last: completion flag not set [04 §3.3]")
			}
			if n.DynamicGate != 1 {
				t.Fatalf("code9 last: gate %x, want 1", n.DynamicGate)
			}
			if n.Deadline != int32(probeTick+30+want) {
				t.Fatalf("code9 last: deadline %d, want exactly tick+30+RNG(30) = %d [R-P0-01][04 §3.3]", n.Deadline, probeTick+30+want)
			}
			if n.Deadline < int32(probeTick+30) || n.Deadline > int32(probeTick+59) {
				t.Fatalf("code9 last: deadline %d outside 30..59 range", n.Deadline)
			}
			if d := sim.Draws() - before; d != 1 {
				t.Fatalf("code9 last: draw delta %d, want exactly 1", d)
			}
		})
		t.Run("code9 non-last unlinks and frees", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 9 // [04 §3.3] otherwise unlink and free
				}
				n.DynamicGate = 0x400
				return 2
			})
			q.Push(moveID, Node{Param1: 1})
			q.Push(moveID, Node{Param1: 2})
			clearGates(q)
			a := q.primary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 1 || q.primary[0].Param1 != 2 {
				t.Fatalf("code9 non-last: head not removed, len %d", len(q.primary))
			}
			if a.Flags&FlagTombstone != 0 {
				t.Fatalf("code9 non-last: primary head must not be tombstoned [04 §3.3]")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code9 non-last: draw delta %d, want 0", d)
			}
		})
		t.Run("code above 9 expires the single record and returns", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := &Queue{binding: &QueueBinding{SimRNG: sim}}, newTestUnit()
			p := newProbe()
			p.install(t, moveID, func(n *Node) Code { return 12 }) // [04 §3.3] expiry helper and return, no draw, no cancel-all
			q.Push(moveID, Node{Param1: 1})
			q.Push(moveID, Node{Param1: 2})
			q.secondary = []*Node{secNode(buildID, 10, 0)}
			clearGates(q)
			a := q.primary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.primary) != 1 || q.primary[0].Param1 != 2 {
				t.Fatalf("code>9: head not removed, len %d", len(q.primary))
			}
			if p.callsFor(2) != 0 {
				t.Fatalf("code>9: successor dispatched, want walk stopped after expiry helper")
			}
			if len(q.secondary) != 1 {
				t.Fatalf("code>9: secondary touched, want single-node expiry only")
			}
			if a.Flags&FlagTombstone != 0 {
				t.Fatalf("code>9: primary head must not be tombstoned [04 §3.3]")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code>9: draw delta %d, want 0", d)
			}
		})
	})

	t.Run("secondary", func(t *testing.T) {
		// Secondary walk: front-to-back, skipping records that are not due
		// [04 §3.3] C8; primary is kept empty so the front-blocker rule never
		// suppresses the segment under test.
		newQ := func(sim *rng.Simulation, nodes ...*Node) (*Queue, *units.Unit) {
			q := &Queue{binding: &QueueBinding{SimRNG: sim}}
			q.secondary = nodes
			return q, newTestUnit()
		}
		notDue := func(id ID, param uint32) *Node {
			n := secNode(id, param, 0)
			n.DynamicGate = 1
			n.Deadline = int32(probeTick + 5000) // far in the future: skipped
			return n
		}

		t.Run("code0 resets phase and advances", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 4))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 0 }) // [04 §3.3] reset the phase to zero
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Phase != 0 {
				t.Fatalf("code0 secondary: len %d phase %d, want record kept, phase 0", len(q.secondary), q.secondary[0].Phase)
			}
			if q.secondary[0].Flags&FlagTombstone != 0 {
				t.Fatalf("code0 secondary: record must not be tombstoned")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code0 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code1 advances phase", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 7))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 1 }) // [04 §3.3] advance the phase by one
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Phase != 8 {
				t.Fatalf("code1 secondary: len %d phase %d, want 8", len(q.secondary), q.secondary[0].Phase)
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code1 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code2 continues to next record", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 7), secNode(buildID, 2, 3))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 2 }) // [04 §3.3] continue walking unchanged
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 2 {
				t.Fatalf("code2 secondary: len %d, want both records kept", len(q.secondary))
			}
			if q.secondary[0].Phase != 7 || q.secondary[1].Phase != 3 {
				t.Fatalf("code2 secondary: phases %d %d, want unchanged", q.secondary[0].Phase, q.secondary[1].Phase)
			}
			if p.callsFor(1) != 1 || p.callsFor(2) != 1 {
				t.Fatalf("code2 secondary: calls %d/%d, want front-to-back walk reached both", p.callsFor(1), p.callsFor(2))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code2 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code3 draws RNG(15), gate bit, deadline, then next record", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 0), notDue(buildID, 2))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 3 }) // [04 §3.3] wait 30..44; the only drawing arm
			want := expectedDraw(sim, 15)
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 2 {
				t.Fatalf("code3 secondary: len %d, want both records kept", len(q.secondary))
			}
			n := q.secondary[0]
			if n.DynamicGate != 1 {
				t.Fatalf("code3 secondary: gate %x, want 1", n.DynamicGate)
			}
			if n.Deadline != int32(probeTick+30+want) {
				t.Fatalf("code3 secondary: deadline %d, want exactly tick+30+RNG(15) = %d [04 §3.3]", n.Deadline, probeTick+30+want)
			}
			if n.Deadline < int32(probeTick+30) || n.Deadline > int32(probeTick+44) {
				t.Fatalf("code3 secondary: deadline %d outside 30..44 range", n.Deadline)
			}
			// skip not-due: the second record keeps its far-future deadline untouched.
			if q.secondary[1].Deadline != int32(probeTick+5000) || q.secondary[1].DynamicGate != 1 {
				t.Fatalf("code3 secondary: not-due record was disturbed")
			}
			if p.callsFor(2) != 0 {
				t.Fatalf("code3 secondary: not-due record dispatched, want skipped")
			}
			if d := sim.Draws() - before; d != 1 {
				t.Fatalf("code3 secondary: draw delta %d, want exactly 1", d)
			}
		})
		t.Run("code4 continues to next record", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 7), secNode(buildID, 2, 3))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 4 }) // [04 §3.3] continue walking unchanged
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 2 || p.callsFor(1) != 1 || p.callsFor(2) != 1 {
				t.Fatalf("code4 secondary: len %d calls %d/%d, want both walked, unchanged", len(q.secondary), p.callsFor(1), p.callsFor(2))
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code4 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code5 unlinks and frees, continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 0), secNode(buildID, 2, 0))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 5 // [04 §3.3] C8 unlink and free as a plain removal
				}
				n.DynamicGate = 0x400
				return 2
			})
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Param1 != 2 {
				t.Fatalf("code5 secondary: len %d, want record 1 removed", len(q.secondary))
			}
			if p.callsFor(2) != 1 {
				t.Fatalf("code5 secondary: successor not dispatched, want walk continued")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code5 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code6 removes the single record and returns", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 0), secNode(buildID, 2, 0))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 6 }) // [04 §3.3] C8 remove the single record and return, no tail yield
			a := q.secondary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Param1 != 2 {
				t.Fatalf("code6 secondary: len %d, want single record removed", len(q.secondary))
			}
			if a.Flags&FlagTombstone == 0 {
				t.Fatalf("code6 secondary: removed record must be tombstoned")
			}
			if p.callsFor(2) != 0 {
				t.Fatalf("code6 secondary: successor dispatched, want walk returned after single removal")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code6 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code7 removes the single record and returns without cancel-all", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 0), secNode(buildID, 2, 0))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 7 }) // [04 §3.3] C8 remove the single record and return, no cancel-all
			a := q.secondary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Param1 != 2 {
				t.Fatalf("code7 secondary: len %d, want only the single record removed", len(q.secondary))
			}
			if a.Flags&FlagTombstone == 0 {
				t.Fatalf("code7 secondary: removed record must be tombstoned")
			}
			if p.callsFor(2) != 0 {
				t.Fatalf("code7 secondary: successor dispatched, want walk returned")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code7 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code8 unlinks and frees, continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 0), secNode(buildID, 2, 0))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 8 // [04 §3.3] C8 unlink and free as a plain removal
				}
				n.DynamicGate = 0x400
				return 2
			})
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Param1 != 2 {
				t.Fatalf("code8 secondary: len %d, want record 1 removed", len(q.secondary))
			}
			if p.callsFor(2) != 1 {
				t.Fatalf("code8 secondary: successor not dispatched, want walk continued")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code8 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code9 last record is a plain removal with no re-arm and no draw", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 5))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code { return 9 }) // [04 §3.3] C8 plain unlink+free regardless of last position
			a := q.secondary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 0 {
				t.Fatalf("code9 secondary last: record kept len %d, want plain removal (no re-arm) [04 §3.3]", len(q.secondary))
			}
			if a.Deadline != -1 {
				t.Fatalf("code9 secondary last: deadline re-armed to %d, want none [04 §3.3]", a.Deadline)
			}
			if a.Flags&FlagTombstone == 0 {
				t.Fatalf("code9 secondary: removed record must be tombstoned")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code9 secondary last: draw delta %d, want 0 (secondary code 9 never draws) [04 §3.3]", d)
			}
		})
		t.Run("code9 non-last unlinks and frees, continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 0), secNode(buildID, 2, 0))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 9 // [04 §3.3] C8 plain unlink+free
				}
				n.DynamicGate = 0x400
				return 2
			})
			a := q.secondary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Param1 != 2 {
				t.Fatalf("code9 secondary non-last: len %d, want record 1 removed", len(q.secondary))
			}
			if a.Deadline != -1 {
				t.Fatalf("code9 secondary: deadline re-armed to %d, want none [04 §3.3]", a.Deadline)
			}
			if p.callsFor(2) != 1 {
				t.Fatalf("code9 secondary non-last: successor not dispatched, want walk continued")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code9 secondary: draw delta %d, want 0", d)
			}
		})
		t.Run("code above 9 expires the single record as a plain removal, continues", func(t *testing.T) {
			sim := injectTestSim(t)
			q, u := newQ(sim, secNode(buildID, 1, 0), secNode(buildID, 2, 0))
			p := newProbe()
			p.install(t, buildID, func(n *Node) Code {
				if n.Param1 == 1 {
					return 12 // [04 §3.3] C8 plain unlink+free, no draw
				}
				n.DynamicGate = 0x400
				return 2
			})
			a := q.secondary[0]
			before := sim.Draws()
			q.Pump(u, probeTick)
			if len(q.secondary) != 1 || q.secondary[0].Param1 != 2 {
				t.Fatalf("code>9 secondary: len %d, want record 1 removed", len(q.secondary))
			}
			if a.Flags&FlagTombstone == 0 {
				t.Fatalf("code>9 secondary: removed record must be tombstoned")
			}
			if p.callsFor(2) != 1 {
				t.Fatalf("code>9 secondary: successor not dispatched, want plain removal walk continued")
			}
			if d := sim.Draws() - before; d != 0 {
				t.Fatalf("code>9 secondary: draw delta %d, want 0", d)
			}
		})
	})
}
