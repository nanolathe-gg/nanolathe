package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func TestRawUnitRecordRetainsFreedSlotUntilReuse(t *testing.T) {
	w := newFixtureWorld(2, nil)
	def := &content.UnitDef{UnitName: "raw-record", MaxDamage: 100}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	stale := w.Unit(h)
	stale.Kills = 7
	stale.Orders = []int{1}
	stale.RenderPieceFlags = []uint8{7}
	w.Destroy(h, DeathKilled)
	if got := w.FinalizeDeath(h, 1); !got.Freed {
		t.Fatalf("FinalizeDeath = %+v, want freed", got)
	}
	if got := w.Unit(h); got != nil {
		t.Fatalf("live lookup after free = %#v, want nil", got)
	}
	if got := w.RawUnitRecord(h); got == stale || got == nil || got.Handle != h || got.Owner != 0 || got.Kills != 7 || got.Orders != nil || got.RenderPieceFlags != nil {
		t.Fatalf("raw freed record = %#v, want only retained slot/owner/kill fields", got)
	}
	reused, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reused != h {
		t.Fatalf("reused slot = %d, want %d", reused, h)
	}
	if got := w.RawUnitRecord(h); got != w.Unit(reused) || got == stale || got.Kills != 0 {
		t.Fatalf("raw reused record = %#v, want new live zero-kill occupant", got)
	}
}

func TestRawUnitRecordDropsNeverCreatedAllocation(t *testing.T) {
	w := newFixtureWorld(2, nil)
	def := &content.UnitDef{UnitName: "raw-never-created", MaxDamage: 100}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.FreeNeverCreated(h)
	if got := w.RawUnitRecord(h); got != nil {
		t.Fatalf("never-created raw record = %#v, want nil", got)
	}

	reused, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reused != h || w.RawUnitRecord(h) != w.Unit(reused) {
		t.Fatalf("reused raw record did not name current slot occupant")
	}
}
