package combat

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// rulesForModern binds the rule set the central gameplay mode selects, so each
// case below exercises the Modern contract and the Strict bypass through the
// same seam the session binds [I11].
func rulesForModern(modern bool) Rules {
	if modern {
		return &ModernRules{}
	}
	return StrictRules{}
}

// holdFireFixture is one held shooter with a targeted slot 0. ordered selects
// the slot's provenance through the control byte's autonomy bit: clear is a
// slot an order holds, set is a target the unit took on its own
// [06 R-WPN-05 §3].
func holdFireFixture(t *testing.T, weaponKind string, targetKind units.TargetKind, ordered bool) (*units.World, *world.Terrain, *units.Unit, *units.Slot, *economy.Service) {
	w, terrain, u, target := newTestWorldAndUnits(t)
	weapon := &content.WeaponDef{ID: 41, Range: 1000, LineOfSight: true, WeaponVelocity: 65536, Tolerance: wideDriftTolerance, PitchTolerance: wideDriftTolerance, ReloadTime: 30, EnergyPerShot: 7, MetalPerShot: 3, CommandFire: weaponKind == "command", Stockpile: weaponKind == "stockpile"}
	u.InstallWeapon(0, weapon)
	slot := u.SlotAt(0)
	if ordered {
		slot.Flags &^= units.SlotFlagAutonomous
	}
	slot.Target = units.Target{Kind: targetKind, Unit: target.Handle, X: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(10)}
	slot.Reload, slot.Ammo = 2, 3
	u.Flags &^= units.StandingFieldMask << units.StandingFireShift
	econ := &economy.Service{}
	econ.Players[u.Owner].Stock[economy.Energy], econ.Players[u.Owner].Stock[economy.Metal] = 100, 100
	return w, terrain, u, slot, econ
}

// Nanolathe Modern policy: docs/DESIGN_WEAPONS_PROJECTILES.md §2.6.1. A target
// the held unit took on its own is retained and never worked.
func TestModernHoldFireSuppressesAutonomousTargetsAndResumes(t *testing.T) {
	for _, targetKind := range []units.TargetKind{units.TargetUnit, units.TargetGround} {
		for _, weaponKind := range []string{"ordinary", "command", "stockpile"} {
			for _, resume := range []string{"stance", "strict"} {
				t.Run(fmt.Sprintf("%s/%s/target%d", weaponKind, resume, targetKind), func(t *testing.T) {
					w, terrain, u, slot, econ := holdFireFixture(t, weaponKind, targetKind, false)
					originalTarget := slot.Target
					random := rng.NewSimulation(77)
					beforeRandom := random
					beforePlayer := econ.Players[u.Owner]
					beforePending, beforeReveal := u.Pending, u.RevealDeadline
					svc := &Service{Rules: &ModernRules{}}
					events := 0
					svc.Events = func(Event) { events++ }
					for tick := uint32(1); tick <= 3; tick++ {
						sum := svc.StepWeaponsForUnit(u, tick, w, nil, terrain, econ, nil, &random, nil)
						if sum.Fired != 0 || sum.Dispatched || svc.Count() != 0 {
							t.Fatalf("held unit launched or aimed: %+v", sum)
						}
					}
					if slot.Reload != 0 || slot.Ammo != 3 || slot.Target != originalTarget || random != beforeRandom || econ.Players[u.Owner] != beforePlayer || u.Pending != beforePending || u.RevealDeadline != beforeReveal || events != 0 {
						t.Fatal("Hold Fire changed target or shot effects; only reload countdown may advance")
					}
					if resume == "stance" {
						u.Flags |= 1 << units.StandingFireShift
					} else {
						svc.Rules = StrictRules{}
					}
					if sum := svc.StepWeaponsForUnit(u, 4, w, nil, terrain, econ, nil, &random, nil); sum.Fired != 1 {
						t.Fatalf("retained target did not resume: %+v", sum)
					}
					if slot.Target != originalTarget {
						t.Fatal("resume replaced target")
					}
					if slot.Weapon.Stockpile {
						if slot.Ammo != 2 || slot.Reload != 0 || econ.Players[u.Owner] != beforePlayer {
							t.Fatal("stockpile resume did not spend exactly one round")
						}
					} else if slot.Ammo != 3 || slot.Reload == 0 || econ.Players[u.Owner].Stock[economy.Energy] != 93 || econ.Players[u.Owner].Stock[economy.Metal] != 97 {
						t.Fatal("ordinary resume did not apply normal reload/debit")
					}
				})
			}
		}
	}
}

// A slot an order holds fires through Hold Fire, and does so exactly as Strict
// 3.1 does: same tick, same reload, debit, ammunition, reveal and shared-stream
// state (the spawner case below covers a shot that draws). When the order ends the slot is the unit's own again, and a target it
// then takes on its own stays silent.
func TestModernHoldFireAdmitsOrderedSlotLikeStrict(t *testing.T) {
	type outcome struct {
		firedAt        []uint32
		reload, ammo   int32
		random         rng.Simulation
		player         economy.Player
		reveal, shots  uint32
		launchedEvents int
	}
	for _, targetKind := range []units.TargetKind{units.TargetUnit, units.TargetGround} {
		for _, weaponKind := range []string{"ordinary", "command", "stockpile"} {
			t.Run(fmt.Sprintf("%s/target%d", weaponKind, targetKind), func(t *testing.T) {
				var got [2]outcome
				for i, modern := range []bool{true, false} {
					w, terrain, u, slot, econ := holdFireFixture(t, weaponKind, targetKind, true)
					random := rng.NewSimulation(77)
					svc := &Service{Rules: rulesForModern(modern)}
					o := &got[i]
					svc.Events = func(Event) { o.launchedEvents++ }
					for tick := uint32(1); tick <= 4; tick++ {
						if sum := svc.StepWeaponsForUnit(u, tick, w, nil, terrain, econ, nil, &random, nil); sum.Fired != 0 {
							o.firedAt = append(o.firedAt, tick)
						}
					}
					o.reload, o.ammo, o.random, o.player, o.reveal, o.shots = slot.Reload, slot.Ammo, random, econ.Players[u.Owner], u.RevealDeadline, uint32(svc.Count())
					if !modern {
						continue
					}
					// The order-removal walk hands the slot back and clears its
					// target [04 R-ORDER-02 §2]; a target offered afterwards is
					// the unit's own and Hold Fire keeps it silent.
					target := slot.Target
					InhibitWeaponSlot(u, 0)
					if slot.Target.Kind != units.TargetNone || !slot.IsAutonomous() {
						t.Fatal("fixture did not hand the slot back")
					}
					slot.Target, slot.Reload = target, 0
					before, count := random, svc.Count()
					for tick := uint32(5); tick <= 8; tick++ {
						if sum := svc.StepWeaponsForUnit(u, tick, w, nil, terrain, econ, nil, &random, nil); sum.Fired != 0 || sum.Dispatched {
							t.Fatalf("ended order fell through into autonomous fire: %+v", sum)
						}
					}
					if random != before || svc.Count() != count || econ.Players[u.Owner] != o.player {
						t.Fatal("suppressed autonomous shot drew randomness, launched or spent resources")
					}
				}
				// Tick 2 is the visit whose decrement ends the countdown. A
				// stockpile weapon assigns no reload and spends a round a tick.
				if len(got[0].firedAt) == 0 || got[0].firedAt[0] != 2 {
					t.Fatalf("ordered slot did not fire when its reload countdown ended: %v", got[0].firedAt)
				}
				if !reflect.DeepEqual(got[0], got[1]) {
					t.Fatalf("ordered shot under Modern Hold Fire differs from Strict 3.1:\nmodern %+v\nstrict %+v", got[0], got[1])
				}
			})
		}
	}
}

func TestModernHoldFireSpawnerPrecedesQueriesAndShotRandomness(t *testing.T) {
	for _, tc := range []struct {
		name            string
		modern, ordered bool
		fires           bool
	}{{"modern own slot", true, false, false}, {"modern ordered slot", true, true, true}, {"strict own slot", false, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			launch := modernBallisticSlot()
			launch.Weapon.Accuracy = 1024
			launch.Flags = units.SlotFlagEnabled | units.SlotFlagAutonomous
			if tc.ordered {
				launch.Flags &^= units.SlotFlagAutonomous
			}
			muzzle, aim := modernTerrainPoints()
			shooter := &units.Unit{Handle: 1, Health: 100, MaxHealth: 100}
			random := rng.NewSimulation(77)
			before := random
			oldSlot := launch
			calls := 0
			spy := &FireSpy{}
			svc := &Service{Rules: rulesForModern(tc.modern)}
			ports := FirePorts{Shooter: shooter, Origin: muzzle, RNG: &random, ShooterHealth: 100, ShooterMaxHealth: 100, Spy: spy, MuzzlePiece: func(int) int32 { calls++; return -1 }}
			_, fired := TryFire(svc, &launch, 0, Target{Kind: TargetPoint, X: aim.X, Y: aim.Y, Z: aim.Z}, 10, ports)
			if !tc.fires {
				if fired || calls != 0 || len(spy.Events) != 0 || random != before || launch != oldSlot || shooter.RevealDeadline != 0 || svc.Count() != 0 {
					t.Fatal("held spawner performed shot work")
				}
			} else if !fired || calls != 1 || len(spy.Events) == 0 || random == before || shooter.RevealDeadline != 610 {
				t.Fatal("ordered or Strict attempt lost retail shot work")
			}
		})
	}
}

func TestModernHoldFireCancelsBurstRemainderOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		modern   bool
		deadline uint32
		ordered  bool
	}{{"modern due", true, 4, false}, {"modern before due", true, 100, false}, {"modern ordered due", true, 4, true}, {"strict due", false, 4, false}} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, shooter, _ := newTestWorldAndUnits(t)
			shooter.Flags &^= units.StandingFieldMask << units.StandingFireShift
			svc := &Service{Rules: rulesForModern(tc.modern)}
			weapon := &content.WeaponDef{ID: 11, LineOfSight: true, WeaponVelocity: 65536, Range: 32767, BurstRate: 3, SprayAngle: 500, RandomDecay: 100, SoundTrigger: true, SoundStart: "burst"}
			cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"burst": weapon}}
			cat.RebuildWeaponIndex()
			pos := Vec3{X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(50), Z: numeric.FixedFromInt(100)}
			svc.Reserve()
			svc.Records[0] = Projectile{Shooter: shooter.Handle, WeaponID: 11, BurstRemaining: 2, OrderedBurst: tc.ordered, BurstDeadline: tc.deadline, ExpiryTick: 1000, Speed: 65536, Pos: pos, StartPos: pos, Velocity: Vec3{X: 65536}}
			svc.Reserve()
			svc.Records[1] = Projectile{Shooter: shooter.Handle, WeaponID: 11, ExpiryTick: 1000, Speed: 65536, Pos: pos, StartPos: pos, Velocity: Vec3{X: 65536}}
			random := rng.NewSimulation(77)
			before := random
			events := 0
			svc.Events = func(Event) { events++ }
			svc.TickProjectiles(4, w, nil, nil, nil, nil, nil, cat, &random, nil)
			if tc.modern && !tc.ordered {
				if svc.Count() != 1 || svc.Records[0].BurstRemaining != 0 || svc.Records[0].Pos.X != pos.X+65536 || random != before || events != 0 {
					t.Fatal("Hold Fire did not silently cancel only parked remainder")
				}
				svc.Rules = StrictRules{}
				svc.TickProjectiles(5, w, nil, nil, nil, nil, nil, cat, &random, nil)
				if svc.Count() != 1 || svc.Records[0].Pos.X != pos.X+2*65536 || random != before {
					t.Fatal("cancelled burst replayed after mode change")
				}
			} else if svc.Count() != 3 || svc.Records[0].BurstRemaining != 1 || random == before || events != 1 {
				t.Fatal("Strict or ordered burst lost its retail clone behavior")
			}
		})
	}
}

// The spawner stamps a burst anchor with the provenance of the slot that
// launched it, which is what lets an ordered burst outlive its order.
func TestModernHoldFireBurstAnchorRecordsSlotProvenance(t *testing.T) {
	for _, ordered := range []bool{true, false} {
		launch := modernBallisticSlot()
		launch.Weapon.Burst, launch.Weapon.BurstRate = 3, 2
		launch.Flags = units.SlotFlagEnabled | units.SlotFlagAutonomous
		if ordered {
			launch.Flags &^= units.SlotFlagAutonomous
		}
		muzzle, aim := modernTerrainPoints()
		// Return Fire: both provenances launch, so both anchors exist.
		shooter := &units.Unit{Handle: 1, Health: 100, MaxHealth: 100, Flags: 1 << units.StandingFireShift}
		svc := &Service{Rules: &ModernRules{}}
		h, fired := TryFire(svc, &launch, 0, Target{Kind: TargetPoint, X: aim.X, Y: aim.Y, Z: aim.Z}, 10, FirePorts{Shooter: shooter, Origin: muzzle, ShooterHealth: 100, ShooterMaxHealth: 100})
		if !fired || svc.Records[int(h)-1].BurstRemaining == 0 || svc.Records[int(h)-1].OrderedBurst != ordered {
			t.Fatalf("ordered=%v: burst anchor lost its slot provenance: fired=%v %+v", ordered, fired, svc.Records[int(h)-1])
		}
	}
}

func TestModernHoldFireBurstUsesCurrentShooterReference(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reuse bool
		fire  uint32
	}{{"freed shooter", false, 0}, {"reused slot holds", true, 0}, {"reused slot fires", true, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, shooter, _ := newTestWorldAndUnits(t)
			handle, def := shooter.Handle, shooter.Def
			w.FreeImmediate(handle)
			if tc.reuse {
				h, err := w.Create(def, 0, 0, 0, 0)
				if err != nil || h != handle {
					t.Fatalf("fixture did not reuse shooter: %d %v", h, err)
				}
				current := w.Unit(h)
				current.Flags = (current.Flags &^ (units.StandingFieldMask << units.StandingFireShift)) | tc.fire<<units.StandingFireShift
			}
			weapon := &content.WeaponDef{ID: 11, LineOfSight: true, WeaponVelocity: 65536, Range: 32767, BurstRate: 3, WeaponTimer: 100}
			cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"burst": weapon}}
			cat.RebuildWeaponIndex()
			svc := &Service{Rules: &ModernRules{}}
			svc.Reserve()
			svc.Records[0] = Projectile{Shooter: handle, WeaponID: 11, BurstRemaining: 2, Speed: 65536, Pos: Vec3{Y: 65536}, Velocity: Vec3{X: 65536}}
			svc.TickProjectiles(4, w, nil, nil, nil, nil, nil, cat, nil, nil)
			if tc.reuse && tc.fire == 0 {
				if svc.Count() != 0 {
					t.Fatal("current held shooter did not cancel remainder")
				}
			} else if svc.Count() != 2 {
				t.Fatal("missing or current non-held shooter gained a new rejection")
			}
		})
	}
}

// A callback dispatched before the stance change can still deliver its ready
// result, but that result cannot bypass Modern's launch gate.
func TestModernHoldFireBlocksPreviouslyDispatchedAim(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	vm := cob.NewVM(progWithAim([]uint32{0x10021001, 1, 0x10065000}, "AimPrimary", 0))
	attachTestCOB(shooter, vm)
	shooter.InstallWeapon(0, weaponTurret(2))
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	svc := &Service{Rules: &ModernRules{}}
	sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, nil, nil, nil)
	if !sum.Dispatched || sum.Fired != 0 {
		t.Fatalf("expected pending aim: %+v", sum)
	}
	shooter.Flags &^= units.StandingFieldMask << units.StandingFireShift
	drainTestUnitCOB(t, shooter)
	if !slot.Aim.Ready {
		t.Fatal("pre-held aim did not deliver")
	}
	if sum = svc.StepWeaponsForUnit(shooter, 2, w, nil, terrain, nil, nil, nil, nil); sum.Fired != 0 || sum.Dispatched || svc.Count() != 0 || !slot.Aim.Ready {
		t.Fatalf("ready callback bypassed hold or was discarded: %+v", sum)
	}
	shooter.Flags |= 2 << units.StandingFireShift
	if sum = svc.StepWeaponsForUnit(shooter, 3, w, nil, terrain, nil, nil, nil, nil); sum.Fired != 1 {
		t.Fatalf("ready retained aim did not resume: %+v", sum)
	}
}
