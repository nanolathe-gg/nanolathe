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
// `(uint16)perPlayerUnitLimit / 30 + 1` RECORDS per player per tick, advancing a
// persistent cursor through that player's fixed record slice and wrapping to its
// beginning at the end. The contract this locks is the one that matters for
// fairness — every record is reached once per cycle, none twice and none
// starved — and that the budget is the stated arithmetic.
//
// Correction (WU-19-154): this test previously fed `beginTick` a GLOBAL LIVE
// COUNT and walked a compacted vector of live units, which is the reading
// [06 §3.2] corrects under "The budget word is the per-player unit limit". The
// dividend is a setup constant and the walk covers free records too.
func TestAutonomousScanCursorVisitsEveryRecordOncePerCycle(t *testing.T) {
	const players = 2
	// Under a limit of thirty the budget is one record per player per tick, so a
	// cycle is exactly the slice length and every record falls in exactly one of
	// its windows.
	const perPlayerLimit = 5

	var c autonomousScanCursor
	visits := make([][]int, players) // per player, per record index
	for p := range visits {
		visits[p] = make([]int, perPlayerLimit)
	}
	c.beginTick(1, perPlayerLimit)
	if c.span != 1 {
		t.Fatalf("budget %d, want (uint16)%d/%d + 1 = 1 [06 §3.2]", c.span, perPlayerLimit, autonomousScanDivisor)
	}

	// Two full cycles.
	for tick := uint32(1); tick < 1+2*perPlayerLimit; tick++ {
		c.beginTick(tick, perPlayerLimit)
		for i := 0; i < perPlayerLimit; i++ {
			for p := 0; p < players; p++ {
				if c.visits(uint8(p), i) {
					visits[p][i]++
				}
			}
		}
	}
	for p := range visits {
		for i, n := range visits[p] {
			if n != 2 {
				t.Fatalf("player %d record %d was visited %d times in two cycles, want 2: %v", p, i, n, visits[p])
			}
		}
	}
}

// The budget itself: `(uint16)perPlayerUnitLimit / 30 + 1`, an integer divide of
// the session's PER-PLAYER UNIT LIMIT — a setup constant, so the budget never
// moves as units are built or die [06 §3.2][05 R-SHARE-01 §7]. The stock
// campaign limit of 200 is the worked example the correction names.
func TestAutonomousScanBudgetIsThePerPlayerLimitOverThirtyPlusOne(t *testing.T) {
	for _, tc := range []struct{ limit, want int }{
		{0, 1}, {29, 1}, {30, 2}, {59, 2}, {60, 3}, {200, 7}, {250, 9}, {900, 31},
	} {
		var c autonomousScanCursor
		c.beginTick(1, tc.limit)
		if c.span != tc.want {
			t.Fatalf("per-player limit %d gives budget %d, want %d [06 §3.2]", tc.limit, c.span, tc.want)
		}
	}
}

// A budget at least as large as the player's slice visits every record every
// tick, which is what a tiny fixture pool sees.
func TestAutonomousScanCursorVisitsAllWhenTheBudgetCovers(t *testing.T) {
	var c autonomousScanCursor
	for tick := uint32(1); tick <= 4; tick++ {
		c.beginTick(tick, 1) // 1/30 + 1 = 1 per tick over a one-record slice
		if !c.visits(0, 0) {
			t.Fatalf("tick %d: a one-record slice must be scanned every tick", tick)
		}
	}
}

// The free records inside the window still spend budget: a player owning one
// unit at record 0 of a 200-record slice is revisited on the same fixed period
// as a player owning two hundred, which is the consequence [06 §3.2] draws from
// the corrected dividend and the opposite of what a live-count dividend gives.
func TestAutonomousScanFreeRecordsSpendBudget(t *testing.T) {
	const perPlayerLimit = 200 // the stock campaign default [08 "Counters"]
	var c autonomousScanCursor
	hits := make([]int, perPlayerLimit)
	// Over exactly `limit` ticks the cursor advances 7 records a tick and, since
	// 7 and 200 are coprime, starts a window on every record exactly once — so
	// every record, live or free, is covered exactly `span` times. A live-count
	// dividend would instead give a player owning one unit a window that covered
	// it every tick.
	for tick := uint32(1); tick <= perPlayerLimit; tick++ {
		c.beginTick(tick, perPlayerLimit)
		for r := range hits {
			if c.visits(0, r) {
				hits[r]++
			}
		}
	}
	if c.span != 7 {
		t.Fatalf("budget %d, want 200/30 + 1 = 7 [06 §3.2]", c.span)
	}
	for r, n := range hits {
		if n != c.span {
			t.Fatalf("record %d covered %d times over %d ticks, want %d [06 §3.2]", r, n, perPlayerLimit, c.span)
		}
	}
}
