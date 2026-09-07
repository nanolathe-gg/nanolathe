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

// Budget includes empty records and narrows the stored limit before division [06 §3.2].
func TestAutonomousScanBudgetIsThePerPlayerLimitOverThirtyPlusOne(t *testing.T) {
	for _, tc := range []struct{ limit, want int }{{0, 1}, {29, 1}, {30, 2}, {59, 2}, {60, 3}, {200, 7}, {250, 9}, {900, 31}, {65536, 1}} {
		if got := autonomousScanBudget(tc.limit); got != tc.want {
			t.Fatalf("limit %d: budget %d, want %d", tc.limit, got, tc.want)
		}
	}
}

func TestAutonomousCursorPreservesWrappedOrderAndInactivePlayer(t *testing.T) {
	var c autonomousScanCursor
	for i := 0; i < 202; i++ {
		if got := c.nextRecord(0, 200); got != i%200 {
			t.Fatalf("visit %d: record %d", i, got)
		}
	}
	// Player 1 was skipped for the entire interval; it still starts at its first record.
	if got := c.nextRecord(1, 200); got != 0 {
		t.Fatalf("skipped player advanced to %d", got)
	}
	if got := c.nextRecord(0, 200); got != 2 {
		t.Fatalf("other player changed cursor to %d", got)
	}
}

func TestAutonomousScanFreeRecordsSpendBudget(t *testing.T) {
	var c autonomousScanCursor
	var hits [200]int
	for tick := 0; tick < 200; tick++ {
		for visit := 0; visit < autonomousScanBudget(200); visit++ {
			hits[c.nextRecord(0, 200)]++
		}
	}
	for record, n := range hits {
		if n != 7 {
			t.Fatalf("record %d: %d visits, want 7", record, n)
		}
	}
}
