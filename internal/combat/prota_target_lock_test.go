package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// autonomousScanAdmitsUnit is the retail admission of a visited record, the
// form the armed-bit tests exercise.
func autonomousScanAdmitsUnit(u *units.Unit) bool { return autonomousScanAdmits(u, false) }

// TestProTATargetLockRelease locks the shipped 4.8 change to the autonomous
// maintenance scan (research/extensions/prota-engine.md "shipped target
// retention and firing boundary"): a retained unit target that fails the
// unit-to-unit physical gate is released, and the scan admits the encoded
// standing-fire value three. The zero switch, and Strict's rules whatever the
// table says, keep retail's retention.
func TestProTATargetLockRelease(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rules     Rules
		on        bool
		weaponRng int32
		stance    uint32
		wantKept  bool
	}{
		{"retail keeps an out-of-range target", StrictRules{}, true, 5, 2, true},
		{"community with the switch off keeps it", CommunityRules{}, false, 5, 2, true},
		{"release drops an out-of-range target", CommunityRules{}, true, 5, 2, false},
		{"release keeps an in-range target", CommunityRules{}, true, 1000, 2, true},
		{"release admits standing-fire value three", CommunityRules{}, true, 5, 3, false},
		{"retail does not visit standing-fire value three", StrictRules{}, true, 5, 3, true},
		{"modern inherits the release", &ModernRules{}, true, 5, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			weapon := &content.WeaponDef{ID: 51, Range: tc.weaponRng, LineOfSight: true, WeaponVelocity: 100 * 65536 / 30}
			shooter.InstallWeapon(0, weapon)
			shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			shooter.Flags = shooter.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | tc.stance<<units.StandingFireShift
			cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
			cat.RebuildWeaponIndex()
			svc := &Service{Rules: tc.rules, Community: community.Features{TargetLockRelease: tc.on}}
			svc.StepAutonomousForPlayer(shooter.Owner, w, nil, terrain, nil, cat, nil)
			got := shooter.SlotAt(0).Target
			if kept := got.Kind == units.TargetUnit && got.Unit == target.Handle; kept != tc.wantKept {
				t.Fatalf("target kept=%v (%+v), want %v", kept, got, tc.wantKept)
			}
		})
	}
}
