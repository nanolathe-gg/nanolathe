package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type overrideScriptSurfaceFireRules struct{ StrictRules }

func (overrideScriptSurfaceFireRules) ScriptAttackSurfaceFire(ScriptAttackSurfaceFireRequest) bool {
	return true
}

func TestCommunitySurfaceFireAttackResolverUsesSlotZeroOnly(t *testing.T) {
	makeFixture := func(t *testing.T, rules Rules, feature, slotZeroTag, slotOneTag bool) (*units.Unit, *units.Unit) {
		t.Helper()
		actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
			d.CanAttack = true
			d.CanMove = true
			d.CanHover = true
			d.BMCode = 1
		}))
		target := mkUnit(2, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
			d.ModelTop = 10
			d.ModelTopFixed = 10 << 16
		}))
		actor.InstallWeapon(0, &content.WeaponDef{WaterWeapon: true, SurfaceFire: slotZeroTag})
		actor.InstallWeapon(1, &content.WeaponDef{WaterWeapon: true, SurfaceFire: slotOneTag})
		setTestHostility(actor, func(_, candidate *units.Unit) bool { return candidate == target })
		setTestSeaLevel(actor, 10)
		bindingOfUnit(actor).Community = community.Features{WeaponTargetKeys: feature}
		bindingOfUnit(actor).Rules = rules
		return actor, target
	}

	for _, tc := range []struct {
		name                       string
		rules                      Rules
		feature, slotZero, slotOne bool
		want                       string
	}{
		{"unbound is strict", nil, true, true, false, ""},
		{"explicit strict ignores enabled table", StrictRules{}, true, true, false, ""},
		{"community feature off", CommunityRules{}, false, true, false, ""},
		{"community slot zero tagged", CommunityRules{}, true, true, false, "Attack_Chase"},
		{"modern embeds community", &ModernRules{}, true, true, false, "Attack_Chase"},
		{"slot one alone inert", CommunityRules{}, true, false, true, ""},
		{"registered method override", overrideScriptSurfaceFireRules{}, false, false, false, "Attack_Chase"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor, target := makeFixture(t, tc.rules, tc.feature, tc.slotZero, tc.slotOne)
			if got := resolveAttackAt(actor, target, nil); got != tc.want {
				t.Fatalf("resolved %q, want %q", got, tc.want)
			}
		})
	}
}
