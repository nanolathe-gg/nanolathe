package combat

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"testing"
)

func TestStockpileVisitReadsReloadWordUnsigned(t *testing.T) {
	weapon := &content.WeaponDef{ReloadTime: -1, Stockpile: true, EnergyPerShot: 65535}
	slot := &Slot{Weapon: weapon}
	entry := &StockpileEntry{Weapon: weapon, Count: 1}
	var charged float32
	next, _, completed := TickStockpile(entry, slot, 100, func(energy, metal float32) bool { charged = energy; return true })
	if entry.Progress != 5 || charged != 5 || completed != 0 || slot.Ammo != 0 || next != 105 {
		t.Fatalf("unsigned reload visit: progress=%d cost=%v rounds=%d ammo=%d next=%d", entry.Progress, charged, completed, slot.Ammo, next)
	}
}

func TestStockpileVisitRetainsLowWordCost(t *testing.T) {
	weapon := &content.WeaponDef{ReloadTime: 10, Stockpile: true, EnergyPerShot: 4294967296}
	slot := &Slot{Weapon: weapon}
	entry := &StockpileEntry{Weapon: weapon, Count: 1}
	var charged float32
	TickStockpile(entry, slot, 0, func(energy, metal float32) bool { charged = energy; return true })
	// Half the cost is 2147483648: signed-64 truncation retains the negative
	// low word, which remains exactly representable in the admission float.
	if charged != -2147483648 || entry.Progress != 5 {
		t.Fatalf("first visit cost/progress = %v/%d", charged, entry.Progress)
	}
	TickStockpile(entry, slot, 5, func(energy, metal float32) bool { charged = energy; return true })
	// The full cumulative cost wraps to zero; subtracting the previous low
	// word wraps again before its single-precision store [06 §11.1].
	if charged != -2147483648 || slot.Ammo != 1 || entry.Count != 0 {
		t.Fatalf("second visit cost/ammo/count = %v/%d/%d", charged, slot.Ammo, entry.Count)
	}
	if got := StockpileCostDelta(0, 0, 0, 0); got != 0 {
		t.Fatalf("zero-time sentinel delta = %v", got)
	}
}
