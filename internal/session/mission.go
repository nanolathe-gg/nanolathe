package session

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// sessionKindCampaign and sessionKindSkirmish are the session-kind words the
// pool's player-slice comparator switches on [08 R-SESS-01 §7]: kind 1
// (campaign) and kind 2 (skirmish) both order by slot; only kind 3
// (multiplayer, never built by this engine) consults the peer-identity sort
// key. They are a distinct concept from mission.Type — that discriminant
// selects how a mission *file* is loaded (campaign wrapper vs direct OTA,
// [08 "Mission type dispatch"]) and must never stand in for the session kind
// here, even though two of its three values happen to coincide.
const (
	sessionKindCampaign = 1
	sessionKindSkirmish = 2
)

// NewMission loads a campaign mission by VFS logical path and difficulty per
// [08 "Mission type dispatch"], [08 "Schema choice"] and prepares the battle
// session. It is the plan API entry point [PLAN_14 Public API] C3 and is
// retained as the canonical constructor that later phases compile against.
// The VFS and catalog are taken from the default process state when nil; tests
// should call NewMissionWithFS for injection.
func NewMission(path string, difficulty int) (*Session, error) {
	return NewMissionWithFS(nil, nil, path, difficulty)
}

// NewMissionWithFS is the strict production constructor. It never fabricates
// nil terrain, empty catalog, or missing service. [02 §5][03 §2.2][P0-16]
func NewMissionWithFS(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int) (*Session, error) {
	return NewMissionWithProgress(fs, cat, path, difficulty, nil)
}

// NewMissionWithProgress is NewMissionWithFS with a load observer, reporting
// the same families the skirmish constructor does so one loading screen can be
// driven from either entry point. A nil observer makes this exactly
// NewMissionWithFS.
func NewMissionWithProgress(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int, report content.Progress) (*Session, error) {
	return NewMissionWithProgressSeeds(fs, cat, path, difficulty, 0, 0, report)
}

// NewMissionWithProgressSeeds is the explicit battle-entry constructor used by
// the composition layer. The pair is installed before wind, placement, COB,
// or AI setup can draw from either stream [01 §7.1][01 §7.2][R-CORE-02].
func NewMissionWithProgressSeeds(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int, simSeed, crtSeed uint32, report content.Progress) (*Session, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("session: empty mission path")
	}
	if fs == nil {
		fs = vfs.New()
	}
	cat, err := strictCatalogWithProgress(fs, cat, report)
	if err != nil {
		return nil, err
	}
	var m *mission.Mission
	if strings.Contains(path, ":") {
		campaignPath, idx, parseErr := parseCampaignMissionSelector(path)
		if parseErr != nil {
			return nil, parseErr
		}
		m, err = mission.LoadCampaignWithSink(fs, campaignPath, idx, difficulty, 0, nil)
		if err != nil {
			return nil, err
		}
	} else {
		m, err = mission.LoadWithType(fs, mission.TypeCampaign, path, difficulty, 0, nil)
		if err != nil {
			// Type 1 is the strict campaign path. Types 2 and 3 are the only
			// mission kinds that use the direct OTA fuzzy resolver; falling back
			// here would turn a campaign load failure into a different mission
			// type and violate the retail dispatch contract [08 "Mission type dispatch"].
			return nil, err
		}
	}
	// The unit-restriction loader of battle entry, kind 1 only
	// [08 R-ENTRY-01 §2 step 4]. Mission.UseOnlyPath already carries the
	// resolved logical path (resource slot 6). When the file exists, the
	// definitions it does not name are removed from the catalog, and the
	// survivors are re-sorted and renumbered — retail clears a per-definition
	// creatable bit and the catalog compile that follows in the same entry
	// compacts the cleared records out, so a kind-1 restriction manifests as
	// catalog removal, not as an allocator refusal [05 R-SHARE-01 §8]. A
	// missing file leaves every definition creatable.
	//
	// The restriction lasts one battle — retail rebuilds the table from the
	// FBI files before every battle — so it is applied to a clone, never to
	// the shared compiled catalog the caller handed in.
	//
	// The pool's record count is read before the restriction is applied.
	// Retail allocates the per-player slice from the session's unit limit,
	// never from the definition count [05 R-SHARE-01 §7], so a mission whose
	// restriction file names a dozen units must not end up with a dozen unit
	// records per slot. Now that the count comes from the limit the ordering
	// no longer matters, but the read stays ahead of the filter so the two can
	// never be re-coupled by accident.
	//
	// A campaign's limit is the OTA `maxunits` the mission loader decoded,
	// whose missing-value default is 200; it is the one mode whose OTA value
	// survives battle entry [08 R-SKIR-01 §6].
	poolRecords := int(campaignUnitLimit(m))
	cat, err = applyUseOnlyRestriction(fs, cat, m.UseOnlyPath)
	if err != nil {
		return nil, err
	}
	terrain, err := loadTerrainStrict(fs, cat, m)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyTerrain, 100)
	// The pool's player-slice order only ever consults the peer-identity sort
	// key in session kind 3 (multiplayer); kinds 1 (campaign) and 2 (skirmish)
	// always order by slot regardless of what that word holds [08 R-SESS-01
	// §7]. This constructor only ever builds a campaign session, so it passes
	// the campaign kind explicitly rather than deriving it from mission.Type
	// — a different discriminant (file-loading dispatch, not session kind)
	// that happens to share this one value. Nanolathe never builds a kind-3
	// session, so no sort-key plumbing is needed here at all [08 R-SESS-01
	// §7 "Consequence for single-player"].
	unitsWorld, err := newBattleSlicedWorldWithCOBSized(cat, fs, sessionKindCampaign, [pool.PlayerCount]uint32{}, poolRecords)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyUnitWorld, 100)
	s := &Session{
		Catalog:      cat,
		World:        terrain,
		Mission:      m,
		Clock:        &clock.State{Requested: 10, Active: 10},
		Snapshot:     frame.NewBuffer(),
		Units:        unitsWorld,
		Econ:         &economy.Service{},
		Latch:        NewEndLatch(),
		CampaignSlot: m.CampaignIndex,
	}
	// Correct controller states: human local 1, computer enemy 2 [08 "Established AI-facing data"]
	for i := 0; i < 2 && i < 10; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		if i == 0 {
			p.ControllerState = 1
		} else {
			p.ControllerState = 2
		}
		p.IsObserver = false
		p.GameEnded = false
		p.EndGameCountdown = -1
		p.Allies[i] = true
	}
	// The two campaign player rows exist now, so the panel's side bytes can be
	// stamped onto them before any trigger consumer runs.
	applyCampaignPlayerTableSides(s, fs, m)
	// UpdateTime/WinLoseTime/DisplayTimer seeded to GlobalTick per [05] C5.
	// UpdateTime is the one deadline the end-condition poll rides; WinLoseTime
	// is seeded here and persisted, and no gameplay site reads or advances it
	// [08 R-TRIG-01 §6][08 "Player records"].
	s.Econ.SeedDeadlines(0)
	// DET-01 [R-CORE-02]: battle bootstrap seeds both streams fresh before any
	// battle setup draw; battle-entry wind zeroes the deadline with NO draws
	// and the meteor initial next-strike is written (no draws) [R-CORE-01
	// §4.4.1]. Production passes the explicit pair selected at this boundary;
	// the zero values here are the direct constructor's explicit zero-seed input.
	s.SeedSessionRNG(simSeed, crtSeed)
	s.InitBattleWindForSession()
	s.initMeteor()
	s.InitAudio(fs)
	if err := createAndBindServices(s); err != nil {
		return nil, err
	}
	// Manager records and their eight-draw strategic constructors precede every
	// commander/mission unit allocation [08 R-ENTRY-01 §3 step 24].
	for i := range s.AI {
		s.AI[i] = nil
	}
	mgAI := mission.DecodeMissionGlobals(m.OTA.Global)
	aiProfileName := mgAI.AIProfile
	if strings.TrimSpace(aiProfileName) == "" {
		aiProfileName = "default"
	}
	sharedProf, perr := loadCampaignAIProfile(fs, aiProfileName)
	if perr != nil {
		return nil, perr
	}
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		if !p.Exists || p.IsObserver || (p.ControllerState != 1 && p.ControllerState != 2) {
			continue
		}
		if err := initializeBattleAI(s, uint8(i), sharedProf); err != nil {
			return nil, err
		}
	}
	if err := battleEntryPlacement(s, m); err != nil {
		return nil, err
	}
	report.Report(FamilyPlacement, 100)
	if err := ensureCOBForAll(s, fs); err != nil {
		return nil, err
	}
	report.Report(FamilyScripts, 100)
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	s.RegisterAll()
	if err := finishBattleEntry(s, func() error {
		if err := overwriteCampaignResources(s, m); err != nil {
			return err
		}
		s.InitShareThresholds()
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.SelectForGametype(GametypeCampaign); err != nil {
		return nil, err
	}
	if err := s.ValidateComposition(); err != nil {
		return nil, fmt.Errorf("session: composition invalid: %w", err)
	}
	return s, nil
}

// applyCampaignPlayerTableSides stamps the campaign player table's per-slot
// side ordinal. That ordinal is the whole of the commander identity every
// commander-driven trigger resolves through: a unit is a commander when its
// definition name equals, case-insensitively, the commander name of the
// *unit's own owner's* side in the side-data table — not the local side's and
// not the definition's own flag [08 R-TRIG-01 §3].
//
// The writer is the single-player new-game panel. Choosing Arm sets the local
// side index to 0 and the two campaign player slots' side bytes to (0, 1);
// choosing Core sets 1 and (1, 0) [08 R-CAMP-01 §3]. Those two rows are the
// whole table: every trigger owner test compares against slot 0 or slot 1 and
// no other slot is visible to one [08 R-TRIG-01 §3].
//
// Which of the two rows a battle runs under is settled before the campaign
// list is ever filtered, and a campaign whose `[HEADER] campaignside` names a
// side is offered to that side alone — so for a named side that name is the
// side the mission is played as [08 R-CAMP-01 §1 "campaignside filters, it
// does not assign"]. The same section records that the literal `ALL` is
// admitted to both lists and settles nothing, so it stays unknown here and the
// identity fails closed rather than defaulting.
// applyUseOnlyRestriction is battle entry's unit-restriction loader for kind 1
// [08 R-ENTRY-01 §2 step 4]. It returns the catalog the battle runs on: the
// input unchanged when no restriction file is present, or a restricted clone
// when one is. The clone matters — retail rebuilds the definition table from
// the FBI files before every battle, so the removal lasts exactly one battle
// and must not reach a catalog the caller shares [05 R-SHARE-01 §8].
func applyUseOnlyRestriction(fs vfs.FSOps, cat *content.Catalog, useOnlyPath string) (*content.Catalog, error) {
	if cat == nil {
		return cat, nil
	}
	names, present, err := mission.LoadUseOnlyNames(fs, useOnlyPath)
	if err != nil {
		return nil, err
	}
	if !present {
		return cat, nil
	}
	restricted := cat.Clone()
	if err := restricted.RestrictToCreatable(names); err != nil {
		return nil, fmt.Errorf("nanolathe: unit restriction failed: logical path %s, providers searched [vfs], expected a compacted unit catalog: %w", useOnlyPath, err)
	}
	return restricted, nil
}

func applyCampaignPlayerTableSides(s *Session, fs vfs.FSOps, m *mission.Mission) {
	if s == nil || fs == nil || m == nil || s.Catalog == nil {
		return
	}
	if m.Type != mission.TypeCampaign || m.CampaignPath == "" {
		// A type-1 mission reached by a bare OTA path carries no campaign file
		// and therefore no authored side name. The front end's local-side value
		// is the only other source [08 R-CAMP-01 §1] and no constructor seam
		// carries it, so the identity stays unknown.
		return
	}
	campaign, err := mission.DiscoverCampaign(fs, m.CampaignPath)
	if err != nil || campaign == nil || campaign.Document == nil || campaign.Document.Root == nil {
		return
	}
	header := campaign.Document.Root.Section("HEADER")
	if header == nil {
		// A campaign file without a `[HEADER]` block is skipped by the
		// enumeration that offers it, so it names no side [08 R-CAMP-01 §1].
		return
	}
	name, ok := header.StringValue("campaignside", "")
	name = strings.TrimSpace(name)
	if !ok || name == "" || strings.EqualFold(name, "ALL") {
		// This is not an open research question: [08 R-CAMP-01 §1] establishes
		// that `campaignside` only filters which campaigns the new-game panel
		// offers, and that `ALL` is admitted to both lists and settles
		// nothing — "a real gap, not a defaulting opportunity". The side is
		// actually decided earlier, by the front-end local-side value written
		// when the new-game panel opens, and no constructor seam here carries
		// that value into the session. So for `ALL` (and for the absent/empty
		// name case, which authors no side at all) this function leaves the
		// identity unknown: campaignPlayerSideKnown stays false and every
		// commander/trigger owner test that depends on it fails closed rather
		// than guessing a side.
		return
	}
	// The admission test compares `campaignside` case-insensitively against the
	// side's name, so the side-data table's ordinal for that name is the local
	// side index [08 R-CAMP-01 §1], [02 §6].
	local := -1
	for i, side := range s.Catalog.Sides {
		if side != nil && strings.EqualFold(strings.TrimSpace(side.Name), name) {
			local = i
			break
		}
	}
	switch local {
	case 0:
		s.campaignPlayerSide[0], s.campaignPlayerSide[1] = 0, 1
	case 1:
		s.campaignPlayerSide[0], s.campaignPlayerSide[1] = 1, 0
	default:
		// The panel writes exactly the two pairs above; an unresolved name, or
		// one resolving to a further side-table ordinal, has no authored row
		// [08 R-CAMP-01 §3].
		return
	}
	s.campaignPlayerSideKnown[0], s.campaignPlayerSideKnown[1] = true, true
}

// parseCampaignMissionSelector accepts only the explicit composition identity
// "campaign-path:MISSION<decimal-index>". A malformed explicit selector must
// never fall through to campaign slot zero [08 R-ENTRY-01 §3].
func parseCampaignMissionSelector(selector string) (string, int, error) {
	selector = strings.TrimSpace(selector)
	if strings.Count(selector, ":") != 1 {
		return "", 0, fmt.Errorf("session: malformed campaign mission selector %q: expected <campaign>:MISSION<index>", selector)
	}
	parts := strings.SplitN(selector, ":", 2)
	campaignPath := strings.TrimSpace(parts[0])
	missionPart := strings.TrimSpace(parts[1])
	if campaignPath == "" || len(missionPart) <= len("mission") || !strings.EqualFold(missionPart[:len("mission")], "mission") {
		return "", 0, fmt.Errorf("session: malformed campaign mission selector %q: expected <campaign>:MISSION<index>", selector)
	}
	digits := missionPart[len("mission"):]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", 0, fmt.Errorf("session: malformed campaign mission selector %q: mission index must be unsigned decimal", selector)
		}
	}
	idx, err := strconv.Atoi(digits)
	if err != nil {
		return "", 0, fmt.Errorf("session: malformed campaign mission selector %q: mission index: %w", selector, err)
	}
	return campaignPath, idx, nil
}

// loadCampaignAIProfile requires the authored mission profile (with the
// established ai/default.txt fallback implemented by ai.LoadProfile). A
// computer-controlled campaign slot without a profile cannot run the rooted
// planner, so construction must fail rather than silently creating a passive
// computer player [08 "Established AI-facing data and rooted planner"].
func loadCampaignAIProfile(fs vfs.FSOps, name string) (*ai.Profile, error) {
	prof, err := ai.LoadProfile(fs, name)
	if err != nil {
		return nil, fmt.Errorf("session: ai profile %q: %w", name, err)
	}
	if prof == nil {
		return nil, fmt.Errorf("session: ai profile %q: nil profile", name)
	}
	return prof, nil
}

// RouteForGametype selects the initial state for a save based on gametype via
// the existing state-machine helpers per [08 "Session states"] C3. Gametype 1
// (campaign) selects StateLocalPreload (4) which then takes the same StateLoading
// (5) path; gametype 2 selects StateLoading (5) directly.
func RouteForGametype(s *Session, gametype int) error {
	if s == nil {
		return fmt.Errorf("session: nil session")
	}
	return s.SelectForGametype(gametype)
}

// BattleEntry performs the single-player battle-entry order per [08
// "Placement and battle entry"] C9: place features → reconstruct units →
// grant starting resources DIRECTLY to live stock outside the ledger
// (economy.CreditSpawn, [05 "Authoritative settlement order"]).
func BattleEntry(s *Session, m *mission.Mission) error {
	if err := battleEntryPlacement(s, m); err != nil {
		return err
	}
	if err := grantResourcesStrict(s, m); err != nil {
		return err
	}
	// Initialize sharing thresholds once from rebuilt capacity after units exist [P1-06] [P1-I04].
	s.InitShareThresholds()
	return nil
}

// battleEntryPlacement is the campaign placement half used by the production
// constructor before the tick-zero prime and second resource grant. BattleEntry
// retains its existing direct-call resource behavior for fixtures [08
// R-ENTRY-01 §6, §8].
func battleEntryPlacement(s *Session, m *mission.Mission) error {
	if s == nil {
		return fmt.Errorf("session: nil session")
	}
	if m == nil {
		return fmt.Errorf("session: nil mission")
	}
	if s.Catalog == nil {
		return fmt.Errorf("session: missing Catalog for mission battle entry [02 §5]")
	}
	if s.World == nil {
		return fmt.Errorf("session: missing World for mission battle entry [03 §2.2]")
	}
	if s.Features == nil {
		return fmt.Errorf("session: missing Features for mission battle entry [05]")
	}
	if s.Units == nil {
		return fmt.Errorf("session: missing Units for mission battle entry [01 §6.1]")
	}
	if s.Econ == nil {
		return fmt.Errorf("session: missing Economy for mission battle entry [05]")
	}
	if err := placeFeatures(s, m); err != nil {
		return err
	}
	// The deposit pass runs after every feature stamp, mission-placed ones
	// included [05 R-FEAT-01 §7].
	s.World.SeedFeatureMetalDeposits()
	if err := reconstructUnits(s, m); err != nil {
		return err
	}
	// Initialize COB before InitialMission [04 §4.1] – each unit's VM must exist before script runs.
	if err := requireCOBForSession(s); err != nil {
		return err
	}
	// InitialMission runs ONCE on the loading worker after ALL mission units
	// exist [04 §3.6][08 R-ENTRY-01 §6] — here, immediately after unit
	// placement and before resources are granted.
	// It queues orders; from the next tick the ordinary pump consumes them.
	mission.RunInitialMissionsWithCatalog(m, s.Units, s.Catalog)
	// InitialMission lazily creates order queues after service composition. Bind
	// every queue that it actually created before any later load step can pump
	// or resolve it; this is the same concrete context retained by construction
	// for product queues and queue replacement [04 §3.3][04 §3.5][06 §11.1].
	s.bindExistingOrderQueues()
	// Wire cargo/transport from i-verb immediate attach [04 §3.6] P0-04.
	wireMissionCargo(s, m)
	return nil
}

func placeFeatures(s *Session, m *mission.Mission) error {
	// Terrain-provided and mission-provided feature records converge on the same
	// feature stamping service; deterministic load order matters [08 "Placement
	// and battle entry"]. No RNG draws occur here [I4].
	if s.World == nil || s.Features == nil {
		return nil
	}
	if m == nil || len(m.Features) == 0 {
		return nil
	}
	// Deterministic source order: terrain features already stamped via world.Load's
	// ExpandPlot + stampFeatureAnchors into Plot; we now stamp mission-authored
	// features in decode order (not map iteration) [08 "Placement and battle entry"] [I1][04 §6.2].
	for _, fp := range m.Features {
		if !fp.IsPlaced() {
			continue
		}
		name := fp.Name
		if name == "" {
			continue
		}
		var def *content.FeatureDef
		if s.Catalog != nil && s.Catalog.Features != nil {
			def = s.Catalog.Features[content.CanonicalKey(name)]
		}
		if def == nil {
			continue
		}
		cx, cz := int(fp.X), int(fp.Z)
		if cx < 0 || cz < 0 || cx >= int(s.World.CellW) || cz >= int(s.World.CellH) {
			continue
		}
		s.Features.PlaceAt(cx, cz, def)
	}
	return nil
}

func reconstructUnits(s *Session, m *mission.Mission) error {
	if s.Units == nil {
		return fmt.Errorf("session: missing Units for mission battle entry [01 §6.1]")
	}
	if s.Catalog == nil {
		return fmt.Errorf("session: missing Catalog for mission battle entry [02 §5]")
	}
	// P0-04/P0-06: two-pass spawner with sparse created[] [P0-04][P0-06].
	// Pass one walks records in order, applies the player check, then invokes
	// the normal allocator; allocation failure leaves a sparse nil entry. No delayed CreationCountdown queue
	// (bounded negative: no reader for CreationCountdown). [P0-04]
	// Eligibility checks the occupied slot, participating control state, and
	// non-newline placement terminator [P0-04] – diagnostic
	// but still creates the unit. We preserve sparse
	// mapping for P0-06 first-occurrence scan skipping NULL gaps (A27).
	for idx, up := range m.Units {
		def, ok := s.Catalog.Unit(up.UnitName)
		if !ok || def == nil {
			continue // Missing unit definition leaves this placement slot empty.
		}
		// Retail mapping: 0→1 then idx=byte-1, so 0 and 1 both map to 0 (human), 2→1, etc. [P0-04] I13.
		// Production mapping collapses placement player values 0 and 1 to human
		// owner 0, then maps subsequent values to their corresponding owner. [P0-04][I13]
		p := up.Player
		if p == 0 {
			p = 1
		}
		ownerIdx := p - 1
		if ownerIdx < 0 {
			ownerIdx = 0
		}
		if ownerIdx > 9 {
			ownerIdx = 9
		}
		owner := uint8(ownerIdx)
		h, err := s.Units.Create(def, owner, numeric.Fixed(int64(up.X)), numeric.Fixed(int64(up.Y)), numeric.Fixed(int64(up.Z)))
		if err != nil {
			continue // allocation failure → sparse NULL
		}
		u := s.Units.Unit(h)
		if u != nil {
			// Mission placement invokes the common allocator draw sequence first,
			// then overwrites its authoritative heading with the authored angle
			// [R-P28-ANG-01R §2].
			u.Move.Heading = up.Angle
			u.PlacementIdx = idx
			u.PlacementIdent = up.Ident
			u.PlacementUnitName = up.UnitName
			if up.HealthPercentage != 0 && up.HealthPercentage != 100 {
				u.Health = int32(int64(u.MaxHealth) * int64(up.HealthPercentage) / 100)
			}
			if up.IsImmune() {
				u.Flags |= 1 << 15
			}
			// Publish visibility synchronously before loader returns — no empty-coverage frame [03 §3.3] C10.
			publishOne(s, u)
			if s.Movement != nil && s.Movement.Routes != nil {
				s.Movement.EnsureUnit(u)
			}
		}
	}
	return nil
}

func grantResourcesStrict(s *Session, m *mission.Mission) error {
	if s == nil || s.Econ == nil {
		return fmt.Errorf("session: missing Economy for mission battle entry [05]")
	}
	// Starting resources are credited DIRECTLY to live stock outside the ledger
	// [08 "Placement and battle entry"] via economy.CreditSpawn per C9. No
	// Mirror, Accepted or Carry bucket is touched. Amounts are the authored
	// HumanMetal/HumanEnergy vs ComputerMetal/ComputerEnergy from the OTA
	// GlobalHeader per [P1-02 §2.1] (authored; decode default 0 per [02 map-global keys]).
	// Using CreditSpawn preserves I2's float32 stock identity.
	if m == nil || m.OTA == nil || m.OTA.Global == nil {
		return fmt.Errorf("session: mission has no GlobalHeader for starting resources [08 \"Placement and battle entry\"]")
	}
	mg := mission.DecodeMissionGlobals(m.OTA.Global) // [P1-02 §2.1]; decode defaults per [02 map-global keys]
	for p := 0; p < 10; p++ {
		if !s.Econ.Players[p].Exists {
			continue
		}
		var metal, energy float32
		switch s.Econ.Players[p].ControllerState {
		case 1: // human local
			metal = float32(mg.HumanMetal)
			energy = float32(mg.HumanEnergy)
		case 2, 3: // computer
			metal = float32(mg.ComputerMetal)
			energy = float32(mg.ComputerEnergy)
		default:
			if p == 0 {
				metal = float32(mg.HumanMetal)
				energy = float32(mg.HumanEnergy)
			} else {
				metal = float32(mg.ComputerMetal)
				energy = float32(mg.ComputerEnergy)
			}
		}
		if metal != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, metal)
		}
		if energy != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, energy)
		}
	}
	return nil
}

// requireCOBForSession verifies that each live unit has an authored COB VM before
// InitialMission [04 §4.1]. Unit creation binds the VM and starts Create.
func requireCOBForSession(s *Session) error {
	if s == nil || s.Units == nil {
		return fmt.Errorf("session: missing Units before InitialMission [04 §4.1]")
	}
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if u.ScriptState != nil && u.ScriptState.VM != nil {
			continue
		}
		return fmt.Errorf("session: unit %d has no COB binding before InitialMission [04 §4.1]", u.Handle)
	}
	return nil
}

// wireMissionCargo wires immediate attach i-verb cargo from InitialMission [04 §3.6] P0-04.
// It scans placements for i tokens and attaches the named target unit as cargo on the carrier.
// The sparse created[] array is reconstructed via PlacementIdx to match retail's first-occurrence scan [P0-04][P0-06].
func wireMissionCargo(s *Session, m *mission.Mission) {
	if s == nil || s.Units == nil || m == nil || len(m.Units) == 0 {
		return
	}
	// Reconstruct sparse created[placementIdx] -> *units.Unit
	createdSparse := make([]*units.Unit, len(m.Units))
	for _, u := range s.Units.IterSliced() {
		if u == nil {
			continue
		}
		if u.PlacementIdx >= 0 && u.PlacementIdx < len(createdSparse) {
			createdSparse[u.PlacementIdx] = u
		}
	}
	// Build ident/unitname maps for first-occurrence scan skipping NULL gaps [P0-06].
	identMap := make(map[string]int)
	unitNameMap := make(map[string]int)
	for i, pl := range m.Units {
		if createdSparse[i] == nil {
			continue
		}
		if pl.Ident != "" {
			lower := strings.ToLower(pl.Ident)
			if _, ok := identMap[lower]; !ok {
				identMap[lower] = i
			}
		}
		if pl.UnitName != "" {
			lower := strings.ToLower(pl.UnitName)
			if _, ok := unitNameMap[lower]; !ok {
				unitNameMap[lower] = i
			}
		}
	}
	lookup := func(name string) int {
		lower := strings.ToLower(strings.TrimSpace(name))
		if idx, ok := identMap[lower]; ok {
			return idx
		}
		if idx, ok := unitNameMap[lower]; ok {
			return idx
		}
		return -1
	}
	for idx, pl := range m.Units {
		carrier := createdSparse[idx]
		if carrier == nil {
			continue
		}
		script := strings.TrimSpace(pl.InitialMission)
		if script == "" {
			continue
		}
		// Tokenize on commas as retail does (_strcspn ","), then dispatch [04 §3.6].
		tokens := strings.Split(script, ",")
		for _, tok := range tokens {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			if len(tok) > 255 {
				tok = tok[:255]
			}
			first := tok[0]
			if first >= 'A' && first <= 'Z' {
				first = first + 'a' - 'A'
			}
			if first != 'i' {
				continue
			}
			// Distinguish i vs other? 'i' alone is attach, "i <name>"
			rest := strings.TrimSpace(tok[1:])
			if rest == "" {
				continue
			}
			// splitArgs equivalent: replace commas with spaces then fields
			rest = strings.ReplaceAll(rest, ",", " ")
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			name := fields[0]
			targetIdx := lookup(name)
			if targetIdx < 0 || targetIdx >= len(createdSparse) {
				continue
			}
			target := createdSparse[targetIdx]
			if target == nil || target == carrier {
				continue
			}
			// Wire attachment: target's carrier is carrier, carrier's cargo appends target
			target.Attachment.Carrier = carrier.Handle
			target.Attachment.AttachPiece = -1
			// Avoid duplicate cargo entries
			found := false
			for _, h := range carrier.Attachment.Cargo {
				if h == target.Handle {
					found = true
					break
				}
			}
			if !found {
				carrier.Attachment.Cargo = append(carrier.Attachment.Cargo, target.Handle)
			}
		}
	}
}

// Ensure imports are used for vet.
var (
	_ = content.CanonicalKey
	_ = world.NewWind
)
