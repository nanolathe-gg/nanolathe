package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"testing"
)

func modernCombatFixture(t *testing.T) (*Service, *units.World, *world.Terrain, *units.Unit, *units.Unit, *content.WeaponDef) {
	t.Helper()
	w, terrain := newContactFixture(t)
	def := &content.UnitDef{UnitName: "fixture", MaxDamage: 100, Limit: -1, StandingFireOrder: 2, ModelTopFixed: 16 << 16, DamageModifier: 65536}
	makeUnit := func(owner uint8, x int32) *units.Unit {
		h, err := w.Create(def, owner, cellCentre(x), numeric.FixedFromInt(10), cellCentre(1))
		if err != nil {
			t.Fatal(err)
		}
		return w.Unit(h)
	}
	shooter, target := makeUnit(0, 1), makeUnit(1, 5)
	terrain.PlotAt(5, 1).SetOccupantA(int16(target.Handle))
	weapon := &content.WeaponDef{ID: 77, LineOfSight: true, Turret: true, Range: 1000, WeaponVelocity: 16 << 16, AreaOfEffect: 8, DamageDefault: 60, ReloadTime: 30, Accuracy: 32, Tolerance: 65535, PitchTolerance: 65535, EnergyPerShot: 7, MetalPerShot: 3}
	shooter.InstallWeapon(0, weapon)
	shooter.Flags |= units.ArmedStatus
	s := &Service{Rules: &ModernRules{}, Visibility: func(visibility.PlayerID, visibility.Target) bool { return true }, Reaction: &ReactionSeams{Allied: func(a, b uint8) bool { return a == b }}}
	return s, w, terrain, shooter, target, weapon
}

func modernLaunch(t *testing.T, s *Service, w *units.World, terrain *world.Terrain, shooter, target *units.Unit, weapon *content.WeaponDef, random *rng.Simulation) (pool.Handle, bool) {
	t.Helper()
	s.rules().CombatTick(s, 10, false)
	slot := Slot{Weapon: weapon, Target: Target{Kind: TargetUnit, Unit: target.Handle}}
	q := ShotQuery{Service: s, World: w, Shooter: shooter, Target: target, Terrain: terrain, Tick: 10}
	aim := Vec3{X: target.X, Y: target.Y, Z: target.Z}
	return TryFire(s, &slot, 0, slot.Target, 10, FirePorts{Shooter: shooter, ShooterSide: shooter.Owner, Origin: Vec3{X: shooter.X, Y: shooter.Y, Z: shooter.Z}, TargetWorld: func(pool.Handle) (Vec3, bool) { return aim, true }, Shot: &q, RNG: random, ShooterHealth: shooter.Health, ShooterMaxHealth: shooter.MaxHealth})
}

func TestModernIncomingFinishingShotAndStrictBypass(t *testing.T) {
	for _, modern := range []bool{true, false} {
		s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
		if !modern {
			s.Rules = StrictRules{}
		}
		random := rng.NewSimulation(77)
		for shot := 0; shot < 3; shot++ {
			before := random
			_, fired := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
			want := !modern || shot < 2
			if fired != want {
				t.Fatalf("modern=%v shot=%d fired=%v", modern, shot, fired)
			}
			if fired == (random == before) {
				t.Fatalf("fired=%v must spend spread iff launched", fired)
			}
		}
	}
	s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
	weapon.DamageDefault = 300
	random := rng.NewSimulation(77)
	if _, fired := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); !fired {
		t.Fatal("indivisible finishing shot refused")
	}
	if _, fired := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); fired {
		t.Fatal("already lethal shot duplicated")
	}
}

func TestModernCoverageReleasesMissCompactionAndUncertainShots(t *testing.T) {
	s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
	weapon.DamageDefault = 100
	random := rng.NewSimulation(77)
	h, _ := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
	s.Records[int(h)-1].Velocity.Z = numeric.FixedFromInt(20)
	if _, ok := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); !ok {
		t.Fatal("deviated shot retained kill promise")
	}
	s.MarkDead(h)
	s.Compact(nil)
	if s.incoming[0].target != target || s.Count() != 1 {
		t.Fatal("compaction lost surviving prediction")
	}
	if _, ok := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); ok {
		t.Fatal("compacted surviving promise ignored")
	}
	s.MarkDead(1)
	s.Compact(nil)
	if _, ok := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); !ok {
		t.Fatal("miss/death failed to release coverage")
	}
	for _, change := range []func(){
		func() { target.Def = &content.UnitDef{UnitName: "moving", BMCode: 1, ModelTopFixed: 16 << 16} },
		func() { weapon.AreaOfEffect = 32 },
		func() { weapon.NoExplode = true },
		func() { weapon.WeaponVelocity = 1 << 16 },
	} {
		s, w, terrain, shooter, target, weapon = modernCombatFixture(t)
		change()
		for i := 0; i < 3; i++ {
			if _, ok := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); !ok {
				t.Fatal("uncertain launch was withheld")
			}
		}
	}
}

func TestModernIncomingEffectiveDamageETAAndIdentity(t *testing.T) {
	s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
	weapon.DamageDefault = 100
	target.Kills = 25 // victim veterancy reduces accepted damage [06 §9.2].
	random := rng.NewSimulation(77)
	modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
	if got := s.incomingDamage(shooter, target, w, terrain, 10, 6); got != 80 {
		t.Fatalf("effective incoming=%d want 80", got)
	}
	if got := s.incomingDamage(shooter, target, w, terrain, 10, 1); got != 0 {
		t.Fatalf("slow incoming covered a faster shot: %d", got)
	}
	// A mismatching allocation pointer must not cover a reused retail slot.
	replacement := *target
	if got := s.incomingDamage(shooter, &replacement, w, terrain, 10, 6); got != 0 {
		t.Fatalf("identity mismatch retained %d damage", got)
	}
	s.Rules = StrictRules{}
	if _, ok := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); !ok {
		t.Fatal("strict transition withheld")
	}
	s.Rules = &ModernRules{}
	if _, ok := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); !ok {
		t.Fatal("strict launch incorrectly created a Modern promise")
	}
	if _, ok := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); ok {
		t.Fatal("modern transition forgot retained live promises")
	}
}

func modernTargetQuery(s *Service, w *units.World, terrain *world.Terrain, shooter *units.Unit, targets ...*units.Unit) TargetQuery {
	q := TargetQuery{Shooter: shooter, Slot: shooter.SlotAt(0), Index: 0, World: w, Terrain: terrain}
	q.Acquisition = slotAcquisition(s, shooter, q.Slot, 0, w, nil, terrain, nil, nil, 0, -1)
	for _, target := range targets {
		q.Candidates = append(q.Candidates, acquisitionCandidate(shooter, target, 0, target.Flags, nil))
	}
	return q
}

func TestModernThreatAcquisitionRetentionAndKnowledge(t *testing.T) {
	s, w, terrain, shooter, factory, weapon := modernCombatFixture(t)
	h, err := w.Create(factory.Def, 1, cellCentre(8), numeric.FixedFromInt(10), cellCentre(1))
	if err != nil {
		t.Fatal(err)
	}
	tower := w.Unit(h)
	danger := *weapon
	danger.Range = 1000
	danger.DamageDefault = 150
	tower.InstallWeapon(0, &danger)
	q := modernTargetQuery(s, w, terrain, shooter, factory)
	// Factory lacks shootme. Modern takes opportunities for either controller.
	for _, control := range []uint8{ControlByteHuman, ControlByteComputer} {
		q.Acquisition.ShooterControlByte = control
		if got, _ := s.rules().SelectTarget(s, &q); got != factory.Handle {
			t.Fatal("controller-dependent opportunity")
		}
	}
	shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: factory.Handle}
	q = modernTargetQuery(s, w, terrain, shooter, factory, tower)
	if got, _ := s.rules().SelectTarget(s, &q); got != tower.Handle {
		t.Fatal("dangerous tower did not displace factory")
	}
	tower.SlotAt(0).Weapon = &content.WeaponDef{Range: 1000, LineOfSight: true, DamageDefault: 0}
	if got, _ := s.rules().SelectTarget(s, &q); got != factory.Handle {
		t.Fatal("harmless weapon displaced retained factory")
	}
	tower.SlotAt(0).Weapon = &danger
	s.Visibility = func(_ visibility.PlayerID, v visibility.Target) bool { return v.X != tower.X }
	if got, _ := s.rules().SelectTarget(s, &q); got != factory.Handle {
		t.Fatal("invisible threat acquired")
	}
	s.Visibility = func(visibility.PlayerID, visibility.Target) bool { return true }
	// An explicit slot never enters autonomous maintenance.
	shooter.SlotAt(0).Flags &^= units.SlotFlagAutonomous
	s.targets.primary[shooter.Owner] = []pool.Handle{factory.Handle, tower.Handle}
	for i := 0; i < 40; i++ {
		s.StepAutonomousForPlayer(shooter.Owner, w, nil, terrain, nil, nil, nil)
	}
	if shooter.SlotAt(0).Target.Unit != factory.Handle {
		t.Fatal("explicit binding replaced")
	}
}

func TestModernDangerNoticeIncludesUnarmedAndMissedLaunch(t *testing.T) {
	for _, modern := range []bool{true, false} {
		s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
		if !modern {
			s.Rules = StrictRules{}
		}
		notices := 0
		s.DangerNotice = func(v, a *units.Unit, tick uint32) {
			if v != target || a != shooter || tick != 10 {
				t.Fatal("wrong notice")
			}
			notices++
		}
		random := rng.NewSimulation(77)
		h, _ := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
		s.MarkDead(h)
		s.AcceptDamage(w, 10, DamageInput{Victim: target.Handle, Attacker: shooter.Handle, Kind: KindNoReaction, Nominal: 1})
		want := 0
		if modern {
			want = 2
		}
		if notices != want {
			t.Fatalf("modern=%v notices=%d want=%d", modern, notices, want)
		}
		s.Reaction.Allied = func(uint8, uint8) bool { return true }
		s.AcceptDamage(w, 10, DamageInput{Victim: target.Handle, Attacker: shooter.Handle, Kind: KindOrdinary, Nominal: 1})
		if notices != want {
			t.Fatal("allied damage raised danger")
		}
	}
}

func TestModernCoveredShotPreservesResourcesAndPhysicalFeedback(t *testing.T) {
	s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
	weapon.Turret = false
	weapon.DamageDefault = 100
	random := rng.NewSimulation(77)
	modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
	shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	shooter.SlotAt(0).Flags &^= units.SlotFlagAutonomous
	shooter.SlotAt(0).Ammo = 3
	econ := &economy.Service{}
	econ.Players[0].Stock[economy.Energy] = 100
	econ.Players[0].Stock[economy.Metal] = 100
	before, randomBefore := econ.Players[0], random
	pending := shooter.Pending
	sum := s.StepWeaponsForUnit(shooter, 10, w, nil, terrain, econ, nil, &random, nil)
	if sum.Fired != 0 || shooter.SlotAt(0).Reload != 0 || shooter.SlotAt(0).Ammo != 3 || econ.Players[0] != before || random != randomBefore || shooter.Pending != pending {
		t.Fatal("held launch spent resources or produced blocked movement feedback")
	}
	s.Rules = StrictRules{}
	sum = s.StepWeaponsForUnit(shooter, 10, w, nil, terrain, econ, nil, &random, nil)
	if sum.Fired != 1 || econ.Players[0].Stock[economy.Energy] != 93 || shooter.SlotAt(0).Reload == 0 {
		t.Fatalf("strict bypass did not launch: %+v", sum)
	}
}

func TestModernTargetMarginTieAlliesAndCoverage(t *testing.T) {
	s, w, terrain, shooter, a, weapon := modernCombatFixture(t)
	h, err := w.Create(a.Def, 1, cellCentre(8), numeric.FixedFromInt(10), cellCentre(1))
	if err != nil {
		t.Fatal(err)
	}
	b := w.Unit(h)
	enemyWeapon := *weapon
	enemyWeapon.DamageDefault = 60
	a.InstallWeapon(0, &enemyWeapon)
	b.InstallWeapon(0, &enemyWeapon)
	shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: b.Handle}
	q := modernTargetQuery(s, w, terrain, shooter, a, b)
	if got, _ := s.rules().SelectTarget(s, &q); got != b.Handle {
		t.Fatal("equal threat jittered to closer target")
	}
	stronger := enemyWeapon
	stronger.DamageDefault = 70
	a.SlotAt(0).Weapon = &stronger
	if got, _ := s.rules().SelectTarget(s, &q); got != b.Handle {
		t.Fatal("small advantage crossed switching margin")
	}
	stronger.DamageDefault = 150
	if got, _ := s.rules().SelectTarget(s, &q); got != a.Handle {
		t.Fatal("large danger advantage failed to replace retained target")
	}
	// Active fire against a nearby ally matters even when the enemy cannot
	// reach the querying unit. The allied target comes from the enemy slot.
	allyH, err := w.Create(a.Def, 0, cellCentre(7), numeric.FixedFromInt(10), cellCentre(1))
	if err != nil {
		t.Fatal(err)
	}
	enemyWeapon.Range = 32
	b.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: allyH}
	a.SlotAt(0).Weapon = &content.WeaponDef{Range: 1000, DamageDefault: 0}
	shooter.SlotAt(0).Target = units.Target{}
	if got, _ := s.rules().SelectTarget(s, &q); got != b.Handle {
		t.Fatal("nearby ally's attacker ignored")
	}
	// With neither threat armed, exact scores use distance then handle,
	// independent of input order and with no selection RNG.
	b.SlotAt(0).Weapon = a.SlotAt(0).Weapon
	b.X = a.X
	q = modernTargetQuery(s, w, terrain, shooter, b, a)
	random := rng.NewSimulation(7)
	q.Acquisition.RNG = &random
	before := random
	if got, _ := s.rules().SelectTarget(s, &q); got != a.Handle || random != before {
		t.Fatal("tie unstable or consumed RNG")
	}
	// Acquisition suppresses only completed coverage, and never reserves aim.
	b.X = cellCentre(8)
	weapon.DamageDefault = 100
	q = modernTargetQuery(s, w, terrain, shooter, a, b)
	if got, _ := s.rules().SelectTarget(s, &q); got != a.Handle {
		t.Fatal("aim unexpectedly reserved coverage")
	}
	modernLaunch(t, s, w, terrain, shooter, a, weapon, &random)
	if got, _ := s.rules().SelectTarget(s, &q); got != b.Handle {
		t.Fatal("acquisition ignored launched lethal coverage")
	}
	s.MarkDead(1)
	if got, _ := s.rules().SelectTarget(s, &q); got != a.Handle {
		t.Fatal("miss did not restore opportunity")
	}
}

func TestModernResponseSuitabilityAndProjectileDeadline(t *testing.T) {
	s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
	weapon.Range = 1
	if !ModernResponseAdmits(shooter, target, 0, terrain, nil) {
		t.Fatal("range blocked pursuit suitability")
	}
	weapon.DamageDefault = 0
	if ModernResponseAdmits(shooter, target, 0, terrain, nil) {
		t.Fatal("harmless response admitted")
	}
	weapon.DamageDefault = 100
	weapon.CommandFire = true
	if ModernResponseAdmits(shooter, target, 0, terrain, nil) {
		t.Fatal("command fire response admitted")
	}
	for _, owner := range []uint8{ControlByteHuman, ControlByteComputer} {
		if s.rules().AutonomousSlot(weapon, owner) {
			t.Fatal("Modern commandfire autonomy differs by controller")
		}
	}
	weapon.CommandFire = false
	weapon.Range = 1000
	random := rng.NewSimulation(77)
	modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
	// After projectile phase, the earliest next sample is the next tick. A
	// record expiring then cannot cover an acquisition during this tick.
	s.Records[0].ExpiryTick = 11
	s.rules().CombatTick(s, 10, true)
	if got := s.incomingDamage(shooter, target, w, terrain, 10, 6); got != 0 {
		t.Fatalf("expired phase forecast=%d", got)
	}
	// Reconstructed retail records carry no transient launch evidence.
	restored := &Service{Rules: &ModernRules{}, Slots: s.Slots, Records: s.Records}
	if got := restored.incomingDamage(shooter, target, w, terrain, 10, 6); got != 0 {
		t.Fatalf("load invented launch evidence=%d", got)
	}
}

func TestModernDistantShotWaitsForNearImpactKill(t *testing.T) {
	s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
	terrain.PlotAt(5, 1).SetOccupantA(0)
	target.X = cellCentre(15)
	terrain.PlotAt(15, 1).SetOccupantA(int16(target.Handle))
	weapon.DamageDefault = 100
	random := rng.NewSimulation(77)
	h, fired := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
	if !fired {
		t.Fatal("first distant shot refused")
	}
	// The shot has since traveled close to the static target. A new shot is
	// fourteen samples away, but this committed shot arrives in two.
	s.Records[int(h)-1].Pos.X = cellCentre(13)
	if _, fired = modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); fired {
		t.Fatal("distant shot ignored near-impact kill")
	}
	q := modernTargetQuery(s, w, terrain, shooter, target)
	if _, found := s.rules().SelectTarget(s, &q); found {
		t.Fatal("distant acquisition ignored near-impact kill")
	}
	// An actually faster arrival may still launch, avoiding a slow commitment
	// preventing an immediate finishing shot.
	fast := *weapon
	fast.ID++
	fast.WeaponVelocity = 224 << 16
	if _, fired = modernLaunch(t, s, w, terrain, shooter, target, &fast, &random); !fired {
		t.Fatal("faster finishing shot withheld")
	}
}

func TestModernOrderOwnedAcquisitionKeepsSwitchingMargin(t *testing.T) {
	s, w, terrain, shooter, challenger, weapon := modernCombatFixture(t)
	h, err := w.Create(challenger.Def, 1, cellCentre(8), numeric.FixedFromInt(10), cellCentre(1))
	if err != nil {
		t.Fatal(err)
	}
	retained := w.Unit(h)
	currentWeapon := *weapon
	currentWeapon.DamageDefault = 60
	retained.InstallWeapon(0, &currentWeapon)
	challengerWeapon := currentWeapon
	challengerWeapon.DamageDefault = 70
	challenger.InstallWeapon(0, &challengerWeapon)
	slot := shooter.SlotAt(0)
	slot.Flags &^= units.SlotFlagAutonomous
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: retained.Handle}
	original := slot.Target
	s.targets.primary[shooter.Owner] = []pool.Handle{challenger.Handle, retained.Handle}
	random := rng.NewSimulation(77)
	before := random
	// This is the order-facing API used by an automatic danger response even
	// while its normal attack row temporarily owns the weapon slot.
	acquire := func() pool.Handle {
		h, ok := s.AcquireWeaponTarget(shooter, 0, 0, w, nil, terrain, nil, nil, &random)
		if !ok {
			t.Fatal("automatic query found no target")
		}
		return h
	}
	if got := acquire(); got != retained.Handle {
		t.Fatal("order-owned slot lost switching margin")
	}
	challengerWeapon.DamageDefault = 150
	if got := acquire(); got != challenger.Handle {
		t.Fatal("large threat improvement failed to cross margin")
	}
	if slot.Target != original || random != before {
		t.Fatal("selection installed target or consumed RNG")
	}
}

func TestModernEffectiveDamageRejectsHealthWrap(t *testing.T) {
	s, w, terrain, shooter, target, weapon := modernCombatFixture(t)
	for _, tc := range []struct {
		health, amount int32
		want           int64
	}{
		{100, 65535, 0}, // receiver health becomes 101, not a kill
		{100, 40000, 0}, // wrapped positive health cannot promise damage
		{100, 300, 300}, // ordinary indivisible lethal shot remains effective
		{100, 100, 100},
		{100, 60, 60},
		{65536 + 100, 60, 0}, // malformed wider storage must not create a huge loss
		{-1, 60, 0},
	} {
		target.Health = tc.health
		weapon.DamageDefault = tc.amount
		if got := s.effectiveDamage(weapon, target, shooter); got != tc.want {
			t.Fatalf("health=%d amount=%d effective=%d want=%d", tc.health, tc.amount, got, tc.want)
		}
	}
	target.Health = 100
	weapon.DamageDefault = 65535
	if got := ApplyDamage(target.Health, 65535); got != 101 {
		t.Fatalf("receiver health=%d want101", got)
	}
	if ModernResponseAdmits(shooter, target, 0, terrain, nil) {
		t.Fatal("health-increasing packet admitted as positive response damage")
	}
	random := rng.NewSimulation(77)
	for i := 0; i < 3; i++ {
		if _, fired := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random); !fired {
			t.Fatal("wrapped damage created a false kill promise")
		}
	}
	if got := s.incomingDamage(shooter, target, w, terrain, 10, 6); got != 0 {
		t.Fatalf("wrapped incoming promised %d damage", got)
	}
	s.AcceptDamage(w, 10, DamageInput{Victim: target.Handle, Attacker: shooter.Handle, Kind: KindNoReaction, Nominal: 65535})
	if target.Health != 101 {
		t.Fatalf("actual accepted health=%d want101", target.Health)
	}
}

// Modern wave air targets' capability test (DESIGN_SESSIONS_AI_SAVE "Modern
// wave air targets"): a direct weapon may pursue an airborne target, a
// ballistic or water weapon may not, and a command-fire weapon never counts.
func TestModernAirPursuitAdmitsByWeaponKind(t *testing.T) {
	_, _, terrain, shooter, target, weapon := modernCombatFixture(t)
	target.Move.ModeMirror = airborneMoverMode
	if !ModernAirPursuitAdmits(shooter, target, terrain, nil) {
		t.Fatal("a direct weapon could not pursue an airborne target")
	}
	weapon.Ballistic = true
	if ModernAirPursuitAdmits(shooter, target, terrain, nil) {
		t.Fatal("a ballistic weapon counted as anti-air")
	}
	weapon.Ballistic, weapon.WaterWeapon = false, true
	if ModernAirPursuitAdmits(shooter, target, terrain, nil) {
		t.Fatal("a water weapon counted as anti-air")
	}
	weapon.WaterWeapon, weapon.CommandFire = false, true
	if ModernAirPursuitAdmits(shooter, target, terrain, nil) {
		t.Fatal("a command-fire weapon counted as anti-air")
	}
	weapon.CommandFire, weapon.ToAirWeapon = false, true
	if !ModernAirPursuitAdmits(shooter, target, terrain, nil) {
		t.Fatal("an anti-air weapon could not pursue an airborne target")
	}
}
