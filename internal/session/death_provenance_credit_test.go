package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Damage stamps only nonzero attackers; the local death packet takes its
// attacker from the resulting stored link [06 §9.1][06 §12.1]. Exercise both
// intake and finalization, including the sixth-kill lead consumer [06 §3.3].
func TestLethalDamageFinalizesStoredAttackerCredit(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				priorHit    bool
				replacement bool
			}{
				{name: "shooterless retains earlier attacker", priorHit: true},
				{name: "shooterless without earlier attacker"},
				{name: "nonzero replaces earlier attacker", priorHit: true, replacement: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s, attacker, def := killCreditFixture(t)
					s.SetGameplay(mode)
					// The fixture bypasses content compilation; supply its absent-key
					// defaults so all three modes use the same sixth-kill lead gate.
					attacker.Def.VeterancyThresholds = []uint32{5, 10, 15, 20, 25}
					attacker.Kills = 5
					h, err := s.Units.Create(def, 1, 0, 0, 0)
					if err != nil {
						t.Fatal(err)
					}
					victim := s.Units.Unit(h)
					victim.Remaining = 0
					if tc.priorHit {
						got := s.Combat.AcceptDamage(s.Units, 1, combat.DamageInput{
							Victim: h, Attacker: attacker.Handle, Kind: combat.KindOrdinary, Nominal: 10,
						})
						if !got.Accepted || got.DeathLatched || victim.EngagementTarget != attacker.Handle {
							t.Fatal("first hit did not establish attacker provenance")
						}
					}
					var incoming, wantLink pool.Handle
					var credited *units.Unit
					if tc.priorHit {
						wantLink, credited = attacker.Handle, attacker
					}
					if tc.replacement {
						incoming, err = s.Units.Create(attacker.Def, attacker.Owner, 0, 0, 0)
						if err != nil {
							t.Fatal(err)
						}
						credited = s.Units.Unit(incoming)
						credited.Kills = 5
						wantLink = incoming
					}
					slot := &units.Slot{Flags: units.SlotFlagEnabled}
					weapon := &content.WeaponDef{WeaponVelocity: 1 << 16}
					point := combat.Vec3{X: numeric.FixedFromInt(100)}
					target := &units.Unit{Def: def}
					target.Move.VelX = numeric.FixedFromInt(1)
					if credited != nil && combat.PreFireLeadPoint(credited, target, slot, weapon, point, s.Combat) != point {
						t.Fatal("five kills led the aim point before death finalization")
					}

					got := s.Combat.AcceptDamage(s.Units, 2, combat.DamageInput{
						Victim: h, Attacker: incoming, Kind: combat.KindOrdinary, Nominal: 1000,
					})
					if !got.DeathLatched || victim.EngagementTarget != wantLink {
						t.Errorf("lethal intake = %+v, attacker link %d, want %d", got, victim.EngagementTarget, wantLink)
					}
					if credited != nil && (credited.Kills != 5 || s.Econ.Players[attacker.Owner].Kills != 0) {
						t.Fatal("death latch awarded credit before finalization")
					}
					s.stepUnitPhase(3)
					if s.Units.Unit(h) != nil {
						t.Fatal("victim survived normal death finalization")
					}
					wantKills := int16(0)
					if credited != nil {
						wantKills = 1
						if credited.Kills != 6 {
							t.Errorf("credited unit kills = %d, want 6", credited.Kills)
						}
						if led := combat.PreFireLeadPoint(credited, target, slot, weapon, point, s.Combat); led.X <= point.X || led.Y != point.Y || led.Z != point.Z {
							t.Errorf("sixth finalized kill did not lead the moving target: point %+v, led %+v", point, led)
						}
					}
					if credited != attacker && attacker.Kills != 5 {
						t.Errorf("earlier unit received unrelated credit: kills %d, want 5", attacker.Kills)
					}
					if got := s.Econ.Players[attacker.Owner].Kills; got != wantKills {
						t.Errorf("player kills = %d, want %d", got, wantKills)
					}
					if got := s.Econ.Players[victim.Owner].Losses; got != 1 {
						t.Errorf("victim player losses = %d, want 1", got)
					}
				})
			}
		})
	}
}

// The ordinary death-explosion producer carries a null shooter [06 §12.2].
// A neighbor it finishes still credits its own earlier attacker [06 §12.1].
func TestDeathExplosionRetainsNeighborsEarlierAttackerCredit(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			s, attacker, def := killCreditFixture(t)
			s.SetGameplay(mode)
			def.CanMove, def.FootprintX, def.FootprintZ = true, 1, 1
			x, z := world.CellToWorld(11), world.CellToWorld(10)
			h, err := s.Units.Create(def, 1, x, s.World.HeightAt(x, z), z)
			if err != nil {
				t.Fatal(err)
			}
			victim := s.Units.Unit(h)
			victim.Remaining = 0
			s.Movement.EnsureUnit(victim)
			s.Combat.AcceptDamage(s.Units, 1, combat.DamageInput{
				Victim: h, Attacker: attacker.Handle, Kind: combat.KindOrdinary, Nominal: 10,
			})
			if victim.Dying || victim.EngagementTarget != attacker.Handle {
				t.Fatal("initial nonlethal hit did not establish provenance")
			}

			explDef := *def
			explDef.ExplodeAsDef = &content.WeaponDef{ID: 99, DamageDefault: 5000, AreaOfEffect: 64}
			x = world.CellToWorld(10)
			explHandle, err := s.Units.Create(&explDef, 1, x, s.World.HeightAt(x, z), z)
			if err != nil {
				t.Fatal(err)
			}
			exploder := s.Units.Unit(explHandle)
			exploder.Remaining = 0
			s.Combat.AcceptDamage(s.Units, 2, combat.DamageInput{
				Victim: explHandle, Kind: combat.KindOrdinary, Nominal: 1000,
			})
			// The exploder follows its neighbor in the slot walk, so its explosion
			// latches the neighbor for the next phase's death visit.
			s.stepUnitPhase(3)
			if s.Units.Unit(explHandle) != nil || !victim.Dying {
				t.Fatalf("death explosion failed to latch neighbor: exploder live=%v, neighbor dying=%v", s.Units.Unit(explHandle) != nil, victim.Dying)
			}
			if victim.EngagementTarget != attacker.Handle {
				t.Errorf("explosion replaced earlier attacker %d with %d", attacker.Handle, victim.EngagementTarget)
			}
			s.stepUnitPhase(4)
			if s.Units.Unit(h) != nil || attacker.Kills != 1 || s.Econ.Players[attacker.Owner].Kills != 1 {
				t.Fatalf("explosion finalization: victim live=%v, attacker kills=%d, player kills=%d", s.Units.Unit(h) != nil, attacker.Kills, s.Econ.Players[attacker.Owner].Kills)
			}
			if s.Econ.Players[1].Kills != 0 {
				t.Fatal("death explosion credited the exploder's owner")
			}
		})
	}
}
