package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_MOVEMENT_PATH §3.4.1. Only a bombing
// pass defers the inclusive leash, and only until overflight completes.
func TestModernBomberLeashBoundary(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, name := range []string{"AirStrike", "AirToGround", "AirToGroundHover", "AirToAir"} {
			for phase := uint8(0); phase <= 6; phase++ {
				t.Run(fmt.Sprintf("modern=%v/%s/phase=%d", modern, name, phase), func(t *testing.T) {
					q, u := gateFixture()
					b := q.Binding()
					b.Rules = modeRules(modern)
					b.Lookup = func(pool.Handle) *units.Unit { return u }
					ledger := &economy.Service{}
					b.Economy = ledger
					q.Push(Lookup(name), Node{Owner: u.Handle, Target: 7, Phase: phase, GuardX: 70, GuardY: 60, Param3: 30})
					n := q.Head()
					random := *b.SimRNG
					before := ledger.Players
					code, done := airEntry(u, n, 0, airInterruptMask(n.ID), 0)
					wantDone := !(modern && name == "AirStrike" && phase >= 1 && phase <= 5)
					if done != wantDone || (done && code != 5) {
						t.Fatalf("entry=(%d,%v), want completion=%v", code, done, wantDone)
					}
					if *b.SimRNG != random || ledger.Players != before {
						t.Fatal("leash decision consumed RNG or resources")
					}
					n.Param3 = 31 // One unit inside the inclusive boundary always continues.
					if _, done = airEntry(u, n, 0, airInterruptMask(n.ID), 0); done {
						t.Fatal("inside-leash attack ended")
					}
					n.Param3 = 0
					if _, done = airEntry(u, n, 0, airInterruptMask(n.ID), 0); done {
						t.Fatal("unleashed attack ended")
					}
				})
			}
		}
	}
}

// Delaying the leash never delays the existing target/cancel entry checks.
func TestModernBomberPassStillCancels(t *testing.T) {
	for _, reason := range []uint32{pendTargetRemoved, pendTargetCloaked, gateCancelCurrent, 0} {
		q, u := gateFixture()
		q.Binding().Rules = &ModernRules{}
		q.Binding().Lookup = func(pool.Handle) *units.Unit { return u }
		q.Push(Lookup("AirStrike"), Node{Owner: u.Handle, Target: 7, Phase: 2, GuardX: 70, GuardY: 60, Param3: 30})
		n := q.Head()
		q.Push(Lookup("VTOL_Move"), Node{Owner: u.Handle})
		if reason == 0 {
			n.Target = 0
		}
		random := *q.Binding().SimRNG
		if code, done := airEntry(u, n, reason, airInterruptMask(n.ID), 0); !done || code != 5 {
			t.Fatalf("reason %d did not cancel: (%d,%v)", reason, code, done)
		}
		if *q.Binding().SimRNG != random {
			t.Fatal("cancellation consumed RNG")
		}
	}
}

func TestModernBomberLeashResumesReturnAfterOverflight(t *testing.T) {
	q, u := gateFixture()
	b := q.Binding()
	b.Rules = &ModernRules{}
	b.Lookup = func(pool.Handle) *units.Unit { return u }
	q.Push(Lookup("AirStrike"), Node{Owner: u.Handle, Target: 7, Phase: 6, GuardX: 70, GuardY: 60, Param3: 30})
	attack := q.Head()
	attack.DynamicGate = 0xE2
	q.Push(Lookup("VTOL_Move"), Node{Owner: u.Handle})
	post := q.Primary()[1]
	q.SetOwnedHandler(post.ID, func(*units.Unit, *Node, uint32, uint32) (Code, bool) { return 0, false })
	slot := u.SlotAt(0)
	slot.Flags &^= units.SlotFlagAutonomous
	slot.Target = units.Target{Kind: units.TargetGround, X: u.X, Z: u.Z}
	random := *b.SimRNG
	q.Pump(u, 1)
	if q.Head() != attack || slot.Target.Kind != units.TargetGround {
		t.Fatal("leash ended pass before overflight arrival")
	}
	attack.Satisfied |= 0x20
	q.Pump(u, 2)
	if q.Head() != post || slot.Target.Kind != units.TargetNone {
		t.Fatal("completed pass did not clear target and resume return move")
	}
	if *b.SimRNG != random {
		t.Fatal("return-to-post consumed RNG")
	}
}
