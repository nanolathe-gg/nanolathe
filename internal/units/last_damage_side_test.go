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

// TestRetailRestoreKeepsAttackerSideSnapshot pins the restore side of the same
// field. The retail unit image the base adapter parses carries the last damage
// CAUSE but no attacker-side snapshot, so the adapter must leave whatever the
// caller restored in place rather than resetting it to the spawn seed.
func TestRetailRestoreKeepsAttackerSideSnapshot(t *testing.T) {
	u := &Unit{LastDamageSide: NeutralAttackerSide}
	data := make([]byte, retailUnitRecordSize)
	if err := RetailUnitBase(u, data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := u.LastDamageSide; got != NeutralAttackerSide {
		t.Fatalf("restore rewrote an unseeded snapshot to %d", got)
	}

	saved := &Unit{LastDamageSide: 4}
	if err := RetailUnitBase(saved, data); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := saved.LastDamageSide; got != 4 {
		t.Fatalf("restore overwrote the saved attacker-side snapshot with %d, want 4", got)
	}
}
