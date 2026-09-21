package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The kamikaze acquisition fixtures below run through the REAL production
// binding: session composition's WeaponAdapter.Acquire, which reaches
// combat.Service.AcquireWeaponTarget. Nothing here stubs Acquire, because the
// contract under test is precisely that the sight-distance caller reaches the
// shared unit-level target search for a shooter whose slot 0 holds no active
// weapon [06 §3.2][04 R-SPEC-01 §1].

// kamikazeShooterDef is a weaponless `kamikaze` definition of the stock mine
// shape: building class (`bmcode 0`, which is the status-word bit
// `Standby_Mine`'s phase 0 requires), fire at will, and an empty `weapon1`
// that leaves slot 0 with no active weapon [02 §5 R-CONTENT-02].
func kamikazeShooterDef(name string, mobile bool) *content.UnitDef {
	def := &content.UnitDef{
		UnitName:           name,
		MaxDamage:          100,
		SightDistance:      64,
		FootprintX:         1,
		FootprintZ:         1,
		BuildTime:          100,
		WorkerTime:         30,
		Kamikaze:           true,
		StandingFireOrder:  2,
		DefaultMissionType: "Standby_Mine",
	}
	if mobile {
		// The stock crawling-bomb shape: a mover that authors `canattack`
		// (which the code-3 resolver's gate requires) and `Standby` as its
		// default mission. Stock `armvader` and `corroach` additionally author
		// hold fire, so they engage only once a player sets fire at will; the
		// fixture sets fire at will because that is the contract under test
		// [04 R-ORD-02 §1][04 R-STANCE-01 §3].
		def.BMCode = 1
		def.MovementClass = "testmove"
		def.DefaultMissionType = "Standby"
		def.StandingMoveOrder = 1 // maneuver
		def.CanAttack = true
		def.CanMove = true
		def.MaxVelocity = 2 << 16
		def.Acceleration = 1 << 14
		def.BrakeRate = 1 << 14
	}
	def.CanonicalKey = content.CanonicalKey(name)
	def.Script = fixtureCOBProgram()
	return def
}

// kamikazeTargetDef is the grounded victim: it authors `shootme`, which check 2
// of the picked-candidate order requires of a human player's candidate
// [06 §3.2][04 R-SPEC-01 §5]. Stock mines are human-owned in these fixtures.
func kamikazeTargetDef(s *Session, name string) *content.UnitDef {
	def := *s.Catalog.Units["armcom"]
	def.UnitName = name
	def.CanonicalKey = content.CanonicalKey(name)
	def.ShootMe = true
	def.MaxDamage = 5000
	def.Script = fixtureCOBProgram()
	return &def
}

// targetRegistryFirstDueTick is the first tick the 30-tick registry cadence
// admits from a zero start [06 §3.1].
const targetRegistryFirstDueTick uint32 = 30

type kamikazeFixture struct {
	session *Session
	shooter *units.Unit
	target  *units.Unit
}

// newKamikazeFixture places a weaponless kamikaze shooter and one hostile
// candidate within the shooter's sight distance, completes both, and binds the
// shooter's default mission. Ticking is the caller's.
func newKamikazeFixture(t *testing.T, mode gameplay.Mode, shooterDef *content.UnitDef, shape func(*content.UnitDef), gap int64) *kamikazeFixture {
	t.Helper()
	s := strictNewSessionWithUnits(t, 0, 73, 23)
	s.SetGameplay(mode)
	s.Vis.SetMode(0)

	targetDef := kamikazeTargetDef(s, "testvictim")
	if shape != nil {
		shape(targetDef)
	}
	shooterX, shooterZ := numeric.FixedFromInt(200), numeric.FixedFromInt(200)
	shooter := createKamikazeUnit(t, s, shooterDef, 0, shooterX, shooterZ)
	targetX := numeric.FixedFromInt(200 + gap)
	target := createKamikazeUnit(t, s, targetDef, 1, targetX, shooterZ)

	// The committed mover-mode mirror is what `Standby_Mine`'s phase 1 reads:
	// grounded is 1 [04 R-ORD-01 §12]. Publish it directly so the fixture does
	// not depend on a movement integration that is not under test.
	target.Move.ModeMirror = 1
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	// Acquisition draws from the per-side registry, which the strategic refresh
	// rebuilds only once every 30 ticks [06 §3.1]. Seed it on the first due tick
	// so a fixture can ask the production binding before ticking; the rebuild
	// takes no random draw.
	for slot := uint8(0); slot < 2; slot++ {
		if !s.Combat.RebuildTargetRegistryIfDue(targetRegistryFirstDueTick, slot, s.Units, s.Vis, s.World, s.Econ) {
			t.Fatalf("target registry for slot %d did not rebuild", slot)
		}
	}
	return &kamikazeFixture{session: s, shooter: shooter, target: target}
}

// acquireThroughProductionBinding asks the shooter's own order-queue binding —
// the WeaponAdapter session composition installs — for a sight-distance
// acquisition on slot 0, which is the single call the opportunity scan makes
// [04 R-STANCE-01 §3].
func acquireThroughProductionBinding(t *testing.T, u *units.Unit) (pool.Handle, bool) {
	t.Helper()
	q := orders.QueueOfUnit(u)
	if q == nil || q.Binding() == nil || q.Binding().Weapons == nil || q.Binding().Weapons.Acquire == nil {
		t.Fatal("fixture lost the production weapon binding")
	}
	return q.Binding().Weapons.Acquire(u, 0, uint32(u.Def.SightDistance))
}

func createKamikazeUnit(t *testing.T, s *Session, def *content.UnitDef, owner uint8, x, z numeric.Fixed) *units.Unit {
	t.Helper()
	y := s.World.HeightAt(x, z)
	if y == numeric.Fixed(-1) {
		y = 0
	}
	h, err := s.Units.Create(def, owner, x, y, z)
	if err != nil {
		t.Fatalf("create %s: %v", def.UnitName, err)
	}
	s.CompleteUnit(h)
	u := s.Units.Unit(h)
	s.bindOrderQueue(u)
	if name := def.DefaultMissionType; name != "" {
		orders.QueueOfUnit(u).Push(orders.Lookup(name), orders.Node{Owner: h, Flags: orders.FlagAutoOp})
	}
	return u
}

// runKamikazeTicks drives whole authoritative ticks so the per-side target
// registry rebuild, the order pump and the damage intake all run in their
// retail order. It stops as soon as the shooter has taken its self-destruct.
func runKamikazeTicks(s *Session, shooter *units.Unit, handle pool.Handle, ticks int) bool {
	return runKamikazeTicksFrom(s, shooter, handle, 1, uint32(ticks))
}

func runKamikazeTicksFrom(s *Session, shooter *units.Unit, handle pool.Handle, first, last uint32) bool {
	for tick := first; tick <= last; tick++ {
		s.Combat.RebuildTargetRegistryIfDue(tick, 0, s.Units, s.Vis, s.World, s.Econ)
		s.Combat.RebuildTargetRegistryIfDue(tick, 1, s.Units, s.Vis, s.World, s.Econ)
		s.phaseUnits(tick)
		if selfDestructed(s, shooter, handle) {
			return true
		}
	}
	return false
}

// selfDestructed reports the mine's own death by cause 3 — the assertion the
// contract names, because the spawned `SelfDestruct` record can be consumed in
// the same tick it is inserted [04 R-ORD-01 §2].
func selfDestructed(s *Session, shooter *units.Unit, handle pool.Handle) bool {
	if shooter == nil {
		return false
	}
	if s.Units.Unit(handle) == nil {
		return shooter.LastDamageCause == 3
	}
	return shooter.LastDamageCause == 3 && (!shooter.Alive || shooter.Dying || shooter.Health <= 0)
}

// T1 — the positive leg under both rule sets. A finished weaponless kamikaze
// mine on `Standby_Mine` at fire at will, with a visible grounded `shootme`
// hostile inside its sight distance, dies by immediate self-destruct.
func TestWeaponlessKamikazeMineDetonatesOnProximity(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			f := newKamikazeFixture(t, mode, kamikazeShooterDef("testmine", false), nil, 16)
			if f.shooter.SlotAt(0).Weapon != nil {
				t.Fatal("fixture mine must hold no active weapon in slot 0")
			}
			if got, ok := acquireThroughProductionBinding(t, f.shooter); !ok || got != f.target.Handle {
				t.Fatalf("sight-distance acquisition on a weaponless kamikaze slot returned (%v, %v) [06 §3.2][04 R-SPEC-01 §1]", got, ok)
			}
			if !runKamikazeTicks(f.session, f.shooter, f.shooter.Handle, 240) {
				t.Fatal("mine never detonated on a visible grounded hostile inside sight distance [04 R-ORD-01 §2][06 §3.2]")
			}
		})
	}
}

// T1 negative legs. Each removes exactly one precondition and must leave the
// mine alive: an allied candidate never enters the scanning player's hostile
// list [06 §3.1]; an airborne candidate fails `Standby_Mine`'s grounded
// post-check, which reads the committed mover-mode mirror [04 R-ORD-01 §3];
// and a hold-fire mine never searches at all [04 R-STANCE-01 §3].
func TestWeaponlessKamikazeMineHoldsFireWithoutItsPreconditions(t *testing.T) {
	t.Run("allied target", func(t *testing.T) {
		f := newKamikazeFixture(t, gameplay.Strict31, kamikazeShooterDef("testmine", false), nil, 16)
		f.session.Econ.Players[0].Allies[1] = true
		// The alliance declaration only reaches acquisition through a registry
		// rebuild [06 §3.1], so take the next due one before asking.
		f.session.Combat.RebuildTargetRegistryIfDue(2*targetRegistryFirstDueTick, 0, f.session.Units, f.session.Vis, f.session.World, f.session.Econ)
		if _, ok := acquireThroughProductionBinding(t, f.shooter); ok {
			t.Fatal("an allied candidate was acquired [06 §3.1]")
		}
		if runKamikazeTicks(f.session, f.shooter, f.shooter.Handle, 240) {
			t.Fatal("mine detonated on an ally")
		}
	})
	t.Run("airborne target", func(t *testing.T) {
		f := newKamikazeFixture(t, gameplay.Strict31, kamikazeShooterDef("testmine", false), func(d *content.UnitDef) {
			// A mover exists only for `bmcode 1` [04 R-FAC-02 §5]; `canfly`
			// is what lets that mover hold the airborne mode.
			d.CanFly = true
			d.CruiseAlt = 100
		}, 16)
		if !f.session.Movement.SetMoverMode(f.target, 2) {
			t.Fatal("fixture could not enter airborne mode")
		}
		// The setter writes the REQUEST; the ordinary commit publishes the
		// mirror the mine reads [04 R-COLL-01 §1]. One tick of the mine's own
		// phase 0 is enough, and it cannot detonate on it.
		f.session.phaseUnits(1)
		if got := f.target.Move.ModeMirror & 3; got != 2 {
			t.Fatalf("fixture target is not airborne (committed mover mode %d)", got)
		}
		if runKamikazeTicksFrom(f.session, f.shooter, f.shooter.Handle, 2, 240) {
			t.Fatal("mine detonated on an airborne target [04 R-ORD-01 §3]")
		}
	})
	t.Run("hold fire", func(t *testing.T) {
		def := kamikazeShooterDef("testmine", false)
		def.StandingFireOrder = 0
		f := newKamikazeFixture(t, gameplay.Strict31, def, nil, 16)
		if runKamikazeTicks(f.session, f.shooter, f.shooter.Handle, 240) {
			t.Fatal("a hold-fire mine detonated [04 R-STANCE-01 §3]")
		}
	})
}

// T2 — the mobile leg. A weaponless kamikaze mover at `Standby` with maneuver
// and fire at will takes an `Attack_Kamikaze` record from the opportunity scan
// and the auto-engage issuer; the sight-distance caller's `nochasecategory`
// mask still refuses a matching candidate, which is check 4 [06 §3.2] and the
// behaviour `corroach`'s authored VTOL mask produces.
func TestWeaponlessKamikazeMoverEngagesUnlessNoChaseRefuses(t *testing.T) {
	const victimDefinitionID = 7
	for _, tc := range []struct {
		name    string
		noChase bool
	}{{"engages", false}, {"no-chase refuses", true}} {
		t.Run(tc.name, func(t *testing.T) {
			def := kamikazeShooterDef("testbomb", true)
			if tc.noChase {
				def.NoChaseCategoryMask = content.MaskForID(victimDefinitionID)
			}
			f := newKamikazeFixture(t, gameplay.Strict31, def, func(d *content.UnitDef) {
				d.UnitMask = content.MaskForID(victimDefinitionID)
			}, 16)
			_, ok := acquireThroughProductionBinding(t, f.shooter)
			if ok == tc.noChase {
				t.Fatalf("acquisition=%v with noChase=%v [06 §3.2] check 4", ok, tc.noChase)
			}
			engaged := false
			for tick := uint32(1); tick <= 120 && !engaged; tick++ {
				f.session.Combat.RebuildTargetRegistryIfDue(tick, 0, f.session.Units, f.session.Vis, f.session.World, f.session.Econ)
				f.session.phaseUnits(tick)
				engaged = queueHoldsKamikazeAttack(f.shooter)
			}
			if engaged == tc.noChase {
				t.Fatalf("Attack_Kamikaze issued=%v with noChase=%v [04 R-STANCE-01 §3]", engaged, tc.noChase)
			}
		})
	}
}

func queueHoldsKamikazeAttack(u *units.Unit) bool {
	q := orders.QueueOfUnit(u)
	if q == nil {
		return false
	}
	for _, n := range q.Primary() {
		if n != nil && orders.DescriptorFor(n.ID).Name == "Attack_Kamikaze" {
			return true
		}
	}
	return false
}

// T3 — the bypass is the shooter's `kamikaze` flag and nothing else. A
// weaponless unit that does not author it — a construction unit, say — still
// gets no target from the sight-distance caller [06 §3.2] check 3.
func TestWeaponlessNonKamikazeAcquiresNothing(t *testing.T) {
	def := kamikazeShooterDef("testbuilder", true)
	def.Kamikaze = false
	def.Builder = true
	f := newKamikazeFixture(t, gameplay.Strict31, def, nil, 16)
	if f.shooter.SlotAt(0).Weapon != nil {
		t.Fatal("fixture builder must hold no active weapon in slot 0")
	}
	if got, ok := acquireThroughProductionBinding(t, f.shooter); ok {
		t.Fatalf("a weaponless non-kamikaze unit acquired %v [06 §3.2] check 3", got)
	}
}
