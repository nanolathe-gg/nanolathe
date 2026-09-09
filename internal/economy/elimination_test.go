package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestPlayerEliminatedDerivesFromTheTwoCounters locks the predicate of
// [05 R-SHARE-01 §3] — "the slot is not eliminated (`live unit count != 0` or
// `total units ever created == 0`)" — and the counter writers of
// [08 R-SKIR-01 §3] "Counters": both allocators increment both counters, the
// death finalizer decrements the live count only.
func TestPlayerEliminatedDerivesFromTheTwoCounters(t *testing.T) {
	w := units.NewSliced(4, &content.Catalog{})
	def := economyFixtureDef(&content.UnitDef{UnitName: "armcom", BuildTime: 100, MaxDamage: 100})
	def.CanonicalKey = content.CanonicalKey("armcom")

	// A slot that has never created a unit is NOT eliminated: the second term
	// of the predicate holds. This is what keeps a participating row alive
	// through battle entry [08 R-SKIR-01 §3] "Victory detection".
	if PlayerEliminated(w, 0) {
		t.Fatalf("player with no unit ever created must not be eliminated")
	}
	if PlayerEliminated(w, 1) {
		t.Fatalf("player 1 with no unit ever created must not be eliminated")
	}

	h, err := w.Create(def, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := w.LiveCountForPlayer(0); got != 1 {
		t.Fatalf("live count after creation = %d, want 1", got)
	}
	if got := w.CreatedCountForPlayer(0); got != 1 {
		t.Fatalf("ever-created after creation = %d, want 1", got)
	}
	if PlayerEliminated(w, 0) {
		t.Fatalf("player with a live unit must not be eliminated")
	}

	u := w.Unit(h)
	u.Dying = true
	if res := w.FinalizeDeath(h, 30); !res.Freed {
		t.Fatalf("FinalizeDeath did not free the slot")
	}
	// The death finalizer decrements the live count and leaves "ever created"
	// alone; only then is the slot eliminated [08 R-SKIR-01 §3] "Counters".
	if got := w.LiveCountForPlayer(0); got != 0 {
		t.Fatalf("live count after death = %d, want 0", got)
	}
	if got := w.CreatedCountForPlayer(0); got != 1 {
		t.Fatalf("ever-created after death = %d, want 1 (monotonic)", got)
	}
	if !PlayerEliminated(w, 0) {
		t.Fatalf("player whose last unit died must be eliminated")
	}
	// The neighbouring slot never created a unit and is still not eliminated.
	if PlayerEliminated(w, 1) {
		t.Fatalf("untouched slot must not become eliminated")
	}
	// Out-of-range rows and an unwired world answer "not eliminated": there is
	// no row 10 occupant to eliminate [05 "Player slot"].
	if PlayerEliminated(w, 10) || PlayerEliminated(nil, 0) {
		t.Fatalf("row 10 and a nil world must answer not eliminated")
	}
}
