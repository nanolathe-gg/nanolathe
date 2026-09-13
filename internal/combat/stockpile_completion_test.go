package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Completion restarts the next round in the same visit. Admission after the
// restart owns the deadline, and cumulative costs truncate independently
// [06 §11.1][06 R-WPN-05 §2].
func TestStockpileCompletionAttemptsNextRound(t *testing.T) {
	for _, rejectNext := range []bool{false, true} {
		name := "accepted"
		if rejectNext {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			w := &content.WeaponDef{ReloadTime: 12, EnergyPerShot: 7, MetalPerShot: 11}
			entry := &StockpileEntry{Weapon: w, Count: 2, Progress: 10}
			slot := &Slot{Weapon: w}
			var requests [][2]float32
			next, refresh, completed := TickStockpile(entry, slot, 100, func(e, m float32) bool {
				requests = append(requests, [2]float32{e, m})
				return !rejectNext || len(requests) == 1
			})
			// Final step: (7-5, 11-9). New first step: (2-0, 4-0).
			if len(requests) != 2 || requests[0] != [2]float32{2, 2} || requests[1] != [2]float32{2, 4} {
				t.Fatalf("requests %v, want final step [2 2] then first step [2 4]", requests)
			}
			wantProgress, wantNext := int32(5), uint32(105)
			if rejectNext {
				wantProgress, wantNext = 0, 110
			}
			if next != wantNext || entry.Progress != wantProgress || entry.Count != 1 || slot.Ammo != 1 || !refresh || completed != 1 {
				t.Fatalf("next=%d entry=%+v ammo=%d refresh=%t completed=%d; want next=%d progress=%d and one completion", next, entry, slot.Ammo, refresh, completed, wantNext, wantProgress)
			}
		})
	}
}
