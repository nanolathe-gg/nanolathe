package units

// CNT-05 definition identity: unit creation stores the definition's stable
// 1-based catalog index as the pool occupancy identity, never a first-use
// counter, and rejects a definition that is not the finalized catalog's own
// record [P0-16 §3.2][02 §5][R-P0-03].

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// stampedCatalog returns a fixture catalog whose definitions carry the
// stable 1-based UnitDefID the category link pass stamps at compile
// [02 §5][R-P0-03]. CompileCategories is the production stamping pass; the
// test then marks the catalog finalized through its documented Compile-only
// hash field (Catalog.Finalized) exactly as Compile would.
func stampedCatalog(t *testing.T) *content.Catalog {
	t.Helper()
	units := map[string]*content.UnitDef{
		"armdef": {UnitName: "armdef", MaxDamage: 100},
		"cordef": {UnitName: "cordef", MaxDamage: 100},
	}
	for k, u := range units {
		u.CanonicalKey = content.CanonicalKey(k)
	}
	if _, err := content.CompileCategories(units); err != nil {
		t.Fatalf("CompileCategories: %v", err)
	}
	cat := &content.Catalog{Units: units, Hash: "fixture-catalog-hash"}
	if !cat.Finalized() {
		t.Fatal("fixture catalog should read as finalized")
	}
	return cat
}

// TestDefIDComesFromCatalogStampNotFirstUse locks the CNT-05 core: the
// stored identity is the definition's catalog index even when definitions
// are used in the opposite order. Under the removed first-use counter the
// first created definition received identity 1 regardless of its catalog
// position.
func TestDefIDComesFromCatalogStampNotFirstUse(t *testing.T) {
	cat := stampedCatalog(t)
	world := newFixtureWorld(len(cat.Units), cat)
	armDef := cat.Units["armdef"]
	corDef := cat.Units["cordef"]
	if armDef.UnitDefID == corDef.UnitDefID {
		t.Fatalf("stamps must differ: %d", armDef.UnitDefID)
	}
	// Create the higher catalog index first.
	hCor, err := world.Create(corDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create cordef: %v", err)
	}
	hArm, err := world.Create(armDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create armdef: %v", err)
	}
	if got := world.DefIDForHandle(hCor); got != uint16(corDef.UnitDefID) {
		t.Fatalf("cordef identity %d, want catalog stamp %d (first-use order leaked)", got, corDef.UnitDefID)
	}
	if got := world.DefIDForHandle(hArm); got != uint16(armDef.UnitDefID) {
		t.Fatalf("armdef identity %d, want catalog stamp %d", got, armDef.UnitDefID)
	}
	// The identity is stable across slot reuse.
	world.FreeImmediate(hCor)
	hCor2, err := world.Create(corDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("reuse cordef: %v", err)
	}
	if got := world.DefIDForHandle(hCor2); got != uint16(corDef.UnitDefID) {
		t.Fatalf("identity changed across slot reuse: %d want %d", got, corDef.UnitDefID)
	}
}

// TestCreateRejectsDefinitionOutsideFinalizedCatalog locks the CNT-05 gate:
// a definition that is not the finalized catalog's own record is rejected
// with an error and no allocation [P0-16 §3.2].
func TestCreateRejectsDefinitionOutsideFinalizedCatalog(t *testing.T) {
	cat := stampedCatalog(t)
	world := newFixtureWorld(len(cat.Units), cat)
	foreign := &content.UnitDef{UnitName: "foreign", MaxDamage: 100}
	foreign.CanonicalKey = content.CanonicalKey(foreign.UnitName)
	if _, err := world.Create(foreign, 0, 0, 0, 0); err == nil {
		t.Fatal("foreign definition was accepted by the canonical allocator")
	} else if !strings.Contains(err.Error(), "finalized catalog") {
		t.Fatalf("reject diagnostic = %v, want a finalized-catalog diagnostic", err)
	}
	if world.Used() != 0 {
		t.Fatalf("rejected definition allocated a slot: used=%d", world.Used())
	}
	// The forced-slot reconstruction path enforces the same gate [P0-16 §3.3].
	if _, err := world.CreateWithForcedSlot(foreign, 0, 0, 0, 0, 1); err == nil {
		t.Fatal("foreign definition was accepted by the forced-slot allocator")
	}
	if world.Used() != 0 {
		t.Fatalf("forced-slot reject allocated a slot: used=%d", world.Used())
	}
	// A member definition still allocates.
	h, err := world.Create(cat.Units["armdef"], 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("member definition rejected: %v", err)
	}
	if world.DefIDForHandle(h) != uint16(cat.Units["armdef"].UnitDefID) {
		t.Fatal("member identity is not its catalog stamp")
	}
}

// TestUnitIndexOfLocksCatalogOwnedRecord locks the content accessor: only
// the catalog's own record for a canonical key resolves, and the returned
// index is the stamped catalog position (mirrors ModelIndex [03 §2.4] C13).
func TestUnitIndexOfLocksCatalogOwnedRecord(t *testing.T) {
	cat := stampedCatalog(t)
	armDef := cat.Units["armdef"]
	idx, ok := cat.UnitIndexOf(armDef)
	if !ok || idx != armDef.UnitDefID {
		t.Fatalf("member lookup = (%d,%v), want stamp %d", idx, ok, armDef.UnitDefID)
	}
	// A same-key impostor that is not the catalog's record does not resolve.
	impostor := &content.UnitDef{UnitName: "armdef", MaxDamage: 1}
	impostor.CanonicalKey = armDef.CanonicalKey
	if _, ok := cat.UnitIndexOf(impostor); ok {
		t.Fatal("same-key impostor resolved as a catalog member")
	}
	if _, ok := cat.UnitIndexOf(nil); ok {
		t.Fatal("nil definition resolved")
	}
	empty := &content.Catalog{}
	if _, ok := empty.UnitIndexOf(armDef); ok {
		t.Fatal("catalog without units resolved a member")
	}
}

// TestFixtureWorldAcceptsUnstampedDefinitions documents the fixture shape:
// a world built without a catalog has no finalized identity to check, a
// definition carrying a stamp stores it, and a synthetic definition falls
// back to a fixture-scoped identity. Not retail behavior.
func TestFixtureWorldAcceptsUnstampedDefinitions(t *testing.T) {
	world := newFixtureWorld(4, nil)
	stamped := &content.UnitDef{UnitName: "stamped", MaxDamage: 100, UnitDefID: 7}
	h1, err := world.Create(stamped, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("stamped fixture definition rejected: %v", err)
	}
	if got := world.DefIDForHandle(h1); got != 7 {
		t.Fatalf("stamped fixture identity %d, want 7", got)
	}
	synthetic := &content.UnitDef{UnitName: "synthetic", MaxDamage: 100}
	h2, err := world.Create(synthetic, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("synthetic fixture definition rejected: %v", err)
	}
	if got := world.DefIDForHandle(h2); got == 0 || got == 7 {
		t.Fatalf("synthetic fixture identity %d, want a distinct nonzero fixture identity", got)
	}
}
