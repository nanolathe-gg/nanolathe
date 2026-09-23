package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Guard is a standing order. The ground row's handler has no success exit:
// every phase-1 visit either assists or re-installs the follow goal and holds
// behind its 30-tick gate, and only a missing ward (code 5), a flying ward
// (code 8) or cancel-all (code 7) ends it [04 R-ORD-01 §8 point 4]
// [04 R-UNIT-06 §1]. The pump only ever runs the front head [04 §3.3 "Where
// the walk resumes"], so a record Shift-queued behind a guard never starts
// while the ward lives, and a plain issue is a Replace that purges the guard
// because `Follow_Ground` lacks the purge-survivor bit [04 §3.3]
// [04 R-MOV-03 §6]. These tests lock both modes: the Modern guard assistance
// policy retains the Guard record and its queued successors, it never
// completes the guard (DESIGN_UNITS_ORDERS_COB "Modern guard assistance").
//
// Play-test report 2026-09-22: "it's letting you guard two labs at once". The
// queue contract below already held on main when this was written; the tests
// pin it so the claim can be answered from them.

// guardQueueFixture is newGuardFixture with a second, equally idle ward.
func guardQueueFixture(t *testing.T, modern bool) (*guardFixture, *units.Unit, *bool) {
	t.Helper()
	f := newGuardFixture(t, 1, 1)
	second := &units.Unit{
		Handle: 3,
		Def:    &content.UnitDef{UnitName: "ward2", FootprintX: 1, FootprintZ: 1, MaxDamage: 100},
		Alive:  true,
		X:      numeric.Fixed(1500 << 16),
		Z:      numeric.Fixed(200 << 16),
	}
	second.MaxHealth, second.Health = 100, 100
	QueueForUnit(second).SetBinding(&QueueBinding{})
	firstGone := false
	b := QueueForUnit(f.guard).Binding()
	b.Rules = modeRules(modern)
	b.Lookup = func(h pool.Handle) *units.Unit {
		switch {
		case h == f.ward.Handle && !firstGone:
			return f.ward
		case h == second.Handle:
			return second
		}
		return nil
	}
	return f, second, &firstGone
}

// issue is the session producer's world-order path: a queued issue appends; a
// plain issue purges the unprotected records and drops leading auto records
// before inserting [04 §3.3].
func issue(q *Queue, u *units.Unit, name string, target pool.Handle, gx, gz numeric.Fixed, tick uint32, queued bool) {
	id := Lookup(name)
	if !queued {
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
	}
	q.Push(id, NewNodeForOrder(id, target, gx, 0, gz, tick, u.Handle, queued))
}

func queueShape(q *Queue) string {
	s := ""
	for _, n := range q.Primary() {
		s += fmt.Sprintf("[%s t%d p%d]", DescriptorFor(n.ID).Name, n.Target, n.Phase)
	}
	return s
}

func eachGuardMode(t *testing.T, run func(t *testing.T, modern bool)) {
	for _, modern := range []bool{false, true} {
		name := "strict"
		if modern {
			name = "modern"
		}
		t.Run(name, func(t *testing.T) { run(t, modern) })
	}
}

// (a) A plain Guard replaces the whole queue, including a guard already
// running and whatever was queued behind it.
func TestPlainGuardReplacesTheQueue(t *testing.T) {
	eachGuardMode(t, func(t *testing.T, modern bool) {
		f, second, _ := guardQueueFixture(t, modern)
		q := QueueForUnit(f.guard)
		issue(q, f.guard, "Follow_Ground", f.ward.Handle, f.ward.X, f.ward.Z, 10, false)
		issue(q, f.guard, "Move_Ground", 0, numeric.Fixed(900<<16), numeric.Fixed(900<<16), 10, true)
		for tick := uint32(10); tick < 70; tick++ {
			q.Pump(f.guard, tick)
		}
		issue(q, f.guard, "Follow_Ground", second.Handle, second.X, second.Z, 70, false)
		if got, want := queueShape(q), "[Follow_Ground t3 p0]"; got != want {
			t.Fatalf("queue after a plain second guard = %s, want %s", got, want)
		}
	})
}

// (b) A Shift-queued order behind a guard never starts while the ward lives,
// however long the guard runs; it starts once the ward is gone and the guard
// completes.
func TestQueuedOrderBehindGuardNeverStarts(t *testing.T) {
	eachGuardMode(t, func(t *testing.T, modern bool) {
		f, _, firstGone := guardQueueFixture(t, modern)
		q := QueueForUnit(f.guard)
		issue(q, f.guard, "Follow_Ground", f.ward.Handle, f.ward.X, f.ward.Z, 10, false)
		issue(q, f.guard, "Move_Ground", 0, numeric.Fixed(900<<16), numeric.Fixed(900<<16), 10, true)
		guard := q.Primary()[0]
		for tick := uint32(10); tick < 1000; tick++ {
			q.Pump(f.guard, tick)
		}
		if got, want := queueShape(q), "[Follow_Ground t2 p1][Move_Ground t0 p0]"; got != want || q.Primary()[0] != guard {
			t.Fatalf("queue after 990 ticks of guarding = %s, want %s with the original guard at the head", got, want)
		}
		for _, req := range f.installs {
			if req.X == numeric.Fixed(900<<16) && req.Z == numeric.Fixed(900<<16) {
				t.Fatal("the queued move installed its goal behind a live guard")
			}
		}
		*firstGone = true
		q.Pump(f.guard, 1000)
		if len(q.Primary()) == 0 || q.Primary()[0].ID != Lookup("Move_Ground") {
			t.Fatalf("queue after the ward vanished = %s, want the queued move at the head", queueShape(q))
		}
	})
}

// (c) One unit never guards two wards. A second guard Shift-queued behind the
// first is never admitted — it takes no admission draw and installs no follow
// goal — so every follow goal the unit is given is about the first ward.
func TestSecondQueuedGuardIsNeverAdmitted(t *testing.T) {
	eachGuardMode(t, func(t *testing.T, modern bool) {
		f, second, _ := guardQueueFixture(t, modern)
		q := QueueForUnit(f.guard)
		issue(q, f.guard, "Follow_Ground", f.ward.Handle, f.ward.X, f.ward.Z, 10, false)
		issue(q, f.guard, "Follow_Ground", second.Handle, second.X, second.Z, 10, true)
		for tick := uint32(10); tick < 1000; tick++ {
			q.Pump(f.guard, tick)
		}
		if got, want := queueShape(q), "[Follow_Ground t2 p1][Follow_Ground t3 p0]"; got != want {
			t.Fatalf("queue = %s, want %s", got, want)
		}
		if f.sim.Draws() != 1 {
			t.Fatalf("admission draws = %d, want exactly the first guard's one draw [04 R-ORD-01 §8 point 2]", f.sim.Draws())
		}
		// The follow radius of two one-cell units is 64 world units
		// [04 R-ORD-01 §8 point 1]; every install stays within it of ward one.
		for _, req := range f.installs {
			dx, dz := int64(req.X-f.ward.X)>>16, int64(req.Z-f.ward.Z)>>16
			if dx*dx+dz*dz > 65*65 {
				t.Fatalf("follow goal (%d,%d) is not about the first ward", req.X>>16, req.Z>>16)
			}
		}
	})
}
