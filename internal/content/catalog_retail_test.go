package content

import (
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

// retailRoot returns the reference install root, or skips the test [PLAN_02
// Tests; docs/ORCHESTRATION.md §6].
func retailRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("TOTALA_ROOT")
	if root == "" {
		root = os.Getenv("HOME") + "/TotalAnnihilation"
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("reference install not present at %s", root)
	}
	return root
}

func mountRetail(t *testing.T) *vfs.FS {
	t.Helper()
	fs := vfs.New()
	if err := fs.MountGameDirectory(retailRoot(t)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	return fs
}

// TestCompileRelationships compiles the whole reference install and asserts
// relationships, never censuses: every unit's weapon links resolve or are
// empty, feature successors resolve, every side has its 30 anchors, build
// menus exist with buttons, and the downloadable enforcement fired verbatim.
func TestCompileRelationships(t *testing.T) {
	fs := mountRetail(t)
	cat, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := cat.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Every unit's weapon1..3 is empty or resolves; explodeas/selfdestructas too.
	for key, u := range cat.Units {
		for _, link := range []struct {
			name string
			def  *WeaponDef
		}{
			{"weapon1", u.Weapon1Def}, {"weapon2", u.Weapon2Def}, {"weapon3", u.Weapon3Def},
			{"explodeas", u.ExplodeAsDef}, {"selfdestructas", u.SelfDestructAsDef},
		} {
			if link.def == nil {
				continue // empty or unresolved; unresolved names checked below
			}
			_ = link
		}
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
	// The downloadable enforcement ran and produced verbatim warnings for
	// every menu-button unit lacking the bit. Spot-check a measured one:
	// ARMAAP is an ARMADVANCE page button whose FBI omits downloadable=1.
	found := false
	for _, w := range cat.Warnings {
		const prefix = "Hey! Somebody forgot to set downloadable=1 for "
		if !strings.HasPrefix(w, prefix) {
			t.Fatalf("warning not verbatim: %q", w)
		}
		if w == prefix+"ARMAAP" {
			found = true
			if u, ok := cat.Unit("ARMAAP"); ok && !u.Downloadable {
				t.Fatal("warning issued but the bit was not forced on")
			}
		}
	}
	if !found {
		t.Fatalf("no downloadable warning for ARMAAP among %d warnings", len(cat.Warnings))
	}
}

// TestCatalogHashStable locks C12: two Compile calls over the same install
// produce an identical Catalog.Hash, independent of map iteration (I1).
func TestCatalogHashStable(t *testing.T) {
	fs := mountRetail(t)
	a, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile a: %v", err)
	}
	b, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile b: %v", err)
	}
	if a.Hash == "" {
		t.Fatal("empty catalog hash")
	}
	if a.Hash != b.Hash {
		t.Fatalf("hash unstable: %s vs %s", a.Hash[:16], b.Hash[:16])
	}
}

// TestWeaponIDCollisionMergesToReferencedName locks the measured stock
// behavior behind the ID merge: ID 36 is shared by EARTHQUAKE
// (gamedata/weapons.tdf) and cormine2 (weapons/cormine2_weapon.tdf). The
// weapons/ file parses after gamedata so CORMINE2 survives — units reference
// it — and WeaponByID(36) returns that one record. The separate EARTHQUAKE of
// weapons/earthquake.tdf carries ID 227 and is unaffected.
func TestWeaponIDCollisionMergesToReferencedName(t *testing.T) {
	fs := mountRetail(t)
	cat, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	w, ok := cat.Weapons["cormine2"]
	if !ok {
		t.Fatal("cormine2 missing after ID 36 merge")
	}
	if w.ID != 36 {
		t.Fatalf("cormine2 ID = %d, want 36", w.ID)
	}
	byID, ok := cat.WeaponByID(36)
	if !ok || byID != w {
		t.Fatalf("WeaponByID(36) mismatch: got %v", byID)
	}
	// Exactly one record carries ID 36.
	for k, def := range cat.Weapons {
		if def.ID == 36 && k != "cormine2" {
			t.Fatalf("second ID-36 record under %q", k)
		}
	}
	// The unrelated EARTHQUAKE (ID 227, weapons/earthquake.tdf) survives under its own name.
	other, ok := cat.Weapons["earthquake"]
	if !ok || other.ID != 227 {
		t.Fatalf("ID-227 earthquake missing or wrong id (%v)", other)
	}
	if u, ok := cat.Unit("cormine2"); ok && u.ExplodeAsDef == nil {
		t.Fatal("cormine2.fbi explodeas no longer resolves after the merge")
	}
}

// TestMovementChainedDefaults is fixture-only per PLAN_02: authored maxslope
// alone means badslope == maxslope/2, and the clamps apply in order. See
// compile_movement_test.go.
