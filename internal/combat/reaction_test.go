package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// reactionFixture builds a victim and an attacker owned by two controlled
// players, with the seams bound to recorders. Slot admission is admitted by
// default so a test can isolate the clause it is about.
type reactionFixture struct {
	svc      *Service
	w        *units.World
	victim   *units.Unit
	attacker *units.Unit
	notices  int
	stops    int
	throttle int
	orders   int
	observed int
	admits   func(*units.Unit, int, *units.Unit) bool
}

func newReactionFixture(t *testing.T) *reactionFixture {
	t.Helper()
	w := newCombatFixtureWorld(10, nil)
	def := &content.UnitDef{UnitName: "reactfixture", MaxDamage: 5000, Limit: -1, FootprintX: 1, FootprintZ: 1}
	at := func(owner uint8, x int64) *units.Unit {
		h, err := w.Create(def, owner, numeric.FixedFromInt(x), numeric.FixedFromInt(10), numeric.FixedFromInt(40))
		if err != nil {
			t.Fatalf("create unit: %v", err)
		}
		u := w.Unit(h)
		u.Health = 5000
		u.MaxHealth = 5000
		return u
	}
	f := &reactionFixture{w: w}
	f.victim = at(1, 40)
	f.attacker = at(2, 44)
	// Armed, fully built, fire at will and roam — the retaliation gate's four
	// victim-side terms [08 R-AI-01 §11][04 R-STANCE-01 §2].
	f.victim.Flags |= units.ArmedStatus
	f.victim.Flags |= 2<<units.StandingMoveShift | 2<<units.StandingFireShift
	f.victim.Remaining = 0
	f.admits = func(*units.Unit, int, *units.Unit) bool { return true }
	f.svc = &Service{ControlByte: func(uint8) uint8 { return ControlByteHuman }}
	f.svc.Reaction = &ReactionSeams{
		ObserverNotice:          func(*units.Unit) { f.observed++ },
		Allied:                  func(a, b uint8) bool { return a == b },
		ArmConstructionThrottle: func(uint8, uint32) { f.throttle++ },
		StopCurrentOrder:        func(*units.Unit, uint32) { f.stops++ },
		RetaliationOrder:        func(*units.Unit, *units.Unit) bool { f.orders++; return false },
		SlotAcquisitionAdmits: func(u *units.Unit, idx int, cand *units.Unit) bool {
			return f.admits(u, idx, cand)
		},
		UnderAttackSilenced: func(*units.Unit) bool { return false },
		UnderAttackNotice:   func(*units.Unit) { f.notices++ },
	}
	return f
}

// installSlotWeapon mirrors what unit spawn does to a slot whose weapon link
// resolves [04 R-UNIT-06 §5 part 3]: the weapon, the armed bit, and the control
// byte's AUTONOMY bit, which every per-slot reader — this offer, the autonomous
// scan, and the guard's leg 2 — requires.
func installSlotWeapon(u *units.Unit, idx int, weapon *content.WeaponDef) {
	s := u.SlotAt(idx)
	s.Weapon = weapon
	s.Flags |= 0x02
	s.Flags |= units.SlotFlagAutonomous
}

// TestReactionOffersTheAttackerToAnIdleSlot locks the per-slot offer of
// [06 R-WPN-04 §2 part 3]: an admissible weapon whose slot has no target is
// given the attacker, and a `commandfire` weapon never is — the same contract
// the autonomous scan carries at [06 §3.2], which is why a human commander's
// laser answers a hit and its disintegrator does not.
func TestReactionOffersTheAttackerToAnIdleSlot(t *testing.T) {
	f := newReactionFixture(t)
	installSlotWeapon(f.victim, 0, &content.WeaponDef{ID: 1, Range: 400})
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	slot := f.victim.SlotAt(0)
	if slot.Target.Kind != units.TargetUnit || slot.Target.Unit != f.attacker.Handle {
		t.Fatalf("the offer left slot 0 targeting %+v, want the attacker %d [06 R-WPN-04 §2]", slot.Target, f.attacker.Handle)
	}
	// The setter writes ONLY the target pair [04 R-UNIT-06 §5 part 3]: an
	// `armed` OR that stood in the offer had no retail counterpart, and the bit
	// it wrote is already set when the weapon link resolves.

	// The same slot with a command-fire weapon refuses.
	g := newReactionFixture(t)
	installSlotWeapon(g.victim, 0, &content.WeaponDef{ID: 1, Range: 400, CommandFire: true})
	g.svc.ReactToDamage(g.w, g.victim, g.attacker, 5)
	if got := g.victim.SlotAt(0).Target.Kind; got != units.TargetNone {
		t.Fatalf("a commandfire slot was offered the attacker (target kind %v); the clause is exact [06 R-WPN-04 §2]", got)
	}
}

// TestReactionOfferKeepsAnAdmissiblePresentTarget locks the offer's "unless"
// clause: the attacker is installed unless the slot's present target exists,
// passes the same gate, and is clear of the slot's bad-target set
// [06 R-WPN-04 §2 part 3].
func TestReactionOfferKeepsAnAdmissiblePresentTarget(t *testing.T) {
	f := newReactionFixture(t)
	installSlotWeapon(f.victim, 0, &content.WeaponDef{ID: 1, Range: 400})
	// A third unit already under the slot's guns.
	other := f.attacker
	f.victim.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: other.Handle}
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.victim.SlotAt(0).Target.Unit != other.Handle {
		t.Fatal("an admissible present target must be kept [06 R-WPN-04 §2]")
	}

	// A present target that fails the gate is replaced.
	g := newReactionFixture(t)
	installSlotWeapon(g.victim, 0, &content.WeaponDef{ID: 1, Range: 400})
	stale, err := g.w.Create(g.victim.Def, 3, numeric.FixedFromInt(90), numeric.FixedFromInt(10), numeric.FixedFromInt(40))
	if err != nil {
		t.Fatal(err)
	}
	g.victim.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: stale}
	g.admits = func(_ *units.Unit, _ int, cand *units.Unit) bool { return cand.Handle != stale }
	g.svc.ReactToDamage(g.w, g.victim, g.attacker, 5)
	if g.victim.SlotAt(0).Target.Unit != g.attacker.Handle {
		t.Fatalf("a present target that fails the gate must be replaced, got %d [06 R-WPN-04 §2]", g.victim.SlotAt(0).Target.Unit)
	}
}

// TestReactionRetaliationOuterGates locks the outer admission of
// [08 R-AI-01 §11]: fully built, armed or `kamikaze`, an owner of control byte
// 1 or 2, and an unallied attacker. Each is independently sufficient to refuse.
func TestReactionRetaliationOuterGates(t *testing.T) {
	weapon := &content.WeaponDef{ID: 1, Range: 400}
	cases := []struct {
		name   string
		break_ func(*reactionFixture)
	}{
		{"unbuilt", func(f *reactionFixture) { f.victim.Remaining = 0.5 }},
		{"unarmed", func(f *reactionFixture) { f.victim.Flags &^= units.ArmedStatus }},
		{"allied", func(f *reactionFixture) {
			f.svc.Reaction.Allied = func(uint8, uint8) bool { return true }
		}},
		{"absent controller", func(f *reactionFixture) {
			f.svc.ControlByte = func(uint8) uint8 { return ControlByteAbsent }
		}},
		{"hold fire", func(f *reactionFixture) {
			f.victim.Flags &^= units.StandingFieldMask << units.StandingFireShift
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newReactionFixture(t)
			installSlotWeapon(f.victim, 0, weapon)
			tc.break_(f)
			f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
			if f.victim.SlotAt(0).Target.Kind != units.TargetNone {
				t.Fatalf("%s still retaliated [08 R-AI-01 §11]", tc.name)
			}
		})
	}
	// `kamikaze` substitutes for the armed flag.
	f := newReactionFixture(t)
	installSlotWeapon(f.victim, 0, weapon)
	f.victim.Flags &^= units.ArmedStatus
	f.victim.Def.Kamikaze = true
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.victim.SlotAt(0).Target.Kind != units.TargetUnit {
		t.Fatal("a kamikaze victim must retaliate even with the armed flag clear [08 R-AI-01 §11]")
	}
}

// TestReactionThrottleIsCanCaptureAndControllerTwo locks the construction
// throttle's two gates and its stop [08 R-AI-01 §11].
func TestReactionThrottleIsCanCaptureAndControllerTwo(t *testing.T) {
	f := newReactionFixture(t)
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.throttle != 0 || f.stops != 0 {
		t.Fatal("a definition without `cancapture` must not arm the throttle [08 R-AI-01 §11]")
	}

	f = newReactionFixture(t)
	f.victim.Def.CanCapture = true
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.throttle != 0 {
		t.Fatal("a human-owned builder must not arm the throttle: the control byte must be 2 [08 R-AI-01 §11]")
	}

	f = newReactionFixture(t)
	f.victim.Def.CanCapture = true
	f.svc.ControlByte = func(uint8) uint8 { return ControlByteComputer }
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.throttle != 1 || f.stops != 1 {
		t.Fatalf("computer builder throttle=%d stops=%d, want 1/1 [08 R-AI-01 §11]", f.throttle, f.stops)
	}
}

// TestReactionUnderAttackNoticeGates locks part 4 [06 R-WPN-04 §2]: the notice
// is requested when bit 7 of the front primary record's gate mask is clear AND
// either the STORED attacker-side snapshot differs from the victim's owner byte
// or the STORED last damage kind is 1. The two values are the previous packet's,
// because the routine runs before the fields are rewritten.
func TestReactionUnderAttackNoticeGates(t *testing.T) {
	f := newReactionFixture(t)
	f.victim.LastDamageSide = 10 // the spawn seed's neutral value
	f.victim.LastDamageCause = 0
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.notices != 1 {
		t.Fatalf("notices=%d, want one: a fresh victim's snapshot differs from its owner [06 R-WPN-04 §2]", f.notices)
	}

	// Bit 7 set on the front primary record silences it.
	f = newReactionFixture(t)
	f.svc.Reaction.UnderAttackSilenced = func(*units.Unit) bool { return true }
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.notices != 0 {
		t.Fatalf("notices=%d, want zero while bit 7 is set [06 R-WPN-04 §2]", f.notices)
	}

	// An own-side non-weapon packet leaves side == owner and a cause that is
	// not 1, and the hit that follows it is silent once.
	f = newReactionFixture(t)
	f.victim.LastDamageSide = f.victim.Owner
	f.victim.LastDamageCause = uint8(CauseReclaim)
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.notices != 0 {
		t.Fatalf("notices=%d, want zero after an own-side non-weapon packet [06 R-WPN-04 §2]", f.notices)
	}
	// Cause 1 from the same side still announces.
	f.victim.LastDamageCause = uint8(CauseOrdinary)
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if f.notices != 1 {
		t.Fatalf("notices=%d, want one: a stored kind of 1 announces regardless of side [06 R-WPN-04 §2]", f.notices)
	}
}

// TestReactionRunsBeforeTheProvenanceStamp locks the ordering of [06 §9.1] step
// 4 through the real damage path: the flash, then the reaction, then the kind
// byte and the attacker fields. Part 4 must observe the PREVIOUS packet's
// values, so a second hit from the same side with a stored cause of 1 is what
// makes the notice fire, not the packet being applied.
func TestReactionRunsBeforeTheProvenanceStamp(t *testing.T) {
	f := newReactionFixture(t)
	terrain := &world.Terrain{CellW: 100, CellH: 100, Plot: make([]world.PlotCell, 100*100)}
	// The sweep's candidates are the plot cells' occupancy words [06 §9.3], and
	// a combat fixture has no movement system to fill them.
	stampGroundOccupancy(t, terrain, f.victim)
	var flashes int
	f.svc.Events = func(ev Event) {
		if ev.Kind == EventDamageFlash {
			flashes++
		}
	}
	var seenSide uint8
	var seenCause uint8
	f.svc.Reaction.UnderAttackNotice = func(u *units.Unit) {
		seenSide, seenCause = u.LastDamageSide, u.LastDamageCause
		f.notices++
	}
	f.victim.LastDamageSide = 7
	f.victim.LastDamageCause = 3
	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 200, DamageDefault: 10, EdgeEffectiveness: 0}
	f.svc.ExplodeWeaponAt(f.w, terrain, weapon, Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, f.attacker.Handle, 5)

	if flashes == 0 {
		t.Fatal("no damage-flash event was emitted [06 R-WPN-04 §2]")
	}
	if seenSide != 7 || seenCause != 3 {
		t.Fatalf("the reaction saw side %d cause %d, want the PREVIOUS packet's 7/3 [06 R-WPN-04 §2]", seenSide, seenCause)
	}
	if f.victim.LastDamageSide != f.attacker.Owner || Cause(f.victim.LastDamageCause) != CauseOrdinary {
		t.Fatalf("after the packet the stamp is side %d cause %d, want %d/1 [06 §9.1]", f.victim.LastDamageSide, f.victim.LastDamageCause, f.attacker.Owner)
	}
}

// TestNoDamageRunsNoReaction is the draw-count control (I4): a scenario in
// which nothing is damaged runs no part of the routine, so it consumes no
// draws and emits no events.
func TestNoDamageRunsNoReaction(t *testing.T) {
	f := newReactionFixture(t)
	terrain := &world.Terrain{CellW: 100, CellH: 100, Plot: make([]world.PlotCell, 100*100)}
	var events int
	f.svc.Events = func(Event) { events++ }
	// A blast with no area reaches no victim [06 §9.3].
	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 0, DamageDefault: 10}
	f.svc.ExplodeWeaponAt(f.w, terrain, weapon, Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, f.attacker.Handle, 5)
	if events != 0 || f.observed != 0 || f.throttle != 0 || f.notices != 0 || f.orders != 0 {
		t.Fatalf("an undamaged scenario ran the reaction: events=%d observed=%d throttle=%d notices=%d orders=%d",
			events, f.observed, f.throttle, f.notices, f.orders)
	}
}

// TestDamageRecordsTheAttackerLink locks the recorded-attacker writer of
// [04 R-UNIT-06 §5 part 1]: every damage application with a nonzero attacker id
// stores that id on the victim, beside the side snapshot, and nothing clears it
// afterwards — not a later tick, and not the attacker's own death. It is the
// producer the guard's legs 1 and 2 read [04 R-UNIT-06 §1].
func TestDamageRecordsTheAttackerLink(t *testing.T) {
	f := newReactionFixture(t)
	weapon := &content.WeaponDef{ID: 1, Range: 400, DamageDefault: 10}
	p := &Projectile{Pos: Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, Shooter: f.attacker.Handle}

	applyDamageToUnit(f.svc, f.victim, p, weapon, 1, 0, f.w, 5)
	if f.victim.EngagementTarget != f.attacker.Handle {
		t.Fatalf("the victim's recorded attacker = %d, want the shooter %d [04 R-UNIT-06 §5]", f.victim.EngagementTarget, f.attacker.Handle)
	}
	if f.victim.LastDamageSide != f.attacker.Owner {
		t.Fatalf("the side snapshot must be written with it, got %d", f.victim.LastDamageSide)
	}

	// No per-tick clear, and no clear when the attacker dies: the link is stale
	// by design and the READER carries the liveness test [04 R-UNIT-06 §5].
	f.w.Destroy(f.attacker.Handle, units.DeathKilled)
	if f.victim.EngagementTarget != f.attacker.Handle {
		t.Fatalf("the attacker's death must not clear the link, got %d", f.victim.EngagementTarget)
	}

	// A packet with no attacker leaves the link alone rather than clearing it.
	before := f.victim.EngagementTarget
	applyDamageToUnit(f.svc, f.victim, &Projectile{Pos: p.Pos}, weapon, 1, 0, f.w, 6)
	if f.victim.EngagementTarget != before {
		t.Fatalf("a null-shooter packet rewrote the link to %d [04 R-UNIT-06 §5]", f.victim.EngagementTarget)
	}
}

// TestSlotAutonomyBitPreconditions locks [04 R-UNIT-06 §5 part 3]: the two slot
// verbs are the only writers of the control byte's bit 4, each with its own
// precondition, and the offer refuses a slot an order currently holds.
func TestSlotAutonomyBitPreconditions(t *testing.T) {
	f := newReactionFixture(t)
	installSlotWeapon(f.victim, 0, &content.WeaponDef{ID: 1, Range: 400})
	slot := f.victim.SlotAt(0)

	// Spawn leaves the slot autonomous, so the offer takes it.
	if slot.Flags&units.SlotFlagAutonomous == 0 {
		t.Fatal("a slot whose weapon link resolved must be autonomous [04 R-UNIT-06 §5]")
	}
	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)
	if slot.Target.Unit != f.attacker.Handle {
		t.Fatalf("an autonomous slot must take the offer, got %+v", slot.Target)
	}

	// With the bit clear — an attack order holding the slot — the offer skips
	// it, whatever its present target.
	g := newReactionFixture(t)
	installSlotWeapon(g.victim, 0, &content.WeaponDef{ID: 1, Range: 400})
	held := g.victim.SlotAt(0)
	held.Flags &^= units.SlotFlagAutonomous
	held.Target = units.Target{}
	g.svc.ReactToDamage(g.w, g.victim, g.attacker, 5)
	if held.Target.Kind != units.TargetNone {
		t.Fatalf("a slot held by an order must be skipped, got %+v", held.Target)
	}
}

// TestDeathWritesTheDeathPacketsAttacker locks the death row of
// [04 R-UNIT-06 §5 part 1]'s writer table for the recorded-attacker link: on a
// unit's death the death handler writes the death packet's attacker, and it
// writes it ALWAYS — the row carries no condition, unlike the damage
// dispatcher's row above it, which writes only for a non-heal packet with a
// nonzero attacker id.
//
// "Always" is the half worth a test, because it is the only thing in retail
// that can erase a link: §5 states there is no per-tick clear and no clear on
// the attacker's death. A unit that was shot and then dies to something with no
// attacker behind it must NOT keep the earlier shooter.
func TestDeathWritesTheDeathPacketsAttacker(t *testing.T) {
	weapon := &content.WeaponDef{ID: 1, Range: 400, DamageDefault: 10}

	// 1. Killed by damage: the link is the killing packet's shooter.
	t.Run("KilledByDamage", func(t *testing.T) {
		f := newReactionFixture(t)
		p := &Projectile{Pos: Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, Shooter: f.attacker.Handle}
		lethal := &content.WeaponDef{ID: 1, Range: 400, DamageDefault: 9000}
		applyDamageToUnit(f.svc, f.victim, p, lethal, 1, 0, f.w, 5)
		if !f.victim.Dying {
			t.Fatalf("the victim did not latch death: health %d", f.victim.Health)
		}
		if f.victim.EngagementTarget != f.attacker.Handle {
			t.Fatalf("recorded attacker = %d, want the killing shooter %d [04 R-UNIT-06 §5]",
				f.victim.EngagementTarget, f.attacker.Handle)
		}
	})

	// 2. Shot first, then killed by a packet with no attacker: the death row
	// overwrites the earlier link with the packet's null. The dispatcher's own
	// row cannot express this — it skips a null attacker id entirely.
	t.Run("KilledWithNoAttackerClearsTheEarlierLink", func(t *testing.T) {
		f := newReactionFixture(t)
		hit := &Projectile{Pos: Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, Shooter: f.attacker.Handle}
		applyDamageToUnit(f.svc, f.victim, hit, weapon, 1, 0, f.w, 5)
		if f.victim.EngagementTarget != f.attacker.Handle {
			t.Fatalf("setup: the first hit did not record the attacker, got %d", f.victim.EngagementTarget)
		}
		lethal := &content.WeaponDef{ID: 1, Range: 400, DamageDefault: 9000}
		applyDamageToUnit(f.svc, f.victim, &Projectile{Pos: hit.Pos}, lethal, 1, 0, f.w, 6)
		if !f.victim.Dying {
			t.Fatalf("the victim did not latch death: health %d", f.victim.Health)
		}
		if f.victim.EngagementTarget != 0 {
			t.Fatalf("recorded attacker = %d after a shooterless death, want null: the death row writes the packet's attacker unconditionally [04 R-UNIT-06 §5]",
				f.victim.EngagementTarget)
		}
	})

	// 3. Self-destructed: cause 3 applies 30000 "to the unit itself" through
	// the ordinary funnel [04 R-SPEC-01 §1], so the death packet's attacker is
	// the unit, and the link ends as its own handle rather than as whoever had
	// shot it earlier.
	t.Run("SelfDestructed", func(t *testing.T) {
		f := newReactionFixture(t)
		hit := &Projectile{Pos: Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, Shooter: f.attacker.Handle}
		applyDamageToUnit(f.svc, f.victim, hit, weapon, 1, 0, f.w, 5)
		if f.victim.EngagementTarget != f.attacker.Handle {
			t.Fatalf("setup: the first hit did not record the attacker, got %d", f.victim.EngagementTarget)
		}
		if !f.svc.ApplySelfDestructDamage(f.w, f.victim.Handle, 6) {
			t.Fatal("self destruct did not apply")
		}
		if !f.victim.Dying || f.victim.DeathCause != units.DeathSelfDestruct {
			t.Fatalf("self destruct did not latch cause 3: dying=%v cause=%v", f.victim.Dying, f.victim.DeathCause)
		}
		if f.victim.EngagementTarget != f.victim.Handle {
			t.Fatalf("recorded attacker = %d after self destruct, want the unit itself %d [04 R-UNIT-06 §5]",
				f.victim.EngagementTarget, f.victim.Handle)
		}
	})

	// 4. A death that carries no packet at all — a reclaimed unit, a cancelled
	// factory product — is the plain Destroy arm and writes the null.
	t.Run("PacketlessDeath", func(t *testing.T) {
		f := newReactionFixture(t)
		hit := &Projectile{Pos: Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, Shooter: f.attacker.Handle}
		applyDamageToUnit(f.svc, f.victim, hit, weapon, 1, 0, f.w, 5)
		f.w.Destroy(f.victim.Handle, units.DeathReclaimed)
		if f.victim.EngagementTarget != 0 {
			t.Fatalf("recorded attacker = %d after a packetless death, want null [04 R-UNIT-06 §5]", f.victim.EngagementTarget)
		}
	})

	// 5. The already-marked guard still short-circuits: a second Destroy on a
	// dying unit writes nothing, so a death cannot be re-attributed.
	t.Run("AlreadyMarkedIsNotRewritten", func(t *testing.T) {
		f := newReactionFixture(t)
		f.w.DestroyBy(f.victim.Handle, units.DeathKilled, f.attacker.Handle)
		f.w.Destroy(f.victim.Handle, units.DeathReclaimed)
		if f.victim.EngagementTarget != f.attacker.Handle {
			t.Fatalf("a second death mark rewrote the link to %d", f.victim.EngagementTarget)
		}
		if f.victim.DeathCause != units.DeathKilled {
			t.Fatalf("a second death mark rewrote the cause to %v", f.victim.DeathCause)
		}
	})
}
