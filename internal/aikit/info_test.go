package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// An aircraft that drops bombs is a bomber even when it also carries an
// anti-air gun whose damage-table credit outweighs the bombs (the tech-2
// bombers do): a dropped weapon never acquires a target on its own
// [06 §3.2], it falls where the aircraft is sent. Fighters, whose missiles
// carry the damage-table anti-air credit, stay fighters.
func TestBombersAndFightersRetail(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	tab := BuildTable(cat, &construction.ModernRules{})
	for _, c := range []struct {
		key     string
		fighter bool
	}{
		{"armpnix", false}, {"corhurc", false},
		{"armthund", false}, {"corshad", false},
		{"armfig", true}, {"corveng", true},
	} {
		u := tab.Lookup(c.key)
		if u == nil {
			t.Errorf("%s: not in the retail table", c.key)
			continue
		}
		if !u.Role.Has(RoleAir | RoleCombat) {
			t.Errorf("%s: roles %b, want an armed aircraft", c.key, u.Role)
			continue
		}
		if got := u.Role.Has(RoleFighter); got != c.fighter || u.Role.Has(RoleBomber) == c.fighter {
			t.Errorf("%s: fighter %v bomber %v, want fighter %v", c.key, got, u.Role.Has(RoleBomber), c.fighter)
		}
		if dropsBombs(u.Def) == c.fighter {
			t.Errorf("%s: drops bombs %v", c.key, dropsBombs(u.Def))
		}
	}
}

// An interceptor (the stock anti-nukes, fixed and mobile) takes only
// projectile targets [06 §3.2]: it adds no damage per second and no reach
// against units. Counting its authored range made a remembered anti-nuke a
// threat disc over the whole map, and made the fixed ones defenses and the
// mobile ones combat units; a mobile one is not a scout either.
func TestInterceptorsAreNotFirepowerRetail(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	tab := BuildTable(cat, &construction.ModernRules{})
	for _, u := range tab.Units {
		if !intercepts(u.Def) {
			continue
		}
		if u.DPS != 0 || u.AirDPS != 0 || u.Range != 0 {
			t.Errorf("%s: DPS %d air %d range %d, want none from an interceptor", u.Key, u.DPS, u.AirDPS, u.Range)
		}
		if u.Role.Any(RoleDefense | RoleCombat | RoleScout | RoleAntiAir | RoleArtillery) {
			t.Errorf("%s: roles %b, want no armed or scout role", u.Key, u.Role)
		}
	}
	for _, key := range []string{"armamd", "corfmd", "armscab", "cormabm"} {
		if u := tab.Lookup(key); u == nil || !intercepts(u.Def) {
			t.Errorf("%s: not an interceptor carrier in the retail table", key)
		}
	}
}
