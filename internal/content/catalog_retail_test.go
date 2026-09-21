//go:build retail

package content

import (
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// retailRoot returns the reference install root, or skips the test. content,
// cob and model import one another, so this package's tests cannot import
// internal/testsupport/retailcat (which imports content) without an import
// cycle — route through testsupport.RetailRoot directly and keep this
// package's own catalog cache below [PLAN_02 Tests; AGENTS.md §Test policy].
func retailRoot(t *testing.T) string {
	t.Helper()
	return testsupport.RetailRoot(t)
}

func mountRetail(t *testing.T) *vfs.FS {
	t.Helper()
	fs := vfs.New()
	if err := fs.MountGameDirectory(retailRoot(t)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	return fs
}

var (
	retailCatalogOnce sync.Once
	retailCatalog     *Catalog
	retailCatalogErr  error
)

// compiledRetailCatalog shares the immutable full-install catalog across the
// retail relationship tests. Compile is the expensive part of these tests;
// the tests only read the resulting catalog, so rebuilding it per assertion
// adds time without adding coverage.
func compiledRetailCatalog(t *testing.T) *Catalog {
	t.Helper()
	root := retailRoot(t)
	retailCatalogOnce.Do(func() {
		fs := vfs.New()
		if err := fs.MountGameDirectory(root); err != nil {
			retailCatalogErr = err
			return
		}
		defer fs.Close()
		retailCatalog, retailCatalogErr = Compile(fs)
	})
	if retailCatalogErr != nil {
		t.Fatalf("compile: %v", retailCatalogErr)
	}
	return retailCatalog
}

// TestCompileRelationships compiles the whole reference install and asserts
// relationships, never censuses: every unit's weapon links resolve or are
// empty, feature successors resolve, every side has its 30 anchors, build
// menus exist with buttons, and the downloadable enforcement left the stock
// corpus untouched (only download-record first products participate).
func TestCompileRelationships(t *testing.T) {
	cat := compiledRetailCatalog(t)
	if err := cat.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Every unit's weapon1 is empty or resolves.
	for key, u := range cat.Units {
		if u.Weapon1 != "" {
			if _, ok := cat.Weapon(u.Weapon1); !ok && u.Weapon1Def == nil {
				t.Errorf("unit %s: weapon1 %q does not resolve", key, u.Weapon1)
			}
		}
	}
	// Every side has exactly 30 anchors.
	for _, s := range cat.Sides {
		if len(s.Anchors) != 30 {
			t.Errorf("side %d anchors = %d, want 30", s.Index, len(s.Anchors))
		}
	}
	// Build menus exist and carry buttons; ARMCOM builds ARMSOLAR first (measured).
	if len(cat.BuildMenus) == 0 {
		t.Fatal("no build menu pages compiled from sidedata.tdf")
	}
	armcom, ok := cat.BuildMenus["armcom"]
	if !ok || len(armcom.Buttons) == 0 {
		t.Fatalf("armcom page missing or empty")
	}
	if armcom.Buttons[0] != "ARMSOLAR" {
		t.Fatalf("armcom first button = %q, want ARMSOLAR", armcom.Buttons[0])
	}
	// The downloadable enforcement walks only each download record's first
	// product, never a CANBUILD button name [02 §5] "downloadable
	// enforcement", [02 R-CAT-01 §8] step 3. Every stock download first
	// product already authors downloadable=1, so the stock catalog raises no
	// such warning at all and no menu-button definition is forced. ARMAAP is
	// an ARMADVANCE page button whose FBI omits downloadable=1: it must stay
	// clear. Campaign AI candidate selection rejects downloadable definitions
	// [08 R-P0-05 §3], so forcing the bit here silently empties its build set.
	const prefix = "Hey!  Somebody forgot to set downloadable=1 for "
	for _, w := range cat.Warnings {
		if strings.HasPrefix(w, prefix) {
			t.Errorf("stock catalog raised a downloadable warning; no stock download first product lacks the bit: %q", w)
		}
		if strings.HasPrefix(w, "Hey! Somebody") {
			t.Errorf("downloadable warning is not verbatim (two spaces after Hey!): %q", w)
		}
	}
	if u, ok := cat.Unit("ARMAAP"); !ok {
		t.Fatal("ARMAAP missing from the stock catalog")
	} else if u.Downloadable {
		t.Fatal("ARMAAP was forced downloadable; a CANBUILD button name must not participate in the enforcement [02 R-CAT-01 §8]")
	}
	// Census lock: the stock corpus keeps 158 definitions non-downloadable.
	clear := 0
	for _, u := range cat.UnitRecords() {
		if !u.Downloadable {
			clear++
		}
	}
	if clear != 158 {
		t.Errorf("non-downloadable stock definitions = %d, want 158 (the authored downloadable=0 census; the enforcement forces none)", clear)
	}
}

// TestCatalogHashStable locks C12: hashing the same compiled catalog and an
// independently copied catalog produces the same result, independent of map
// iteration (I1). The retail catalog itself is shared with the relationship
// tests because rebuilding the full install adds no hash coverage.
func TestCatalogHashStable(t *testing.T) {
	a := compiledRetailCatalog(t)
	if a.Hash == "" {
		t.Fatal("empty catalog hash")
	}
	first := catalogHash(a)
	second := catalogHash(a.Clone())
	if a.Hash != first || first != second {
		t.Fatalf("hash unstable: catalog=%s first=%s second=%s", a.Hash[:16], first[:16], second[:16])
	}
}

// TestWeaponStockFamilyMatchesRCONTENT02 locks the settled weapon-family
// facts on the reference install [02 §5 R-CONTENT-02]: the family is exactly
// Weapons/*.tdf and gamedata/weapons.tdf is inert. The parsed family's
// authored IDs are all unique, so any same-ID diagnostic means we are reading
// a file retail never reads (gamedata/weapons.tdf carries 33 sections whose
// authored IDs overlap the family's). ID 36 is [cormine2]
// (weapons/cormine2_weapon.tdf) alone; [earthquake] in the parsed family is
// weapons/earthquake.tdf at ID 227; record 0 is the inactive sentinel, stock
// [noweapon]; a link that names no record references record 0, inactive.
func TestWeaponStockFamilyMatchesRCONTENT02(t *testing.T) {
	cat := compiledRetailCatalog(t)
	// gamedata/weapons.tdf contributes nothing: the stock family has no
	// same-ID collision at all [02 §5 R-CONTENT-02].
	if dups := cat.WeaponDuplicates(); len(dups) != 0 {
		t.Fatalf("collision diagnostics on the stock family: %v", dups)
	}
	// ID 36 is [cormine2] alone, from weapons/cormine2_weapon.tdf.
	w36, ok := cat.WeaponByID(36)
	if !ok || w36.CanonicalKey != "cormine2" {
		t.Fatalf("ID 36 = (%v, %v), want cormine2", w36, ok)
	}
	if !strings.EqualFold(w36.Provenance.LogicalPath, "weapons/cormine2_weapon.tdf") {
		t.Fatalf("cormine2 provenance = %q, want weapons/cormine2_weapon.tdf", w36.Provenance.LogicalPath)
	}
	// Exactly one record carries ID 36.
	keys := make([]string, 0, len(cat.Weapons))
	for k := range cat.Weapons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if def := cat.Weapons[k]; def.ID == 36 && k != "cormine2" {
			t.Fatalf("second ID-36 record under %q", k)
		}
	}
	// [earthquake] in the parsed family is weapons/earthquake.tdf at ID 227 —
	// not the inert gamedata copy, which authors ID 36.
	eq, ok := cat.WeaponByName("earthquake")
	if !ok || eq.ID != 227 {
		t.Fatalf("earthquake = (%v, %v), want the ID-227 record", eq, ok)
	}
	if !strings.EqualFold(eq.Provenance.LogicalPath, "weapons/earthquake.tdf") {
		t.Fatalf("earthquake provenance = %q, want weapons/earthquake.tdf", eq.Provenance.LogicalPath)
	}
	// Record 0 is the inactive sentinel, stock [noweapon].
	w0, ok := cat.WeaponByID(0)
	if !ok || w0.CanonicalKey != "noweapon" {
		t.Fatalf("record 0 = (%v, %v), want noweapon", w0, ok)
	}
	// Runtime name resolution: case-insensitive first match from slot 0
	// upward [02 §5 R-CONTENT-02].
	if w, ok := cat.WeaponByName("CORMINE2"); !ok || w != w36 {
		t.Fatalf("WeaponByName(CORMINE2) = (%v, %v), want the cormine2 record", w, ok)
	}
	// A link that names no record references record 0 — inactive, not an
	// error. Stock FBIs name placeholder explosions (medium_unitex and friends)
	// that exist in no parsed weapon file; they are the sentinel case.
	def, active := cat.WeaponLink("medium_unitex")
	if active || def != w0 {
		t.Fatalf("WeaponLink(medium_unitex) = (%v, %v), want the record-0 sentinel, inactive", def, active)
	}
	// A resolvable link is active and returns the first-match record.
	if def, active = cat.WeaponLink("cormine2"); !active || def != w36 {
		t.Fatalf("WeaponLink(cormine2) = (%v, %v), want active cormine2", def, active)
	}
	// Unit weapon links compile against the surviving records through the
	// first-match scan.
	if u, ok := cat.Unit("cormine2"); ok && u.ExplodeAsDef == nil {
		t.Fatal("cormine2.fbi explodeas no longer resolves")
	}
}

// TestMovementChainedDefaults is fixture-only per PLAN_02: authored maxslope
// alone means badslope == maxslope/2, and the clamps apply in order. See
// compile_movement_test.go.
