package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// TestSpawnSeedsNeutralAttackerSide locks the spawn seed of [06 R-WPN-04 §2]:
// the attacker-side snapshot is written to the neutral side 10 at creation,
// beside the null attacker pointer. The value matters because the `Under
// Attack` notice compares the stored snapshot with the victim's owner byte
// BEFORE the packet rewrites either — a zero seed silenced the first hit on
// every fresh unit owned by player 0.
func TestSpawnSeedsNeutralAttackerSide(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "seedtest"},
		UnitName:         "seedtest",
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)

	for _, owner := range []uint8{0, 3} {
		h, err := w.Create(def, owner, 0, 0, 0)
		if err != nil {
			t.Fatalf("create for owner %d: %v", owner, err)
		}
		if got := w.Unit(h).LastDamageSide; got != NeutralAttackerSide {
			t.Fatalf("owner %d spawned with attacker-side snapshot %d, want the neutral side %d", owner, got, NeutralAttackerSide)
		}
	}

	// The forced-slot allocator the staged battle restore uses carries the same
	// seed; nothing else writes the field at creation.
	h, err := w.CreateWithForcedSlot(def, 0, 0, 0, 0, pool.Handle(3))
	if err != nil {
		t.Fatalf("forced-slot create: %v", err)
	}
	if got := w.Unit(h).LastDamageSide; got != NeutralAttackerSide {
		t.Fatalf("forced-slot spawn carries attacker-side snapshot %d, want %d", got, NeutralAttackerSide)
	}
}

// TestRetailCodecRoundTripsAttackerSideSnapshot pins the wire side of the same
// field: record byte 0x8E IS the attacker-side snapshot, "the owner byte of the
// last unit that damaged it (10 = no attacker)" [08 R-SAVE-02 §6]
// [06 R-WPN-04 §2].
//
// This test used to assert the opposite — that the record carried no snapshot
// and the adapter must leave the live field alone — while the codec round
// tripped an obsolete opaque byte no producer ever set. A save therefore threw
// the snapshot away, and every reloaded unit compared against a stale value in
// the `Under Attack` gate.
func TestRetailCodecRoundTripsAttackerSideSnapshot(t *testing.T) {
	// Side 4 is neither the victim's owner (2) nor the neutral value, so a
	// codec that dropped the field could not accidentally reproduce it.
	for _, side := range []uint8{4, NeutralAttackerSide} {
		u := &Unit{
			Handle: 1, Def: &content.UnitDef{UnitName: "sidewire"}, Owner: 2, Alive: true,
			RestoredAIGroup: -1, LastDamageSide: side,
		}
		resolve := func(pool.Handle) (uint16, bool) { return 7, true }
		image, err := RetailUnitImage(u, 0, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatalf("save side %d: %v", side, err)
		}
		if image[0x8E] != side {
			t.Fatalf("record byte 0x8E = %d, want the attacker-side snapshot %d [08 R-SAVE-02 §6]", image[0x8E], side)
		}
		v := &Unit{Handle: 2, LastDamageSide: 9}
		if err := RetailUnitBase(v, image); err != nil {
			t.Fatalf("restore side %d: %v", side, err)
		}
		if v.LastDamageSide != side {
			t.Fatalf("restored snapshot = %d, want the saved %d [08 R-SAVE-02 §6]", v.LastDamageSide, side)
		}

		// The LIVE field is the writer's source: changing it and saving again
		// must move the byte. A writer reading some other field would keep
		// emitting the value the first save captured.
		v.Def, v.Owner, v.Alive, v.RestoredAIGroup = u.Def, u.Owner, true, -1
		v.LastDamageSide = 6
		again, err := RetailUnitImage(v, 0, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatalf("re-save side %d: %v", side, err)
		}
		if again[0x8E] != 6 {
			t.Fatalf("re-saved 0x8E = %d, want the live 6 [08 R-SAVE-02 §6]", again[0x8E])
		}
	}
}
