//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestProTAComputerSiloQueuesOneRound exercises the shipped 4.8 stockpile
// purchasing end to end on the authored package: a completed ARMSILO owned by
// the computer player is classified into the armed-building record, the
// patched resource/queue task selects its one CANBUILD pseudo-product, and the
// ordinary submission helper turns it into a slot-zero BUILDWEAPON round
// (research/extensions/prota-engine.md "AI and economy evidence audit"). The
// package switches come from the content-profile gameplay block, which is how
// prota.json enables them; with them overridden off, the retail null task
// leaves the silo alone.
func TestProTAComputerSiloQueuesOneRound(t *testing.T) {
	fs, cat, limits, profile := loadProTAArchive(t)
	if def, ok := cat.Unit("MAKENUKEARM"); !ok || !stockpileAliasName(def.UnitName) {
		t.Fatalf("ProTA pseudo-product name %v does not carry the stockpile alias", def)
	}
	for _, on := range []bool{false, true} {
		// prota.json enables the switches; the "off" pass overrides them back
		// to the build table's answer.
		sources := CommunitySources{Content: profile.GameplaySources()}
		enabled := on
		sources.Content = append(sources.Content, community.Overrides{
			AIDifficultyIncome: &enabled, AIStockpileProducts: &enabled, TargetLockRelease: &enabled,
			AIApplianceEnergy: &enabled, AIBuilderStopThreshold: &enabled,
		})
		cfg := DirectSkirmishConfig("ashap plateau")
		cfg.ApplyDefaults()
		cfg.Gameplay = gameplay.Community39
		cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
		s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{CommunitySources: sources, ContentLimits: limits})
		if err != nil {
			t.Fatalf("compose ProTA skirmish: %v", err)
		}
		if s.Community.AIStockpileProducts != on || s.AI[1] == nil || s.AI[1].Community.AIStockpileProducts != on {
			t.Fatalf("switches=%v: resolved table %+v", on, s.Community)
		}
		advanceProTATicks(t, s, 2)
		commander := retailUnit(s, 1, "CORCOM")
		if commander == nil {
			t.Fatal("computer commander absent")
		}
		// ProTA's CORSILO script declares a piece its model lacks, so strict COB
		// binding refuses it; ARMSILO is the same producer shape.
		silo := placeCompleteRetailUnit(t, s, "ARMSILO", 1, commander.X.Add(numeric.FixedFromInt(160)), commander.Z)
		roundQueued := func() bool {
			q := orders.QueueOfUnit(silo)
			if q == nil {
				return false
			}
			for _, n := range q.Secondary() {
				if n != nil && n.ID == orders.Lookup("BuildWeapon") && n.Param1 == 0 {
					return true
				}
			}
			return false
		}
		// Classification runs on the manager's 30-entry cadence.
		advanceProTATicks(t, s, 35)
		if silo.Group != 5 {
			t.Fatalf("switches=%v: ARMSILO group %d, want the armed-building record", on, silo.Group)
		}
		// The ordinary reservoir scores the pseudo-product through the class
		// routine's "other" column, which is zero while the economy is short;
		// give the computer player the full, surplus economy a late game has.
		m := s.AI[1]
		p := &s.Econ.Players[1]
		p.Stock, p.Capacity = [2]float32{1000, 1000}, [2]float32{1000, 1000}
		p.AIProduction = [2]float32{1000, 1000}
		p.AIConsumption = [2]float32{}
		m.Deadlines[ai.TaskNull] = 0
		tick := s.Clock.GlobalTick
		m.Tick(tick, s.Units, s.Econ)
		if !on {
			if roundQueued() || m.Deadlines[ai.TaskNull] != 0 {
				t.Fatal("retail null task queued a round")
			}
			continue
		}
		if !roundQueued() || m.Deadlines[ai.TaskNull] != tick+30 {
			t.Fatalf("the computer player did not queue one ARMSILO round (deadline %d)", m.Deadlines[ai.TaskNull])
		}
		q := orders.QueueOfUnit(silo)
		if len(q.Secondary()) != 1 || q.Secondary()[0].Param2 != 1 {
			t.Fatalf("silo secondary queue %+v, want one record of one round", q.Secondary())
		}
		// The queued round is a secondary order, which suppresses the next
		// visit: no second round is requested while it stands.
		m.Tick(tick+30, s.Units, s.Econ)
		if len(q.Secondary()) != 1 || q.Secondary()[0].Param2 != 1 {
			t.Fatalf("a standing round admitted another request: %+v", q.Secondary())
		}
	}
}
