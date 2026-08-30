package session

import (
	"fmt"
	"sort"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/units"
)

// initializeBattleAI constructs the session-owned manager state before any
// battle unit is allocated. InitializeRandomState is deliberately the first
// manager operation which consumes the shared simulation stream: it takes the
// established eight draws for each AI-owning slot in ascending player order
// [08 R-ENTRY-01 §3 step 24][08 R-AI-01 §9].
func initializeBattleAI(s *Session, player uint8, profile *ai.Profile) error {
	if s == nil || s.World == nil || s.Catalog == nil || s.Econ == nil || profile == nil {
		return fmt.Errorf("session: incomplete AI battle binding for player %d", player)
	}
	if int(player) >= len(s.AI) || int(player) >= len(s.Econ.Players) {
		return fmt.Errorf("session: AI player %d out of range", player)
	}
	mgr := &ai.Manager{
		Player:       player,
		Profile:      profile,
		RNG:          s.SimRNG(),
		Terrain:      s.World,
		Catalog:      s.Catalog,
		SurfaceMetal: battleSurfaceMetal(s),
	}
	// Bind before Strategic.Init so the construction-time class vectors and
	// every later gated refresh use the same live battle inputs. At battle
	// entry WindScalar is still exactly zero; the first wind chain runs at tick
	// one and consumes its established draws there [05 R-PROD-01 §1][08
	// R-ENTRY-01 §3 step 19][08 R-P0-05 §5–§6].
	mgr.Strategic.BindEnergyEnvironment(func() (windScalar, tidalStrength float32) {
		return s.Econ.WindScalar(), s.Econ.TidalScalar()
	})
	if !mgr.Strategic.InitializeRandomState(s.SimRNG()) {
		return fmt.Errorf("session: AI strategic state initialization failed for player %d", player)
	}
	allTypes := make([]string, 0, len(s.Catalog.Units))
	for key := range s.Catalog.Units {
		allTypes = append(allTypes, key)
	}
	sort.Strings(allTypes)
	mgr.SetCatalog(s.Catalog)
	mgr.Strategic.Init(allTypes)
	mgr.IsAlliance = func(a, b uint8) bool {
		if int(a) >= len(s.Econ.Players) || int(b) >= len(s.Econ.Players) {
			return false
		}
		pa, pb := &s.Econ.Players[a], &s.Econ.Players[b]
		return pa.Exists && pb.Exists && !pa.IsObserver && !pb.IsObserver && pa.Allies[b]
	}
	if !mgr.InitializeBattleState(s.World, ai.RallyBattleBindings{
		Visible: func(viewer uint8, target *units.Unit) bool {
			if int(viewer) >= len(s.Econ.Players) || !s.Econ.Players[viewer].Exists || s.Econ.Players[viewer].IsObserver {
				return false
			}
			return s.IsUnitVisible(int(viewer), target)
		},
		// Unknown: bind ProbeKnown and OrderAdmitted only after the
		// session option bit selecting explored/current knowledge and the
		// locomotion-object/admission identity are traced; those two writer/reader
		// pairs are the deciders [08 R-AI-01 §7, §17]. Both nil values must
		// remain fail-closed.
		ProbeKnown:    nil,
		OrderAdmitted: nil,
	}) {
		return fmt.Errorf("session: AI battle state initialization failed for player %d", player)
	}
	bindAIQueue(mgr, s)
	s.AI[player] = mgr
	return nil
}

// battleSurfaceMetal is the selected mission/session SurfaceMetal word used by
// the AI selector and limit arithmetic. It remains the authored signed value;
// only the distinct per-cell canonical metal seed narrows through a byte
// [08 R-AI-03 §4][05 R-PROD-01 §6].
func battleSurfaceMetal(s *Session) int32 {
	if s == nil || s.Mission == nil || s.Mission.OTA == nil || s.Mission.OTA.Global == nil {
		return 0
	}
	return mission.DecodeMissionGlobals(s.Mission.OTA.Global).SurfaceMetal
}

// finishBattleEntry performs the tail owned solely by [08 R-ENTRY-01 §8]:
// one tick-zero player-phase prime, the second starting-resource grant which
// overwrites live stocks, then one row-major metal-vector snapshot per manager.
// The one-shot latch also protects direct/fixture composition seams from
// silently double-priming or rebuilding the vector.
func finishBattleEntry(s *Session, overwriteResources func() error) error {
	if s == nil || s.Econ == nil {
		return fmt.Errorf("session: missing economy for battle-entry tail")
	}
	if s.battleEntryTailDone {
		return nil
	}
	s.stepPlayerPhase(0)
	if overwriteResources != nil {
		if err := overwriteResources(); err != nil {
			return err
		}
	}
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.Strategic.InitializeMetalSpots(s.World)
		}
	}
	s.battleEntryTailDone = true
	return nil
}

func clearLiveResourceStocks(s *Session) {
	if s == nil || s.Econ == nil {
		return
	}
	for i := range s.Econ.Players {
		s.Econ.Players[i].Stock[economy.Metal] = 0
		s.Econ.Players[i].Stock[economy.Energy] = 0
	}
}

// overwriteCampaignResources implements the surviving second grant at the
// campaign battle-entry boundary. Retail overwrites only live stocks after the
// tick-zero settlement; ledgers and history fields remain untouched
// [08 R-ENTRY-01 §8 step 5].
func overwriteCampaignResources(s *Session, m *mission.Mission) error {
	clearLiveResourceStocks(s)
	return grantResourcesStrict(s, m)
}
