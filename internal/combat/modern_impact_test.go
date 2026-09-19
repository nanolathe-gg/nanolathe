package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A direct projectile can first collide after passing the victim's center.
// Its impact-side byte then points away from the shooter. Modern withdrawal
// uses the actual incoming motion; the retail packet direction stays intact.
func TestModernImpactUsesIncomingMotionPastVictimCenter(t *testing.T) {
	for _, modern := range []bool{true, false} {
		for _, area := range []int32{8, 64} {
			for _, speed := range []int64{16, 20} {
				for _, heading := range []uint16{0, 16384} {
					s, w, terrain, shooter, victim, weapon := modernCombatFixture(t)
					if !modern {
						s.Rules = StrictRules{}
					}
					weapon.AreaOfEffect = area
					victim.Move.Heading = heading
					p := Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner,
						Pos:      Vec3{X: numeric.FixedFromInt(72), Y: victim.Y, Z: victim.Z},
						Velocity: Vec3{X: numeric.FixedFromInt(speed)}}
					p.Pos.X += p.Velocity.X
					if hit, _, _, _, _, _ := checkCollision(&p, weapon, w, terrain, nil, false); hit != victim.Handle {
						t.Fatalf("fixture must contact victim after motion: %d", hit)
					}
					beforeDirection := hitDirectionByte(&p, victim)
					called := false
					s.ImpactNotice = func(v, a *units.Unit, bearing numeric.Angle, tick uint32) {
						called = true
						if v != victim || a != shooter || bearing != 49152 || tick != 1 {
							t.Fatalf("incoming bearing=%d, want west", bearing)
						}
					}
					before := victim.Health
					applyProjectileDamage(s, &p, weapon, w, terrain, 1, victim.Handle)
					if called != modern || victim.Health >= before || hitDirectionByte(&p, victim) != beforeDirection {
						t.Fatalf("modern=%v called=%v health=%d/%d direction changed=%v", modern, called, victim.Health, before, hitDirectionByte(&p, victim) != beforeDirection)
					}
				}
			}
		}
	}
}
