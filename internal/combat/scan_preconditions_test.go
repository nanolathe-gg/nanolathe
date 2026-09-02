package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/units"
)

// The autonomous target scan's PER-UNIT preconditions [06 §3.2]: "The visited
// unit must have a nonzero definition index, a remaining-build-fraction of
// exactly zero, one high status bit set, and its two-bit stance field equal to
// the fire-at-will value." Nothing read them before WU-19-87, so a nanoframe
// still under the nanolathe hunted targets and a unit set to HOLD FIRE fired at
// will.
func TestAutonomousScanPerUnitPreconditions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		prepare     func(u *units.Unit)
		wantAcquire bool
	}{
		{"a fully built fire-at-will unit scans", func(*units.Unit) {}, true},
		{"a unit still under construction does not scan", func(u *units.Unit) {
			u.Remaining = 0.5 // remaining build fraction is not exactly zero
		}, false},
		{"a unit holding fire does not scan", func(u *units.Unit) {
			u.Flags &^= units.StandingFieldMask << units.StandingFireShift // 0 = HOLD FIRE
		}, false},
		{"a unit at return fire does not scan", func(u *units.Unit) {
			u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | 1<<units.StandingFireShift
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, shooter, w, terrain, cat := commandFireProbe(t, false, ControlByteHuman)
			tc.prepare(shooter)
			if got := runProbeVisits(svc, shooter, w, terrain, cat, simRNGPtr(1)); got != tc.wantAcquire {
				t.Fatalf("after %d visits acquired=%v, want %v [06 §3.2]", visitsPerProbe, got, tc.wantAcquire)
			}
		})
	}
}

// The round-robin cursor of [06 §3.2]: the scan visits
// `(uint16)globalLiveUnitCount / 30 + 1` units per player per tick, advancing a
// persistent cursor through that player's unit vector and wrapping to its
// beginning at the end. The contract this locks is the one that matters for
// fairness — every live unit is reached once per cycle, none twice and none
// starved — and that the budget is the stated arithmetic.
func TestAutonomousScanCursorVisitsEveryUnitOncePerCycle(t *testing.T) {
	const players = 2
	const perPlayer = 5
	// Under thirty live units the budget is one unit per player per tick, so a
	// cycle is exactly the vector's length and every entry falls in exactly one
	// of its windows.
	const globalLive = 12

	var c autonomousScanCursor
	visits := make([][]int, players) // per player, per vector index
	for p := range visits {
		visits[p] = make([]int, perPlayer)
	}
	// Tick 1 measures each vector; the window opens from tick 2.
	c.beginTick(1, globalLive)
	for i := 0; i < perPlayer; i++ {
		for p := 0; p < players; p++ {
			_ = c.visits(uint8(p))
		}
	}
	if c.span != 1 {
		t.Fatalf("budget %d, want (uint16)%d/%d + 1 = 1 [06 §3.2]", c.span, globalLive, autonomousScanDivisor)
	}

	// Two full cycles from a settled cursor.
	for tick := uint32(2); tick < 2+2*perPlayer; tick++ {
		c.beginTick(tick, globalLive)
		for i := 0; i < perPlayer; i++ {
			for p := 0; p < players; p++ {
				if c.visits(uint8(p)) {
					visits[p][i]++
				}
			}
		}
	}
	for p := range visits {
		for i, n := range visits[p] {
			if n != 2 {
				t.Fatalf("player %d unit %d was visited %d times in two cycles, want 2: %v", p, i, n, visits[p])
			}
		}
	}
}

// The budget itself: `(uint16)globalLiveUnitCount / 30 + 1`, an integer divide
// of the GLOBAL live count [06 §3.2].
func TestAutonomousScanBudgetIsTheGlobalCountOverThirtyPlusOne(t *testing.T) {
	for _, tc := range []struct{ live, want int }{
		{0, 1}, {29, 1}, {30, 2}, {59, 2}, {60, 3}, {900, 31},
	} {
		var c autonomousScanCursor
		c.beginTick(1, tc.live)
		if c.span != tc.want {
			t.Fatalf("global live %d gives budget %d, want %d [06 §3.2]", tc.live, c.span, tc.want)
		}
	}
}

// A budget at least as large as the player's vector visits everything every
// tick, which is what every small engagement — and every fixture — sees.
func TestAutonomousScanCursorVisitsAllWhenTheBudgetCovers(t *testing.T) {
	var c autonomousScanCursor
	for tick := uint32(1); tick <= 4; tick++ {
		c.beginTick(tick, 3) // 3/30 + 1 = 1 per tick, and the player owns one unit
		if !c.visits(0) {
			t.Fatalf("tick %d: a player owning a single unit must scan it every tick", tick)
		}
	}
}
