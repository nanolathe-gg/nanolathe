// The two frees are not interchangeable. FreeImmediate is the death
// finalizer's, for a unit that existed and was counted; FreeNeverCreated
// unwinds an allocation the engine must treat as never having happened
// [04 R-FAC-02 §3]. The counter halves are the difference
// [08 R-SKIR-01 §3 "Counters"][05 R-SHARE-01 §8].
package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestFreeNeverCreatedRestoresBothCounters(t *testing.T) {
	w := newFixtureWorld(5, nil)
	def := &content.UnitDef{UnitName: "never-created", MaxDamage: 100, Limit: -1}

	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if live, created := w.LiveCountForPlayer(0), w.CreatedCountForPlayer(0); live != 1 || created != 1 {
		t.Fatalf("after one allocation live=%d created=%d, want 1/1", live, created)
	}
	u := w.Unit(h)

	w.FreeNeverCreated(h)

	if live := w.LiveCountForPlayer(0); live != 0 {
		t.Fatalf("live count = %d, want 0", live)
	}
	if created := w.CreatedCountForPlayer(0); created != 0 {
		t.Fatalf("units-ever-created = %d, want 0: an allocation retail refused was never counted [05 R-SHARE-01 §8]", created)
	}
	if u.Dying || u.DeathCause != 0 {
		t.Fatalf("the freed record carries a death (dying=%v cause=%d), but it never existed [04 R-FAC-02 §3]", u.Dying, u.DeathCause)
	}
	if w.Unit(h) != nil {
		t.Fatalf("slot %d still holds a record", h)
	}
	// The slot is lowest-free reusable the same tick [P0-16 §6.3].
	again, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("reuse: %v", err)
	}
	if again != h {
		t.Fatalf("reuse gave slot %d, want the freed %d", again, h)
	}
	if created := w.CreatedCountForPlayer(0); created != 1 {
		t.Fatalf("units-ever-created = %d after one surviving allocation, want 1", created)
	}
}

// FreeImmediate keeps units-ever-created standing: the kill-record handler
// decrements the live count only [08 R-SKIR-01 §3 "Counters"]. Locked here so
// the new entry point cannot be "simplified" into the old one.
func TestFreeImmediateKeepsUnitsEverCreated(t *testing.T) {
	w := newFixtureWorld(5, nil)
	def := &content.UnitDef{UnitName: "died-for-real", MaxDamage: 100, Limit: -1}

	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w.FreeImmediate(h)

	if live := w.LiveCountForPlayer(0); live != 0 {
		t.Fatalf("live count = %d, want 0", live)
	}
	if created := w.CreatedCountForPlayer(0); created != 1 {
		t.Fatalf("units-ever-created = %d after a death, want 1: only the live count decrements [08 R-SKIR-01 §3]", created)
	}
}

// A never-existed free must not leave the freed handle in a carrier's cargo
// list: the slot is reusable in the same tick, so the stale entry would alias
// the next occupant [P0-16 §6.3].
func TestFreeNeverCreatedDropsCarrierLinkage(t *testing.T) {
	w := newFixtureWorld(5, nil)
	def := &content.UnitDef{UnitName: "carried-frame", MaxDamage: 100, Limit: -1}

	carrierH, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	cargoH, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create cargo: %v", err)
	}
	carrier, cargo := w.Unit(carrierH), w.Unit(cargoH)
	carrier.Attachment.Cargo = []pool.Handle{cargoH}
	cargo.Attachment.Carrier = carrierH
	cargo.Attachment.AttachPiece = 3

	w.FreeNeverCreated(cargoH)

	if len(carrier.Attachment.Cargo) != 0 {
		t.Fatalf("carrier keeps %v after the freed record left", carrier.Attachment.Cargo)
	}
	if cargo.Attachment.Carrier != 0 || cargo.Attachment.AttachPiece != -1 {
		t.Fatalf("freed record keeps carrier=%d piece=%d", cargo.Attachment.Carrier, cargo.Attachment.AttachPiece)
	}
}
