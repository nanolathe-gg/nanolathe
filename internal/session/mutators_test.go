package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestApplyEntryMutatorsClonesOnlyWhenNeeded: with no mutators battle entry
// runs on the very catalog it was handed, so no identity or fingerprint can
// move; with mutators it runs on a mutated clone and the handed-in catalog,
// which may be shared between battles, is never written
// (docs/DESIGN_MODS_MUTATORS.md §6.3, §6.6).
func TestApplyEntryMutatorsClonesOnlyWhenNeeded(t *testing.T) {
	cat := minimalCatalogForStrict()
	cat.Hash = "base"
	cat.Units["armcom"].WorkerTime = 300
	cat.Units["armcom"].BuildTime = 3000
	cat.Units["armcom"].BuildCostMetal = 2000
	for _, zero := range []content.Mutators{{}, {BuildSpeed: content.Factor{Num: 1, Den: 1}}} {
		got, err := applyEntryMutators(cat, zero)
		if err != nil || got != cat {
			t.Fatalf("zero set %+v returned %p (%v), want the same catalog %p", zero, got, err, cat)
		}
	}
	m := content.Mutators{BuildSpeed: content.Factor{Num: 2, Den: 1}, BuildCost: content.Factor{Num: 1, Den: 2}}
	got, err := applyEntryMutators(cat, m)
	if err != nil {
		t.Fatal(err)
	}
	if got == cat || got.Hash == cat.Hash {
		t.Fatal("a mutated battle must run on a clone with its own identity")
	}
	if u := got.Units["armcom"]; u.BuildTime != 1500 || u.WorkerTime != 300 || u.BuildCostMetal != 1000 {
		t.Fatalf("mutated commander = buildtime %d workertime %d metal %v, want 1500, an untouched 300 and 1000", u.BuildTime, u.WorkerTime, u.BuildCostMetal)
	}
	if u := cat.Units["armcom"]; u.BuildTime != 3000 || u.BuildCostMetal != 2000 || cat.Hash != "base" {
		t.Fatal("the shared catalog was written")
	}
	if _, err := applyEntryMutators(cat, content.Mutators{BuildSpeed: content.Factor{Num: 5, Den: 1}}); err == nil {
		t.Fatal("a factor off the step list was accepted")
	}
}

// strictHelpBuildCatalog is one mobile builder and one product. The callers
// pick a workertime and a buildtime whose ratio of quantum (workertime/30, an
// integer [05 R-WORK-01 §1]) to buildtime is a power of two, so every
// construction step, scaled or not, is an exact binary fraction and runs can
// be compared for equal billing without a tolerance.
func strictHelpBuildCatalog(workerTime, buildTime int32) *content.Catalog {
	cat := minimalCatalogForStrict()
	mk := func(name string, f func(*content.UnitDef)) {
		d := &content.UnitDef{
			UnitName: name, ObjectName: name, MaxDamage: 100, Limit: -1,
			SightDistance: 64, MovementClass: "testmove",
			FootprintX: 1, FootprintZ: 1, BMCode: 1, CanMove: true,
			MaxVelocity: 1 << 16, TurnRate: 100, Acceleration: 1 << 10, BrakeRate: 1 << 10,
		}
		f(d)
		d.CanonicalKey = content.CanonicalKey(name)
		cat.Units[d.CanonicalKey] = d
	}
	mk("mutatorcon", func(d *content.UnitDef) {
		d.Builder = true
		d.WorkerTime = workerTime
		d.BuildDistance = 400
		// Settlement rebuilds capacity from the owner's units, so the builder
		// carries the storage that keeps the starting stock.
		d.MetalStorage, d.EnergyStorage = 2000, 2000
	})
	mk("mutatorprod", func(d *content.UnitDef) {
		d.BuildTime = buildTime
		d.BuildCostEnergy = 64
		d.BuildCostMetal = 32
	})
	installFixtureCOB(cat)
	return cat
}

// strictHelpBuild runs one helper-driven construction under Strict 3.1 on the
// given catalog, using the assisted-completion fixture's scene, and reports
// the number of ticks on which the frame's remaining fraction fell and what
// the owner paid in total once settlement has caught up.
func strictHelpBuild(t *testing.T, cat *content.Catalog) (workTicks int, metal, energy float32) {
	t.Helper()
	s := &Session{Gameplay: gameplay.Strict31, Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(), LocalOwner: 0}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatal(err)
	}
	s.Units = w
	s.Econ = economyForTest()
	for i := range s.Econ.Players {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = 1
		p.EndGameCountdown = -1
		p.Stock[economy.Metal], p.Stock[economy.Energy] = 1000, 1000
		p.Capacity[economy.Metal], p.Capacity[economy.Energy] = 2000, 2000
	}
	s.Econ.SeedDeadlines(0)
	s.Clock = &clock.State{}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	if s.Rules.Name != StrictRuleSetName {
		t.Fatalf("scene bound %q, want Strict 3.1", s.Rules.Name)
	}
	conDef, prodDef := cat.Units["mutatorcon"], cat.Units["mutatorprod"]
	hHelper, err := w.Create(conDef, 0, world.CellToWorld(10), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatal(err)
	}
	hFrame, err := w.CreateNanoframe(prodDef, 0, world.CellToWorld(9), 0, world.CellToWorld(9))
	if err != nil {
		t.Fatal(err)
	}
	helper, frame := w.Unit(hHelper), w.Unit(hFrame)
	s.Movement.BindWorld(w)
	s.Movement.EnsureUnit(helper)
	frame.Remaining = 1
	frame.Health = 0
	frame.MaxHealth = int32(prodDef.MaxDamage)
	frame.SetActivationEdge(false)
	helper.InBuildStance = true
	orders.QueueForUnit(helper).Push(orders.Lookup("HelpBuild"), orders.Node{
		Owner: hHelper, Target: hFrame,
		GoalX: frame.X, GoalY: frame.Y, GoalZ: frame.Z, GoalSupplied: true,
	})
	startMetal, startEnergy := s.Econ.Players[0].Stock[economy.Metal], s.Econ.Players[0].Stock[economy.Energy]
	finished := uint32(0)
	// Settlement runs once every 30 ticks [05 "Authoritative settlement
	// order"], so the run continues two intervals past completion for the
	// last requests to reach the stock.
	for tick := uint32(1); tick <= 400 && (finished == 0 || tick <= finished+60); tick++ {
		before := frame.Remaining
		s.Clock.GlobalTick = tick
		s.stepAuthoritativePhases(tick)
		if frame.Remaining < before {
			workTicks++
		}
		if finished == 0 && frame.Remaining == 0 {
			finished = tick
		}
	}
	if finished == 0 {
		t.Fatalf("the helper never finished the frame: remaining %v", frame.Remaining)
	}
	return workTicks, startMetal - s.Econ.Players[0].Stock[economy.Metal], startEnergy - s.Econ.Players[0].Stock[economy.Energy]
}

// TestStrictBuildSpeedScalesConstructionAtEqualCost is the relationship §10
// asks for, under Strict 3.1 because mutators apply there too (D1). Build
// speed divides the product's buildtime, so ×2 halves and ×0.5 doubles the
// work ticks of the same construction and both bill the same total, because
// spend follows progress [05 R-WORK-01 §1]. Two of the builders have a
// workertime that is not a multiple of thirty — 50 and 80, the stock air and
// kbot constructors' values — whose integer quantum a workertime multiplier
// would have distorted or zeroed; here no step stalls.
func TestStrictBuildSpeedScalesConstructionAtEqualCost(t *testing.T) {
	for _, tc := range []struct {
		workerTime, buildTime int32
		visits                int // buildtime / quantum
	}{
		{50, 64, 64},   // quantum 1
		{80, 128, 64},  // quantum 2
		{240, 256, 32}, // quantum 8
	} {
		for _, run := range []struct {
			speed  content.Factor
			visits int
		}{
			{content.Factor{}, tc.visits},
			{content.Factor{Num: 2, Den: 1}, tc.visits / 2},
			{content.Factor{Num: 1, Den: 2}, tc.visits * 2},
		} {
			cat, err := applyEntryMutators(strictHelpBuildCatalog(tc.workerTime, tc.buildTime), content.Mutators{BuildSpeed: run.speed})
			if err != nil {
				t.Fatal(err)
			}
			if got := cat.Units["mutatorcon"].WorkerTime; got != tc.workerTime {
				t.Fatalf("build speed moved workertime to %d", got)
			}
			ticks, metal, energy := strictHelpBuild(t, cat)
			if ticks != run.visits || metal != 32 || energy != 64 {
				t.Fatalf("workertime %d buildtime %d at ×%s: %d work ticks billing %v metal %v energy, want %d ticks and the authored 32 and 64",
					tc.workerTime, tc.buildTime, run.speed, ticks, metal, energy, run.visits)
			}
		}
	}
}

// mutatorCampaignMission is a stock mission whose entry carries a unit
// restriction, so the restore below also exercises the restriction's order.
const mutatorCampaignMission = "camps/Arm Campaign.tdf:MISSION0"

// TestEntrySitesApplyMutatorsInStrict checks the three battle-entry sites on
// the reference install, under Strict 3.1 because mutators apply there too
// (D1): a fresh skirmish and a fresh campaign mission run on a mutated clone
// and record the set, a zero set leaves the skirmish on the handed-in catalog,
// and restoring the campaign's save with the same set rebuilds the fresh
// entry's mutated identity exactly. The shared compiled catalog is never
// written. Skipped without retail assets.
func TestEntrySitesApplyMutatorsInStrict(t *testing.T) {
	f := loadRetailFixture(t)
	m := content.Mutators{BuildSpeed: content.Factor{Num: 2, Den: 1}, BuildCost: content.Factor{Num: 1, Den: 2}}
	base := f.cat.Units[content.CanonicalKey(f.cat.Sides[0].Commander)]
	baseWorker, baseTime, baseMetal, baseHash := base.WorkerTime, base.BuildTime, base.BuildCostMetal, f.cat.Hash
	checkCommander := func(site string, cat *content.Catalog) {
		t.Helper()
		u := cat.Units[base.CanonicalKey]
		if cat == f.cat || cat.Hash == baseHash {
			t.Fatalf("%s: ran on the shared catalog identity", site)
		}
		if u.BuildTime != (baseTime+1)/2 || u.WorkerTime != baseWorker || u.BuildCostMetal != float32((int64(baseMetal)+1)/2) {
			t.Fatalf("%s: commander buildtime %d workertime %d metal %v, want half of %d, %d and half of %v", site, u.BuildTime, u.WorkerTime, u.BuildCostMetal, baseTime, baseWorker, baseMetal)
		}
	}

	f.cfg.Gameplay = gameplay.Strict31
	plain, err := NewSkirmishWithEntryOptions(f.fs, f.cat, f.cfg, SkirmishEntryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Catalog != f.cat || !plain.Mutators.IsZero() {
		t.Fatal("a skirmish without mutators must run on the handed-in catalog")
	}
	skirmish, err := NewSkirmishWithEntryOptions(f.fs, f.cat, f.cfg, SkirmishEntryOptions{Mutators: m})
	if err != nil {
		t.Fatal(err)
	}
	if skirmish.Mutators != m || skirmish.Rules.Name != StrictRuleSetName {
		t.Fatalf("skirmish bound %+v under %q", skirmish.Mutators, skirmish.Rules.Name)
	}
	checkCommander("skirmish", skirmish.Catalog)

	src, err := NewMissionWithEntryOptions(f.fs, f.cat, mutatorCampaignMission, 0, 7, 7, MissionEntryOptions{Gameplay: gameplay.Strict31, Mutators: m}, nil)
	if err != nil {
		t.Skipf("stock campaign %q is unavailable: %v", mutatorCampaignMission, err)
	}
	if src.Mutators != m {
		t.Fatalf("mission bound %+v", src.Mutators)
	}
	checkCommander("mission", src.Catalog)
	inputs, err := src.RetailBattleSaveInputs(RetailBattleSummary(src, "mutators", "0", SkirmishDefaultUnitLimit), save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := src.RetailProjection(inputs)
	if err != nil {
		t.Fatal(err)
	}
	data, err := projection.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	bank, err := save.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := StageRetailBattle(bank, RetailLoadDeps{FS: f.fs, Catalog: f.cat, SimSeed: 7, CRTSeed: 7, UnitLimit: src.Skirmish.UnitLimit, Gameplay: gameplay.Strict31, Mutators: m})
	if err != nil {
		t.Fatal(err)
	}
	dst := staged.Session
	if dst.Mutators != m || dst.Catalog.Hash != src.Catalog.Hash {
		t.Fatalf("restore bound %+v with digest %q, want the fresh entry's %q", dst.Mutators, dst.Catalog.Hash, src.Catalog.Hash)
	}
	checkCommander("restore", dst.Catalog)
	if f.cat.Hash != baseHash || base.BuildTime != baseTime || base.WorkerTime != baseWorker || base.BuildCostMetal != baseMetal {
		t.Fatal("an entry wrote the shared compiled catalog")
	}
}

// TestStockCatalogUnderExtremeSteps applies every mutator at the step list's
// two ends to the reference catalog: every stock cost is an integral store, so
// neither end is refused. It then checks the two 32-bit products the mutators
// reach, at the step that grows each most (docs/DESIGN_MODS_MUTATORS.md §6.5
// "Known hazards"):
//
//   - the abandoned-frame decay's 11·buildtime at Build speed ×0.25
//     [05 R-WORK-01 §9][05 R-WORK-01 §11];
//   - the unit-reclaim pulse's workertime × kill factor × maxdamage × 15 at
//     Health ×4 with no kills, for the largest stock reclaimer and reclaimable
//     target [05 R-WORK-01 §4] (Build speed no longer reaches this product).
//
// It also checks that at ×4 no maxdamage or weapon damage passes 32,767, the
// signed 16-bit live health and packet amount they reach [04 §4.4][06 §9.2],
// and logs how many stock records the cap saturates.
//
// Skipped without retail assets.
func TestStockCatalogUnderExtremeSteps(t *testing.T) {
	f := loadRetailFixture(t)
	slowest, top := content.MutatorSteps[0], content.MutatorSteps[len(content.MutatorSteps)-1]
	every := func(k content.Factor) content.Mutators {
		return content.Mutators{BuildSpeed: k, BuildCost: k, Health: k, Damage: k, Sight: k, Radar: k}
	}
	if err := f.cat.Clone().ApplyMutators(every(slowest)); err != nil {
		t.Fatalf("stock catalog refused %s: %v", slowest, err)
	}
	high := f.cat.Clone()
	if err := high.ApplyMutators(every(top)); err != nil {
		t.Fatalf("stock catalog refused %s: %v", top, err)
	}
	slow := f.cat.Clone()
	if err := slow.ApplyMutators(content.Mutators{BuildSpeed: slowest}); err != nil {
		t.Fatal(err)
	}
	var longest int64
	for _, u := range slow.UnitRecords() {
		longest = max(longest, int64(u.BuildTime))
	}
	t.Logf("longest buildtime at build speed ×%s: %d; decay product 11·buildtime %d", slowest, longest, 11*longest)
	if 11*longest > 1<<31-1 {
		t.Fatalf("a stock decay product overflows at Build speed ×%s: 11·%d", slowest, longest)
	}

	const word = 1<<15 - 1
	saturates := func(v int64) bool { return v > 0 && v*int64(top.Num) > word*int64(top.Den) }
	var worker, health int64
	cappedUnits := 0
	for _, u := range high.UnitRecords() {
		if u.CanReclamate {
			worker = max(worker, int64(uint16(u.WorkerTime)))
		}
		if !u.CanCapture {
			health = max(health, int64(u.MaxDamage))
		}
		if u.MaxDamage > word {
			t.Fatalf("%s maxdamage %d at ×%s passes the signed 16-bit live health", u.UnitName, u.MaxDamage, top)
		}
	}
	for _, u := range f.cat.UnitRecords() {
		if saturates(int64(u.MaxDamage)) {
			cappedUnits++
		}
	}
	pulse := worker * health * 15
	t.Logf("largest reclaim-pulse product at health ×%s with no kills: %d; the kill factor that overflows it: %d", top, pulse, (1<<31-1)/pulse+1)
	if pulse > 1<<31-1 {
		t.Fatalf("a stock reclaim pulse overflows at Health ×%s with no kills: %d", top, pulse)
	}
	for _, w := range high.WeaponRecordsByID() {
		if int64(uint16(w.DamageDefault)) > word {
			t.Fatalf("weapon %s default %d at ×%s passes the signed 16-bit packet", w.CanonicalKey, w.DamageDefault, top)
		}
		for _, key := range w.DamageKeysSorted() {
			if w.Damage[key] > word {
				t.Fatalf("weapon %s override %s=%d at ×%s passes the signed 16-bit packet", w.CanonicalKey, key, w.Damage[key], top)
			}
		}
	}
	cappedWeapons := 0
	for _, w := range f.cat.WeaponRecordsByID() {
		capped := saturates(int64(uint16(w.DamageDefault)))
		for _, key := range w.DamageKeysSorted() {
			capped = capped || saturates(int64(w.Damage[key]))
		}
		if capped {
			cappedWeapons++
		}
	}
	t.Logf("at ×%s the 32,767 cap saturates %d stock units' maxdamage and %d stock weapons' damage", top, cappedUnits, cappedWeapons)
}
