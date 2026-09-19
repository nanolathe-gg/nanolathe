package session

import (
	"fmt"
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// initializeBattleAI constructs the session-owned manager state before any
// battle unit is allocated. InitializeRandomState is deliberately the first
// manager operation which consumes the shared simulation stream: it takes the
// established eight draws for each AI-owning slot in ascending player order
// [08 R-ENTRY-01 §3 step 24][08 R-AI-01 §9].
func initializeBattleAI(s *Session, player uint8, profile *ai.Profile, sessionKind int) error {
	if s == nil || s.World == nil || s.Catalog == nil || s.Econ == nil || profile == nil {
		return fmt.Errorf("session: incomplete AI battle binding for player %d", player)
	}
	if int(player) >= len(s.AI) || int(player) >= len(s.Econ.Players) {
		return fmt.Errorf("session: AI player %d out of range", player)
	}
	surfaceMetal, err := battleSurfaceMetal(s)
	if err != nil {
		return err
	}
	mgr := &ai.Manager{
		Player:          player,
		Profile:         profile,
		RNG:             s.SimRNG(),
		Terrain:         s.World,
		Catalog:         s.Catalog,
		SurfaceMetal:    surfaceMetal,
		MissionGateFlag: int32(sessionKind),
		// The bound rule set's think step, taken here because a manager may
		// be constructed after the set was bound — this path serves a
		// restored battle as well as a fresh one. Session.BindRules projects
		// onto the managers that already exist, so the two directions agree
		// and a rebind is idempotent. An unbound session leaves this nil,
		// which the manager reads as the retail step
		// (docs/DESIGN_GAMEPLAY_RULES.md "The computer player's think step").
		Planner: s.Rules.Planner,
	}
	// Bind before Strategic.Init so the construction-time class vectors and
	// every later gated refresh use the same live battle inputs. At battle
	// entry WindScalar is still exactly zero; the first wind chain runs at tick
	// one and consumes its established draws there [05 R-PROD-01 §1][08
	// R-ENTRY-01 §3 step 19][08 R-P0-05 §5–§6].
	mgr.Strategic.BindEnergyEnvironment(func() (windScalar, tidalStrength float32) {
		return s.Econ.WindScalar(), s.Econ.TidalScalar()
	})
	// The session's per-player unit limit is the only global the class
	// routine's half-capacity comparison reads, and it is one word for the
	// whole battle [08 R-AI-01 §13]. It binds before Strategic.Init, whose
	// construction-time class computation already consults it.
	mgr.SetUnitLimit(sessionUnitLimit(s))
	// The map's maximum wind word is the second operand of the class routine's
	// wind-generator zeroing branch [08 R-P0-05 §9]. World load already resolved
	// it through [03 §2.2] C3 — a legacy header's own word, or the authored
	// `maxwindspeed` over the canonical 2000 fallback [05 R-PROD-01 §3] — so
	// this binds the resolved battle word rather than re-deriving one. Like the
	// unit limit it binds before Strategic.Init, whose construction-time class
	// computation already consults it.
	mgr.Strategic.SetMaxWind(s.World.WindMax)
	// The plan gate compares each profile directive's arguments against the
	// battle's difficulty word [08 R-AI-01 §12]. One profile record is shared
	// by every slot, so this settles on the first slot and the rest are no-ops;
	// a word outside the vocabulary leaves the profile's own fallback in place
	// rather than inventing one.
	if difficulty, ok := sessionAIDifficulty(s); ok {
		profile.SetDifficulty(difficulty)
	}
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
		ProbeKnown: rallyProbeKnowledge(s),
		// [08 R-AI-01 §19]: the rally task's member gate for a unit with no
		// mover is the slot-1 shot-time PHYSICAL gate of [06 §3.3] from the
		// member's own position to the rally point — range², the shooter-side
		// sea-level clause, and a ballistic solution when the weapon is
		// ballistic. It is not an order-admission predicate. Binding the combat
		// service's own gate keeps the planner from carrying a second copy.
		ShotTimeAdmits: func(unit *units.Unit, x, y, z numeric.Fixed) bool {
			if s.Combat == nil {
				return false
			}
			return s.Combat.ShotTimeAdmitsPoint(unit, 0, x, y, z, s.World)
		},
	}) {
		return fmt.Errorf("session: AI battle state initialization failed for player %d", player)
	}
	bindAIQueue(mgr, s)
	s.AI[player] = mgr
	return nil
}

// rallyProbeKnowledge binds the rally task to the visibility mode's LineOfSight
// bit through the service's one-point predicate. With LOS enabled that predicate
// samples the AI owner's current-sight byte grid; with Permanent LOS it samples
// the mapping word at the local viewing slot's bit. Its projection performs the
// signed high-half height shear and rejects out-of-bounds cells [08 R-AI-01 §7]
// [03 R-VIS-01 §1].
func rallyProbeKnowledge(s *Session) func(owner uint8, x, y, z numeric.Fixed) bool {
	return func(owner uint8, x, y, z numeric.Fixed) bool {
		if s == nil || s.Vis == nil {
			return false
		}
		return s.Vis.VisiblePoint(visibility.PlayerID(owner), x, y, z)
	}
}

// battleSurfaceMetal is the selected mission/session SurfaceMetal word used by
// the AI selector and limit arithmetic. It remains the authored signed value;
// only the distinct per-cell canonical metal seed narrows through a byte
// [08 R-AI-03 §4][05 R-PROD-01 §6].
//
// It used to read the OTA's [GlobalHeader] section through
// mission.DecodeMissionGlobals. That was the wrong source: SurfaceMetal is
// authored per schema. Across the reference install's 275 map .ota files the
// key occurs 635 times and every occurrence sits inside a [Schema N] section,
// none in [GlobalHeader], so the global read returned its accessor default of
// zero for every map in the corpus [08 R-AI-03 §4-A]. The terrain seeds its
// per-cell metal byte from the selected schema's word instead
// (applySchemaStrict then world.Terrain.ApplySchema), so the two readers of
// one quantity disagreed by construction: the AI's scatter acceptance limit,
// surfaceMetal * footZ * footX * 2, was zero while every trial footprint's
// metal-byte sum was positive, and the helper rejected every geometrically
// valid non-extractor site for the whole battle. That is Nanolathe defect
// PT3-14: a computer player that placed nothing but metal extractors, because
// those take the exhaustive helper, which has no limit test.
//
// Resolution order: the selected schema by name; then the map's only schema
// when it authors exactly one, which is unambiguous because it is the only
// schema the terrain could have been seeded from; then an authored
// [GlobalHeader] word, kept for a mission file that does author one there,
// but only when the key is actually present. A miss is a diagnostic error
// rather than a zero — a silent zero is precisely what hid this defect, and
// the caller refuses to build a manager whose limit disagrees with the
// terrain. An authored zero is not a miss: a metal-free map seeds zero bytes
// too, and limit and sum stay consistent at zero.
func battleSurfaceMetal(s *Session) (int32, error) {
	if s == nil || s.Mission == nil {
		return 0, fmt.Errorf("nanolathe: AI surface-metal binding failed: logical path <session mission record>, providers searched [], expected the loaded mission record")
	}
	schemaName := s.Mission.Schema.Name
	var header *content.MapHeader
	if s.Catalog != nil {
		header = s.Catalog.Maps[content.CanonicalKey(s.Mission.TerrainKey)]
	}
	if header != nil {
		for i := range header.Schemas {
			if header.Schemas[i].Name == schemaName {
				return header.Schemas[i].SurfaceMetal, nil
			}
		}
		if len(header.Schemas) == 1 {
			return header.Schemas[0].SurfaceMetal, nil
		}
	}
	if s.Mission.OTA != nil && s.Mission.OTA.Global != nil {
		if value, found, err := s.Mission.OTA.Global.Int("SurfaceMetal"); err == nil && found {
			return int32(value), nil
		}
	}
	return 0, fmt.Errorf("nanolathe: AI surface-metal binding failed: logical path %s, providers searched [map schema %q, OTA GlobalHeader], expected the selected schema's SurfaceMetal word [08 R-AI-03 §4-A]", s.Mission.TerrainKey, schemaName)
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
	s.clearWatcherVisibilityMasks()
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

// clearWatcherVisibilityMasks applies the world-rebuild tail's watcher clear
// once, after initial unit construction [07 R-CAM-01 §14]. Later commands may
// change these same live bits.
//
// Clearing mode bits 0 and 1 is Mapped + Permanent, and retail does not leave
// the stores as they were: it "forces one bulk rebuild", which refills the word
// grid all-ones and every eligible slot's byte grid with 1 so the watcher sees
// the unmasked map [03 R-VIS-01 §4] pass 1, [03 R-VIS-01 §1], [08 R-SKIR-01 §3].
// The rebuild is the entry rebuild called with the full argument, not the live
// chat-command refresh of [07 R-CAM-01 §6]: watch-mode entry is named in
// [08 R-ENTRY-01 §7] as one of that call's three sites.
func (s *Session) clearWatcherVisibilityMasks() {
	p := s.playerRecord(int(s.LocalOwner))
	if p == nil || !(p.Watcher || p.IsObserver) || s.Vis == nil {
		return
	}
	s.Vis.SetMode(s.Vis.Mode() &^ (visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled))
	rebuildVisibilityForEntry(s)
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
