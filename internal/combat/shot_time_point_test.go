package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestShotTimeAdmitsPointIsTheShooterHalfOnly locks the point form of the
// shot-time gate of [06 §3.3] — the form the rally task's member gate needs
// [08 R-AI-01 §19]. It is the planar range test plus, for a non-water weapon,
// the shooter-side sea-level clause and a ballistic solution; there is no
// target-side clause, because the rally point is a bare position with no
// definition to read a model top height from.
func TestShotTimeAdmitsPointIsTheShooterHalfOnly(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 50}
	def := &content.UnitDef{UnitName: "turret", ModelTop: 20}
	newShooter := func(weapon *content.WeaponDef) *units.Unit {
		u := &units.Unit{Def: def, X: 0, Y: numeric.FixedFromInt(40), Z: 0}
		if weapon != nil {
			u.SlotAt(0).Weapon = weapon
		}
		return u
	}
	// 100 world units of range: a target at 50 is inside, one at 200 is not.
	inside := numeric.FixedFromInt(50)
	outside := numeric.FixedFromInt(200)
	var svc Service

	direct := &content.WeaponDef{Range: 100}
	if !svc.ShotTimeAdmitsPoint(newShooter(direct), 0, inside, 0, 0, terrain) {
		t.Fatal("a point inside range with the shooter above sea level was refused")
	}
	if svc.ShotTimeAdmitsPoint(newShooter(direct), 0, outside, 0, 0, terrain) {
		t.Fatal("a point outside range was admitted")
	}

	// The shooter half: whole Y plus the definition's model top height must be
	// strictly greater than the sea-level byte. 40 + 20 = 60 > 50 passes above;
	// dropping the shooter to 20 makes it 40 <= 50 and rejects.
	drowned := newShooter(direct)
	drowned.Y = numeric.FixedFromInt(20)
	if svc.ShotTimeAdmitsPoint(drowned, 0, inside, 0, 0, terrain) {
		t.Fatal("a submerged shooter was admitted by a non-water weapon")
	}
	// A water weapon stops after the range test and admits the same shooter.
	drownedWater := newShooter(&content.WeaponDef{Range: 100, WaterWeapon: true})
	drownedWater.Y = numeric.FixedFromInt(20)
	if !svc.ShotTimeAdmitsPoint(drownedWater, 0, inside, 0, 0, terrain) {
		t.Fatal("a water weapon applied the shooter-side sea-level clause")
	}

	// A ballistic weapon needs a solution; zero velocity has none.
	ballistic := &content.WeaponDef{Range: 100, Ballistic: true}
	if svc.ShotTimeAdmitsPoint(newShooter(ballistic), 0, inside, 0, 0, terrain) {
		t.Fatal("a ballistic weapon with no solution was admitted")
	}

	// An empty slot 1 is skipped, which is how a building with no weapon there
	// reads the sentinel record [08 R-AI-01 §19].
	if svc.ShotTimeAdmitsPoint(newShooter(nil), 0, inside, 0, 0, terrain) {
		t.Fatal("a member with no weapon in slot 1 was admitted")
	}
}
