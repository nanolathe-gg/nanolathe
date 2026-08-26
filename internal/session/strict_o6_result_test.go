package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// o6AimAndFireProgram is the smallest authored weapon script that satisfies
// the strict weapon readiness contract: AimPrimary returns nonzero and
// FirePrimary returns normally. The projectile is still admitted by the
// ordinary weapon/order path; this fixture does not call combat or damage
// helpers directly [06 §3.3][F-P0-024].
func o6AimAndFireProgram() *cob.Program {
	return &cob.Program{
		Code:        []uint32{0x10021001, 1, 0x10065000, 0x10065000},
		Scripts:     map[string]int{"AimPrimary": 0, "FirePrimary": 3},
		ScriptsByID: []int{0, 3},
		Pieces:      []string{"base"},
	}
}

type o6ResultRun struct {
	TraceHash   string
	StateHash   string
	Result      Result
	ArmedTick   uint32
	FinalTick   uint32
	DeathCount  int
	Callback    int
	Damage      bool
	Projectile  bool
	Corpse      bool
	CorpseCount int
	Order       bool
	Aim         bool
	COBReturn   bool
	Fire        bool
	ResultTrace int
}

// runO6NaturalResult starts a two-player configured skirmish and drives one
// real typed attack command through Session.EnqueueHumanCommand. It never
// writes Health, Alive, Dying, DeathCause, Result, or EndLatch. All terminal
// state must therefore come from the authoritative weapon, projectile,
// damage, death-finalization, corpse, and result paths.
func runO6NaturalResult(t *testing.T, simSeed, crtSeed uint32) o6ResultRun {
	t.Helper()
	rng.SeedGlobal(simSeed, crtSeed)

	cat := strictMinimalCatalog()
	weapon := &content.WeaponDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("o6gun")},
		ID:               77,
		WeaponVelocity:   5 * 65536,
		Range:            100 * 65536,
		ReloadTime:       2,
		DamageDefault:    400,
		Damage:           map[string]int32{"default": 400},
		LineOfSight:      true,
		Turret:           true,
		WaterWeapon:      true,
	}
	cat.Weapons = map[string]*content.WeaponDef{weapon.CanonicalKey: weapon}
	cat.RebuildWeaponIndex()
	shooterDef := cat.Units["armcom"]
	shooterDef.Commander = true
	shooterDef.Builder = false
	shooterDef.CanAttack = true
	shooterDef.SightDistance = 300
	shooterDef.Weapon1 = "o6gun"
	shooterDef.Weapon1Def = weapon
	targetDef := cat.Units["corcom"]
	targetDef.Commander = true
	targetDef.Builder = false
	targetDef.MaxDamage = 1000
	targetDef.SightDistance = 300
	targetDef.Corpse = "o6corpse"
	corpseDef := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("o6corpse")},
		Object:           "o6corpse",
		FootprintX:       1,
		FootprintZ:       1,
		Damage:           100,
		Metal:            20,
		Energy:           20,
		Reclaimable:      true,
	}
	if cat.Features == nil {
		cat.Features = map[string]*content.FeatureDef{}
	}
	cat.Features[corpseDef.CanonicalKey] = corpseDef

	terrain := strictMinimalTerrain()
	s := &Session{
		Catalog: cat,
		World:   terrain,
		Mission: strictSyntheticMission(),
		Skirmish: SkirmishConfig{
			MapName:        "test",
			NumPlayers:     2,
			CommanderDeath: 1,
		},
	}
	// Explicit allied teams exercise team-based result evaluation rather than
	// the per-owner fallback. Controller 0 is the local human production path;
	// controller 2 is a configured computer opponent [08 "Skirmish configuration"].
	s.Skirmish.Players[0].AllyGroup = 1
	s.Skirmish.Players[1].AllyGroup = 2
	s.Skirmish.Players[0].Controller = 0
	s.Skirmish.Players[1].Controller = 2
	s.Skirmish.ApplyDefaults()

	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("new unit world: %v", err)
	}
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.StatusHalfwordAt144 = 1
		p.EndGameCountdown = -1
		p.Allies[i] = true
	}
	s.Econ.Players[0].Allies[0] = true
	s.Econ.Players[1].Allies[1] = true
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(crtSeed)
	s.InitWindForSession(&crt, 0)
	if err := createAndBindServices(s); err != nil {
		t.Fatalf("bind services: %v", err)
	}
	s.RegisterAll()
	s.LocalOwner = 0
	s.EnemyOwner = 1
	s.Clock.ScaledAnchor = 0

	hShooter, err := s.Units.Create(shooterDef, 0, numeric.Fixed(10*65536), 0, numeric.Fixed(10*65536))
	if err != nil {
		t.Fatalf("create shooter: %v", err)
	}
	hTarget, err := s.Units.Create(targetDef, 1, numeric.Fixed(12*65536), 0, numeric.Fixed(12*65536))
	if err != nil {
		t.Fatalf("create commander: %v", err)
	}
	shooter := s.Units.Unit(hShooter)
	target := s.Units.Unit(hTarget)
	if shooter == nil || target == nil {
		t.Fatal("created commanders not addressable")
	}
	shooter.SetScript(cob.NewVM(o6AimAndFireProgram()))
	publishOne(s, shooter)
	publishOne(s, target)
	s.Movement.EnsureUnit(shooter)
	s.Movement.EnsureUnit(target)
	// Battle entry is the post-initialization boundary. All fixture placement,
	// authored script binding, and occupancy publication happen before it; the
	// replay below can only enqueue typed input and advance the session.
	s.State = StateBattle
	s.SetTraceEnabled(true)
	s.ClearTrace()

	// Code 1 resolves to the authored hostile Attack_Chase descriptor. The
	// enqueue is presentation/input only; authoritative queue admission occurs
	// at the next input boundary [01 §4.4][07 §9].
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
		Handles: []pool.Handle{hShooter},
		Code:    1,
		Target:  hTarget,
	}}); err != nil {
		t.Fatalf("enqueue typed attack: %v", err)
	}

	callbackCount := 0
	s.SetResultCallback(func(Result) { callbackCount++ })
	var out o6ResultRun
	const maxTicks = 600
	for tick := 1; tick <= maxTicks; tick++ {
		s.Step(int32(tick))
		if q := orders.QueueForUnit(shooter); q != nil {
			for _, n := range q.Primary() {
				if n != nil && n.Target == hTarget && orders.DescriptorFor(n.ID).Name == "Attack_Chase" {
					out.Order = true
				}
			}
		}
		for _, ev := range s.TraceEvents() {
			switch {
			case ev.Handle == hShooter && ev.Kind == TraceWeaponAimDispatch:
				out.Aim = true
			case ev.Handle == hShooter && ev.Kind == TraceCOBReturn:
				out.COBReturn = true
			case ev.Handle == hShooter && ev.Kind == TraceWeaponFire:
				out.Fire = true
			}
		}
		if target.Health < target.MaxHealth {
			out.Damage = true
		}
		if s.Combat != nil && s.Combat.Count() > 0 {
			out.Projectile = true
		}
		for _, f := range s.Features.Instances() {
			if f != nil && f.Def != nil && f.Def.CanonicalKey == corpseDef.CanonicalKey {
				out.Corpse = true
			}
		}
		if s.GetResult().Ended {
			break
		}
	}
	for _, ev := range s.TraceEvents() {
		if ev.Kind == TraceDeathFinalize && ev.Handle == hTarget {
			out.DeathCount++
		}
		if ev.Kind == TraceVictoryLatch {
			out.ResultTrace++
		}
	}
	if s.Features != nil {
		for _, f := range s.Features.Instances() {
			if f != nil && f.Def != nil && f.Def.CanonicalKey == corpseDef.CanonicalKey {
				out.CorpseCount++
			}
		}
	}
	out.Corpse = out.CorpseCount > 0
	out.Callback = callbackCount
	out.Result = s.GetResult()
	out.ArmedTick = s.GetResultArmedTick()
	out.FinalTick = s.Clock.GlobalTick
	out.TraceHash = HashTrace(s.TraceEvents())
	out.StateHash = HashState(s)

	if !out.Order {
		t.Fatalf("O6: typed attack never entered authoritative queue; trace=%s", LastNTraceStrings(s.TraceEvents(), 20))
	}
	if !out.Aim || !out.COBReturn || !out.Fire {
		t.Fatalf("O6: weapon handshake incomplete aim=%v cob_return=%v fire=%v trace=%s", out.Aim, out.COBReturn, out.Fire, LastNTraceStrings(s.TraceEvents(), 30))
	}
	if !out.Projectile || !out.Damage {
		t.Fatalf("O6: natural attack chain missing projectile=%v damage=%v trace=%s", out.Projectile, out.Damage, LastNTraceStrings(s.TraceEvents(), 20))
	}
	if out.DeathCount != 1 {
		t.Fatalf("O6: target commander death-finalization count=%d, want exactly one", out.DeathCount)
	}
	if out.CorpseCount != 1 {
		t.Fatalf("O6: target death produced %d authored corpse features, want exactly one", out.CorpseCount)
	}
	if !out.Result.Ended {
		t.Fatalf("O6: natural commander death did not reach terminal result by %d ticks: %+v latch=%+v trace=%s", maxTicks, out.Result, s.Latch, LastNTraceStrings(s.TraceEvents(), 40))
	}
	if out.Result.Draw || out.Result.WinnerTeam != s.TeamForOwner(0) || out.Result.Reason != ReasonCommanderDeath {
		t.Fatalf("O6: alliance-aware result want local team %d commander-death victory, got %+v", s.TeamForOwner(0), out.Result)
	}
	if out.Callback != 1 {
		t.Fatalf("O6: result callback count=%d, want exactly one", out.Callback)
	}
	if out.ResultTrace != 1 {
		t.Fatalf("O6: victory-latch trace count=%d, want exactly one", out.ResultTrace)
	}
	if out.ArmedTick == 0 || out.Result.Tick <= out.ArmedTick || out.Result.Countdown >= 0 || !s.Latch.IsEnding() {
		t.Fatalf("O6: result countdown/transition not observed: armed=%d result=%+v latch=%+v", out.ArmedTick, out.Result, s.Latch)
	}
	if s.State != StatePostBattle {
		t.Fatalf("O6: terminal result did not enter post-battle state: %v", s.State)
	}
	return out
}

// TestStrictSkirmish_NaturalCommanderDeathResult implements RESULT-01. It
// deliberately uses the production typed input boundary and observes every
// causal milestone; no test shortcut marks a unit dead or latches a result.
// The AI tactical-group producer remains a documented R-P0-04 unknown, so
// this gate does not seed manager vectors or claim natural AI wave behavior.
func TestStrictSkirmish_NaturalCommanderDeathResult(t *testing.T) {
	const simSeed, crtSeed uint32 = 601, 701
	a := runO6NaturalResult(t, simSeed, crtSeed)
	b := runO6NaturalResult(t, simSeed, crtSeed)
	if a.TraceHash != b.TraceHash || a.StateHash != b.StateHash {
		t.Fatalf("O6 deterministic replay mismatch: trace %s/%s state %s/%s", a.TraceHash, b.TraceHash, a.StateHash, b.StateHash)
	}
	if a.Result.Tick != b.Result.Tick || a.ArmedTick != b.ArmedTick || a.FinalTick != b.FinalTick {
		t.Fatalf("O6 deterministic result timing mismatch: result %d/%d armed %d/%d final %d/%d", a.Result.Tick, b.Result.Tick, a.ArmedTick, b.ArmedTick, a.FinalTick, b.FinalTick)
	}
}
