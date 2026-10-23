//go:build retail

package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestSmartMakersRunStockMakers drives the Smart makers mutator through an
// ordinary Strict 3.1 skirmish on the reference install
// (docs/DESIGN_MODS_MUTATORS.md "Smart makers"). Battle entry marks exactly
// the stock metal makers. The local player gets two energy storages, a
// fusion plant, two Moho makers and four small makers, whose upkeep (1,840)
// exceeds what the player makes, all switched off.
//
//   - With storage held full, the first settled pass that counts the new
//     storages switches both Mohos on: they are the most efficient makers.
//   - Running free, every switch lands on one of the player's settled
//     passes, a pass that switches a Moho off has already switched every
//     small maker off, storage never falls further below 40% than one pass's
//     upkeep, and the makers keep converting energy into metal.
//
// These are relationships, not a census. Skipped without retail assets.
func TestSmartMakersRunStockMakers(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	cfg := DirectSkirmishConfig("ashap plateau")
	cfg.ApplyDefaults()
	cfg.Gameplay = gameplay.Strict31
	cfg.RNGSimSeed, cfg.RNGCrtSeed = aiE2ESeed, aiE2ESeed
	s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{Mutators: content.Mutators{SmartMakers: content.SwitchOn()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range s.Catalog.SortedUnitKeys() {
		u := s.Catalog.Units[key]
		if want := content.SmartMakerCandidate(u); u.SmartMaker != want || (u.SmartMaker && u.MakesMetal == 0) {
			t.Fatalf("%s: marked %v, candidate %v", key, u.SmartMaker, want)
		}
	}
	var marked []string
	for _, key := range s.Catalog.SortedUnitKeys() {
		if s.Catalog.Units[key].SmartMaker {
			marked = append(marked, key)
		}
	}
	// Exactly the six stock makers, Moho and floating makers included
	// (DESIGN_MODS_MUTATORS §6.5).
	if got := strings.Join(marked, ","); got != "armfmkr,armmakr,armmmkr,corfmkr,cormakr,cormmkr" {
		t.Fatalf("marked %s, want the six stock metal makers", got)
	}
	driver := int32(1 << 20)
	step := func() {
		s.Step(driver)
		driver++
	}
	for i := 0; i < 60 && s.State != StateBattle; i++ {
		step()
	}
	owner := uint8(s.LocalOwner)
	var commander *units.Unit
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Def != nil && u.Def.Commander && u.Owner == owner {
			commander = u
			break
		}
	}
	if commander == nil {
		t.Fatal("no local commander")
	}
	create := func(key string, dx, dz int32) *units.Unit {
		def, ok := s.Catalog.Unit(key)
		if !ok {
			t.Skipf("%s absent from the reference install", key)
		}
		x, z := commander.X+world.CellToWorld(dx), commander.Z+world.CellToWorld(dz)
		h, err := s.Units.Create(def, owner, x, s.World.HeightAt(x, z), z)
		if err != nil {
			t.Fatal(err)
		}
		s.CompleteUnit(h)
		return s.Units.Unit(h)
	}
	create("armestor", 12, -12)
	create("armestor", 18, -12)
	create("armfus", 12, 12)
	mohos := []*units.Unit{create("armmmkr", -12, -12), create("armmmkr", -18, -12)}
	var small []*units.Unit
	for i := int32(0); i < 4; i++ {
		small = append(small, create("armmakr", -18+6*i, 14))
	}
	makers := append(append([]*units.Unit(nil), mohos...), small...)
	for _, u := range makers {
		if u.COBBinding() == nil {
			t.Fatal("stock maker has no script binding")
		}
		u.SetActivationEdge(false)
	}
	p := &s.Econ.Players[owner]
	states := func() []bool {
		out := make([]bool, len(makers))
		for i, u := range makers {
			out[i] = u.Activated
		}
		return out
	}

	// Held full: the first settled pass that counts the new storages switches
	// both Mohos on. The pass after the storages are created rebuilds capacity
	// from them, so storage is held full across two settled passes.
	for passes := 0; passes < 2; {
		deadline := p.UpdateTime
		p.Stock[economy.Energy] = max(p.Capacity[economy.Energy], 7000)
		step()
		if p.UpdateTime != deadline {
			passes++
		}
	}
	if !mohos[0].Activated || !mohos[1].Activated {
		t.Fatalf("at full storage %v of %v a Moho stayed off: %v", p.Stock[economy.Energy], p.Capacity[economy.Energy], states())
	}

	// Running free.
	capacity := p.Capacity[economy.Energy]
	lowest, metal := p.Stock[economy.Energy], p.TotalProduced[economy.Metal]
	before := states()
	switches := 0
	for pass := 0; pass < 120; {
		deadline := p.UpdateTime
		step()
		lowest = min(lowest, p.Stock[economy.Energy])
		now := states()
		settled := p.UpdateTime != deadline
		for i := range now {
			if now[i] == before[i] {
				continue
			}
			if !settled {
				t.Fatalf("maker %d switched outside a settled pass at tick %d", i, s.Clock.GlobalTick)
			}
			switches++
		}
		for m := range mohos {
			if before[m] && !now[m] {
				for i, on := range now[len(mohos):] {
					if on {
						t.Fatalf("Moho %d went off before small maker %d: %v", m, i, now)
					}
				}
			}
		}
		before = now
		if settled {
			pass++
		}
	}
	drain := float32(1840)
	if lowest < capacity*2/5-drain || lowest <= 0 {
		t.Fatalf("storage fell to %v of %v", lowest, capacity)
	}
	if switches == 0 || p.TotalProduced[economy.Metal] <= metal {
		t.Fatalf("free run: %d switches, metal %v -> %v", switches, metal, p.TotalProduced[economy.Metal])
	}
	t.Logf("capacity %v, lowest %v, %d switches, metal made %v", capacity, lowest, switches, p.TotalProduced[economy.Metal]-metal)
}
