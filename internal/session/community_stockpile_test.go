package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

func TestCommunityStockpileCatalogClampIsBattleLocal(t *testing.T) {
	zero := &content.WeaponDef{ID: 1, Stockpile: true}
	wrapped := &content.WeaponDef{ID: 2, Stockpile: true, ReloadTime: 65536}
	ordinary := &content.WeaponDef{ID: 3, DefinitionHeader: content.DefinitionHeader{CanonicalKey: "ordinary"}}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"zero": zero, "wrapped": wrapped, "ordinary": ordinary},
		Units: map[string]*content.UnitDef{"builder": {Weapon1: "zero", Weapon1Def: zero}}}
	f := community.Features{BuildWeaponSlotGuard: true}
	for _, mode := range []gameplay.Mode{gameplay.Community39, gameplay.Modern} {
		got := prepareCommunityWeapons(cat, mode, f)
		if got == cat || got.Weapons["zero"].ReloadTime != 1 || got.Weapons["wrapped"].ReloadTime != 1 || got.Weapons["ordinary"].ReloadTime != 0 {
			t.Fatalf("mode %v did not apply only the stockpile zero-word clamp", mode)
		}
		if got.Units["builder"].Weapon1Def != got.Weapons["zero"] {
			t.Fatal("unit weapon link did not follow the derived definition")
		}
	}
	if zero.ReloadTime != 0 || wrapped.ReloadTime != 65536 || cat.Units["builder"].Weapon1Def != zero {
		t.Fatal("shared authored catalog changed")
	}
	if prepareCommunityWeapons(cat, gameplay.Strict31, f) != cat || prepareCommunityWeapons(cat, gameplay.Community39, community.Features{}) != cat {
		t.Fatal("Strict or disabled policy derived a catalog")
	}
}
