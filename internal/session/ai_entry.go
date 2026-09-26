package session

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

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
		// Every manager is built Classic; a player marked Modern takes the
		// Modern AI step when battle entry or a load applies the marks
		// (applyAIControllers, restoreAIControllers).
		Planner:           s.Rules.Planner,
		ConstructionRules: s.Rules.Construction,
		Community:         s.aiCommunity(),
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
		Visible:    s.computerPlayerSees,
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
	// Public map knowledge and the computer player's own sight, for a Modern
	// controller (internal/aikit). The retail step reads neither.
	if s.Mission != nil {
		for _, sp := range s.Mission.Specials {
			if sp.Kind == 1 {
				mgr.StartPositions = append(mgr.StartPositions, [2]int32{int32(sp.X), int32(sp.Z)})
			}
		}
	}
	// The seed a Modern controller's private generator starts from; a copy of
	// the entry seed, so no stream is advanced [I4 "Modern AI exception"].
	// On a restored battle it is the load's own entry seed until the stage
	// replaces it with the seed the save's sidecar recorded, when the sidecar
	// carries one (RetailLoadDeps.AIControllers); the bank itself carries
	// neither the seed nor the controller
	// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player").
	mgr.BattleSeed = s.RNGSimSeed
	mgr.UnitVisible = s.computerPlayerSeesOwn
	bindAIQueue(mgr, s)
	s.AI[player] = mgr
	return nil
}

// publishStartOwners tells each computer player's manager which slot the
// skirmish placement put on each start position, in StartPositions order.
// A slot owns the first start-position record carrying its assigned stored
// number, as the placement's own lookup resolves it [08 R-ENTRY-01 §5]
// step 3. A manager whose StartPositions do not match the placement's
// records keeps none, so it searches rather than trusting a misaligned
// table. The managers share one read-only slice.
func publishStartOwners(s *Session, starts []mission.Special, eligible []int, assignment map[int]int) {
	owners := make([]int8, len(starts))
	for i := range owners {
		owners[i] = -1
	}
	for _, slot := range eligible {
		perm, ok := assignment[slot]
		if !ok {
			continue
		}
		for i := range starts {
			if int(starts[i].ID) == perm {
				owners[i] = int8(slot)
				break
			}
		}
	}
	for _, mgr := range s.AI {
		if mgr == nil || len(mgr.StartPositions) != len(starts) {
			continue
		}
		aligned := true
		for i := range starts {
			if mgr.StartPositions[i] != [2]int32{int32(starts[i].X), int32(starts[i].Z)} {
				aligned = false
				break
			}
		}
		if aligned {
			mgr.StartOwners = owners
		}
	}
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

// battleAIProfileName is the single resolution of the `aiprofile` name every
// battle-entry path shares: the campaign constructor, the skirmish
// constructor and the save-restore reset all call it so a restored battle
// runs the same profile record the live session did.
//
// `aiprofile` is a `[Schema N]` key, not a `[GlobalHeader]` key, and the
// executable reads it with the chosen schema current [02 R-MAP-01 §5] row 7;
// [08 R-CAMP-01 §2] restates it as a schema-branch read and [08 R-AI-01 §12]
// names the resolution point. Across the reference install's 275 map .ota
// files no [GlobalHeader] authors the key at all, while 608 of the 635
// schemas author a non-empty one, so decoding it from the global section
// returned the accessor default — an empty string — for every stock mission
// and every stock map, and the fallback made every battle run
// `ai/default.txt`. That is the same wrong-source defect class as the
// SurfaceMetal binding above: the campaign maps name `MISSIONS` in their
// Easy and Medium schemas, whose profile constrains the constructor plan and
// bans the strategic weapons, and `DEFAULT` in their Hard schemas,
// and the sea/hover/air skirmish schemas name profiles that reweight whole
// unit families, so the computer player ignored the authored strategy
// everywhere.
//
// Resolution order, matching Mission.StartingResources for the four
// starting-resource words of the same row: the selected schema by name, then
// the map's only schema when the file authors exactly one (both through
// Mission.SchemaSection), then an authored [GlobalHeader] key but only when
// it is actually present, kept for a hand-written mission that does author
// one there. A name that is absent or blank is the established
// `ai\default.txt` fallback, which is also what the loader applies when the
// named profile does not resolve to a file [08 R-AI-01 §12]. The authored
// casing is carried through unchanged; the archive lookup folds case.
func battleAIProfileName(m *mission.Mission) string {
	name := ""
	authored := false
	if sec := m.SchemaSection(); sec != nil {
		name, authored = sec.StringValue("aiprofile", "")
	}
	if !authored && m != nil && m.OTA != nil && m.OTA.Global != nil {
		name, _ = m.OTA.Global.StringValue("aiprofile", "")
	}
	if strings.TrimSpace(name) == "" {
		return "default"
	}
	return name
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

// AIOverrides are the configured parameters of a battle's Modern AI computer
// players (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Configuration"): one layer for every computer player, one per battle
// difficulty and one per player slot. Each layer is canonical text
// (CanonicalAIParams) that the host has already checked against the brain's
// vocabulary; the session knows no key. Battle entry merges the layers once
// per computer player into ai.Manager.ControllerParams and never reads them
// again. Like the mutators they ride in the battle-entry options and are
// fixed for the battle; they change what a Modern controller's brain is
// built with and nothing else, so every set that binds no such controller
// carries them dormant. The zero value configures nothing.
type AIOverrides struct {
	// All applies to every computer player.
	All string
	// Difficulty applies by the difficulty the controller plays at
	// (ControllerDifficulty), indexed 0 easy, 1 medium, 2 hard.
	Difficulty [3]string
	// Players applies to one slot, indexed from 0. The lobby and the
	// settings file name slot i "player i+1".
	Players [SkirmishMaxPlayers]string
}

// IsZero reports a set that configures nothing.
func (o AIOverrides) IsZero() bool { return o == AIOverrides{} }

// For is one computer player's effective parameters: the All layer, then its
// difficulty's, then its slot's, merged key by key, so the most specific
// layer that names a key sets it. The result is canonical.
func (o AIOverrides) For(player uint8, difficulty ai.Difficulty) (string, error) {
	layers := []string{o.All, o.Difficulty[difficultyLayer(difficulty)]}
	if int(player) < len(o.Players) {
		layers = append(layers, o.Players[player])
	}
	var merged []AIParam
	for _, layer := range layers {
		params, err := ParseAIParams(layer)
		if err != nil {
			return "", err
		}
		for _, p := range params {
			merged = setAIParam(merged, p)
		}
	}
	return joinAIParams(merged), nil
}

// difficultyLayer indexes AIOverrides.Difficulty.
func difficultyLayer(d ai.Difficulty) int {
	switch d {
	case ai.DifficultyEasy:
		return 0
	case ai.DifficultyHard:
		return 2
	}
	return 1
}

// ControllerDifficulty is the difficulty a Modern controller plays at: the
// battle's word as the shared AI profile's plan gate holds it after
// initializeBattleAI, with anything but easy or hard read as medium. The
// Modern AI picks its persona by it and battle entry picks the AIOverrides
// difficulty layer by it, so the two always agree.
func ControllerDifficulty(p *ai.Profile) ai.Difficulty {
	if p != nil && (p.Plan == ai.DifficultyEasy || p.Plan == ai.DifficultyHard) {
		return p.Plan
	}
	return ai.DifficultyMedium
}

// AIParam is one key=value pair of a Modern controller's parameters.
type AIParam struct{ Key, Value string }

// ParseAIParams reads "key=value,key=value" text: the canonical form, a
// save's record or a --ai argument. Space around a key or a value is
// dropped, and the empty text is no parameters. A piece without "=", an
// empty key or value, or a key named twice is an error. Whether a key and
// its value mean anything is the brain's to say (mods/aikit
// ValidateParams); this is only the text form.
func ParseAIParams(text string) ([]AIParam, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var out []AIParam
	for _, piece := range strings.Split(text, ",") {
		key, value, ok := strings.Cut(piece, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("session: AI parameter %q: want key=value", strings.TrimSpace(piece))
		}
		for i := range out {
			if out[i].Key == key {
				return nil, fmt.Errorf("session: AI parameter %q is given twice", key)
			}
		}
		out = append(out, AIParam{Key: key, Value: value})
	}
	return out, nil
}

// CanonicalAIParams is the canonical text of a parameter set: its pairs in
// key order, each "key=value", joined by commas; "" when the set is empty.
// Equal sets spell the same text whatever the map's order, which is what a
// manager, a report and a save's record hold. A key or a value that is
// empty, holds a separator or has surrounding space is an error.
func CanonicalAIParams(kv map[string]string) (string, error) {
	params := make([]AIParam, 0, len(kv))
	for _, key := range slices.Sorted(maps.Keys(kv)) {
		value := kv[key]
		if !canonicalAIToken(key) || !canonicalAIToken(value) {
			return "", fmt.Errorf("session: AI parameter %q=%q: want a key and a value without spaces, commas or '='", key, value)
		}
		params = append(params, AIParam{Key: key, Value: value})
	}
	return joinAIParams(params), nil
}

func canonicalAIToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, ",= \t\r\n")
}

// setAIParam sets p.Key to p.Value, replacing an earlier value.
func setAIParam(params []AIParam, p AIParam) []AIParam {
	for i := range params {
		if params[i].Key == p.Key {
			params[i].Value = p.Value
			return params
		}
	}
	return append(params, p)
}

// joinAIParams spells params canonically; it sorts them in place.
func joinAIParams(params []AIParam) string {
	sort.Slice(params, func(i, j int) bool { return params[i].Key < params[j].Key })
	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.Key)
		b.WriteByte('=')
		b.WriteString(p.Value)
	}
	return b.String()
}

// applyAIOverrides gives every computer player's manager its effective
// Modern AI parameters (AIOverrides.For). A fresh battle entry calls it after
// initializeBattleAI has set the shared profile's difficulty and before the
// battle-entry prime can build a controller. A restored battle takes its
// save's record instead (restoreAIControllers).
func applyAIOverrides(s *Session, o AIOverrides) error {
	for player, mgr := range s.AI {
		if mgr == nil {
			continue
		}
		params, err := o.For(uint8(player), ControllerDifficulty(mgr.Profile))
		if err != nil {
			return fmt.Errorf("nanolathe: Modern AI parameters rejected: logical path player %d, providers searched [battle-entry options], expected canonical key=value text: %w", player+1, err)
		}
		mgr.ControllerParams = params
	}
	return nil
}
