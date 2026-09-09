package content

import "testing"

func TestRestoredWeaponByteIsBattleLocalAndKeepsIdentity(t *testing.T) {
	original := &Catalog{Weapons: map[string]*WeaponDef{"gun": {ID: 173}}}
	battle := original.Clone()
	weapon := battle.Weapons["gun"]
	if weapon.ActiveByte() != 173 {
		t.Fatal("fresh definition byte was reduced to a boolean")
	}
	weapon.RestoreActiveByte(0)
	if !IsWeaponInactive(weapon) || weapon.ID != 173 {
		t.Fatal("restored byte did not control activity independently of identity")
	}
	if original.Weapons["gun"].ActiveByte() != 173 {
		t.Fatal("restoration leaked into caller catalog")
	}
	second := battle.Clone()
	second.Weapons["gun"].RestoreActiveByte(31)
	if weapon.ActiveByte() != 0 || second.Weapons["gun"].ID != 173 {
		t.Fatal("definition clone shared restored state or changed identity")
	}
}
