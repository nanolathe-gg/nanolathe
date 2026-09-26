package session

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
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

// MissionEntryOptions carries the frontend selection independently of its value:
// zero is an explicitly selected Arm side [08 R-CAMP-01 §3].
type MissionEntryOptions struct {
	BuilderOptions   *orders.BuilderOptions
	CommunitySources CommunitySources
	Gameplay         gameplay.Mode
	SelectedSide     int
	SelectedSideSet  bool
	// ContentLimits are the table sizes a catalog compile runs under when the
	// caller supplies no catalog. They come from the mounted content set's
	// profile (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles"); the zero
	// value is the retail baseline.
	ContentLimits content.Limits
	// Mutators are the battle's global multipliers. They apply to campaign
	// missions as they do to skirmish (P5), after the unit restriction, to
	// this entry's catalog clone (docs/DESIGN_MODS_MUTATORS.md §6.3). The
	// zero value applies none.
	Mutators content.Mutators
	// AIOverrides are the Modern AI computer players' configured parameters,
	// as for a skirmish (SkirmishEntryOptions.AIOverrides).
	AIOverrides AIOverrides
}

// NewMissionWithEntryOptions is the explicit battle-entry constructor used by
// the composition layer. The RNG pair is installed before wind, placement, COB,
// or AI setup can draw from either stream [01 §7.1][01 §7.2][R-CORE-02], and
// the selected player sides are installed before any battle-entry consumer or
// the tick-zero prime [08 R-ENTRY-01 §8]. Without a selection, named campaign
// admission can resolve the side; ALL remains unknown.
func NewMissionWithEntryOptions(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int, simSeed, crtSeed uint32, options MissionEntryOptions, report content.Progress) (*Session, error) {
	entryFeatures, err := ResolveCommunity(options.Gameplay, options.CommunitySources)
	if err != nil {
		return nil, err
	}
	if options.SelectedSideSet && options.SelectedSide != 0 && options.SelectedSide != 1 {
		return nil, fmt.Errorf("session: campaign selected side %d is outside the two frontend sides", options.SelectedSide)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("session: empty mission path")
	}
	if fs == nil {
		fs = vfs.New()
	}
	cat, err = strictCatalogWithProgress(fs, cat, options.ContentLimits, report)
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
	cat = prepareCommunityWeapons(cat, options.Gameplay, entryFeatures)
	cat, err = applyEntryMutators(cat, options.Mutators)
	if err != nil {
		return nil, err
	}
	terrain, err := loadTerrainStrict(fs, cat, m, entryFeatures)
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
		Gameplay:         options.Gameplay.Normalize(),
		CommunitySources: options.CommunitySources,
		Community:        entryFeatures,
		EntryCommunity:   entryFeatures,
		Mutators:         options.Mutators,
		Catalog:          cat,
		World:            terrain,
		Mission:          m,
		Clock:            &clock.State{Requested: 10, Active: 10},
		Snapshot:         frame.NewBuffer(),
		Units:            unitsWorld,
		Econ:             &economy.Service{},
		Latch:            NewEndLatch(),
		CampaignSlot:     m.CampaignIndex,
		LocalOwner:       0, ViewingOwner: 0, EnemyOwner: 1,
	}
	// Correct controller states: human local 1, computer enemy 2 [08 "Established AI-facing data and rooted planner"]
	for i := 0; i < 2 && i < 10; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		// Campaign start fixes colours to (0, 1), independently of the
		// selected sides [08 R-CAMP-01 §3][08 R-SKIR-01 §8].
		p.Logo = uint8(i)
		if i == 0 {
			p.ControllerState = 1
			p.Name = "Player"
		} else {
			p.ControllerState = 2
		}
		p.IsObserver = false
		// The campaign seat setup is a registration path too, and registration
		// writes the score-panel rank byte to the slot index [08 R-SKIR-01 §2]
		// [07 R-HUD-04 §1]. A campaign mission never runs the kill-lead shift
		// and never shows the panel, so the seed is the byte's whole life here;
		// it exists so a slot's rank has one initial-value contract whichever
		// path registered it.
		p.SeedScorePanelRank(i)
		p.GameEnded = false
		p.EndGameCountdown = -1
		p.Allies[i] = true
	}
	// The two campaign player rows exist now, so the panel's side bytes can be
	// stamped onto them before any trigger consumer runs.
	if options.SelectedSideSet {
		stampCampaignPlayerSides(s, options.SelectedSide)
	} else {
		applyCampaignPlayerTableSides(s, fs, m)
	}
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
	if err := s.initMeteor(); err != nil {
		return nil, err
	}
	s.InitAudio(fs)
	if err := createAndBindServices(s); err != nil {
		return nil, err
	}
	if err := s.initializeBuilderOptions(options.BuilderOptions); err != nil {
		return nil, err
	}
	// Manager records and their eight-draw strategic constructors precede every
	// commander/mission unit allocation [08 R-ENTRY-01 §3 step 24].
	for i := range s.AI {
		s.AI[i] = nil
	}
	// The `aiprofile` name comes from the SELECTED SCHEMA, not [GlobalHeader]:
	// none of the 50 base campaign missions authors a global key, and their
	// per-difficulty schemas do not all agree — every Medium schema and all
	// but one Easy schema name `MISSIONS`, while all but one Hard schema name
	// `DEFAULT`, so the difficulty a mission is started at decides whether the
	// computer player runs under the campaign restrictions at all
	// [02 R-MAP-01 §5 row 7][08 R-CAMP-01 §2][08 R-AI-01 §12].
	sharedProf, perr := loadCampaignAIProfile(fs, battleAIProfileName(m))
	if perr != nil {
		return nil, perr
	}
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		if !p.Exists || p.IsObserver || (p.ControllerState != 1 && p.ControllerState != 2) {
			continue
		}
		if err := initializeBattleAI(s, uint8(i), sharedProf, sessionKindCampaign); err != nil {
			return nil, err
		}
	}
	if err := applyAIOverrides(s, options.AIOverrides); err != nil {
		return nil, err
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
	// The build menus are rebuilt against the compacted table, not carried
	// across it. Retail's restriction takes effect before the battle-entry
	// catalog compile, and that compile re-resolves every authored `canbuild<n>`
	// name through the by-name search over the surviving records: a name that is
	// no unit yields index 0 and is *skipped, not stored*
	// [02 R-CAT-01 §5 step 6], as is a download item whose UNITNAME no longer
	// resolves [02 R-CAT-01 §8 step 4]. Pruning before RestrictToCreatable keeps
	// the catalog digest — which covers the menus — consistent with what the
	// battle actually offers.
	pruneRestrictedBuildMenus(restricted, names)
	if err := restricted.RestrictToCreatable(names); err != nil {
		return nil, fmt.Errorf("nanolathe: unit restriction failed: logical path %s, providers searched [vfs], expected a compacted unit catalog: %w", useOnlyPath, err)
	}
	return restricted, nil
}

// pruneRestrictedBuildMenus drops from every compiled build menu the products
// the restriction removes, and drops the menu of a builder the restriction
// removes outright. It is the menu half of the catalog compaction described on
// applyUseOnlyRestriction: only names that still resolve to a surviving
// definition are stored [02 R-CAT-01 §5 step 6][02 R-CAT-01 §8 step 4].
//
// The surviving set is the restriction file's names intersected with the
// catalog, exactly as RestrictToCreatable computes it — a `[name]` section
// naming no definition permits nothing.
func pruneRestrictedBuildMenus(cat *content.Catalog, names []string) {
	if cat == nil {
		return
	}
	keep := make(map[string]struct{}, len(names))
	for _, n := range names {
		key := content.CanonicalKey(n)
		if key == "" {
			continue
		}
		if _, ok := cat.Units[key]; ok {
			keep[key] = struct{}{}
		}
	}
	if cat.BuildMenus != nil {
		// Deterministic iteration: the map is rewritten in sorted key order so
		// a restricted catalog is byte-identical across runs [I1].
		builders := make([]string, 0, len(cat.BuildMenus))
		for k := range cat.BuildMenus {
			builders = append(builders, k)
		}
		sort.Strings(builders)
		for _, builder := range builders {
			page := cat.BuildMenus[builder]
			if page == nil {
				continue
			}
			if _, ok := keep[content.CanonicalKey(page.Builder)]; !ok {
				// The builder itself is gone from the table, so it has no
				// record to hold a list.
				delete(cat.BuildMenus, builder)
				continue
			}
			base := 0
			kept := page.Buttons[:0:0]
			for i, button := range page.Buttons {
				if _, ok := keep[content.CanonicalKey(button)]; !ok {
					continue
				}
				kept = append(kept, button)
				if i < page.BaseButtonCount {
					base++
				}
			}
			if page.AuthoredButtons != nil {
				authored := make([]string, 0, len(page.AuthoredButtons))
				for _, button := range page.AuthoredButtons {
					if _, ok := keep[content.CanonicalKey(button)]; ok {
						authored = append(authored, button)
					}
				}
				page.AuthoredButtons = authored
			}
			page.Buttons = kept
			page.BaseButtonCount = base
		}
	}
	if len(cat.DownloadPlacements) > 0 {
		placements := cat.DownloadPlacements[:0:0]
		for _, placement := range cat.DownloadPlacements {
			if _, ok := keep[content.CanonicalKey(placement.Builder)]; !ok {
				continue
			}
			if _, ok := keep[content.CanonicalKey(placement.Product)]; !ok {
				continue
			}
			placements = append(placements, placement)
		}
		cat.DownloadPlacements = placements
	}
}

func applyCampaignPlayerTableSides(s *Session, fs vfs.FSOps, m *mission.Mission) {
	if s == nil || fs == nil || m == nil || s.Catalog == nil {
		return
	}
	if m.Type != mission.TypeCampaign || m.CampaignPath == "" {
		// A type-1 mission reached by a bare OTA path carries no campaign file
		// and therefore no authored side name. This caller supplied no frontend
		// selection either, so identity stays unknown [08 R-CAMP-01 §1].
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
		// ALL admits either selected side. A caller that supplied no explicit
		// selection cannot infer commander identity from that admission rule
		// [08 R-CAMP-01 §1].
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
	stampCampaignPlayerSides(s, local)
}

// stampCampaignPlayerSides writes both the runtime record persisted by saves
// and the known identity mirror used by commander triggers [08 "Player records"]
// [08 R-CAMP-01 §3].
func stampCampaignPlayerSides(s *Session, local int) {
	if s == nil || (local != 0 && local != 1) {
		return
	}
	for owner, side := range [2]int{local, 1 - local} {
		s.campaignPlayerSide[owner] = int8(side)
		s.campaignPlayerSideKnown[owner] = true
		if s.Econ != nil {
			s.Econ.Players[owner].Side = uint8(side)
		}
	}
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
	// No cargo pass runs here. The `i name` verb is the interpreter's, and the
	// interpreter applies it: the acting unit boards ITSELF into the named
	// carrier through the shared cargo representation [04 §3.6]. Battle entry
	// used to re-read the same verbs afterwards with the roles reversed —
	// treating the acting unit as the carrier and the named one as its cargo —
	// which left the interpreter's link in place and added the opposite one, so
	// a stock `i TRANSPORT5` produced a two-way cycle that broke attachment
	// movement, unloading and save-load cycle validation (WU-19-205, review
	// finding R09). There is no separate attachment pass in retail either:
	// "no delayed queue, cargo loop, or separate attachment pass exists beyond
	// the immediate attach verb" [08 R-TRIG-01 §9].
	return nil
}

// placeFeatures is the campaign path's mission `[features]` pass. The pass
// itself — the anchor rule, the decode order and the stamp — is
// stampMissionFeatures, which the skirmish path runs identically: the two
// battle entries differ in what happens around the pass, never in the pass
// [02 R-MAP-01 §8][05 R-FEAT-01 §3].
func placeFeatures(s *Session, m *mission.Mission) error {
	if s == nil {
		return nil
	}
	return stampMissionFeatures(s, m)
}

func reconstructUnits(s *Session, m *mission.Mission) error {
	if s.Units == nil {
		return fmt.Errorf("session: missing Units for mission battle entry [01 §6.1]")
	}
	if s.Catalog == nil {
		return fmt.Errorf("session: missing Catalog for mission battle entry [02 §5]")
	}
	// The first pass validates each resolved definition's player before the
	// allocator. Invalid players abort entry; definition misses and allocation
	// refusals leave sparse placement entries [08 R-ENTRY-01 §6].
	for idx, up := range m.Units {
		def, ok := s.Catalog.Unit(up.UnitName)
		if !ok || def == nil {
			continue // Missing unit definition leaves this placement slot empty.
		}
		// The loader normalizes zero to one; retain that normalization for
		// callers supplying decoded placements directly [08 R-TRIG-01 §9].
		p := up.Player
		if p == 0 {
			p = 1
		}
		ownerIdx := p - 1
		if ownerIdx < 0 || ownerIdx >= pool.PlayerCount || s.Econ == nil ||
			!s.Econ.Players[ownerIdx].Exists ||
			s.Econ.Players[ownerIdx].ControllerState < 1 || s.Econ.Players[ownerIdx].ControllerState > 3 ||
			s.Econ.Players[ownerIdx].Side == neutralSideIndex {
			// Fatal retail entry diagnostics propagate through the host's
			// battle-entry error boundary [08 R-ENTRY-01 §6][08 R-TRIG-01 §9].
			//lint:ignore ST1005 retail text: reproduced verbatim [08 R-ENTRY-01 §6].
			return fmt.Errorf("Player number %d invalid for unit %s", ownerIdx, up.UnitName)
		}
		owner := uint8(ownerIdx)
		// The position fixup runs between the eligibility check and the
		// allocator: a non-mobile definition is snapped to the footprint grid
		// and re-seated on the terrain, a mobile one keeps its authored triple
		// [08 R-ENTRY-01 §6].
		x, y, z := missionPlacementPosition(s.World, def, up)
		h, err := s.Units.Create(def, owner, x, y, z)
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
			// "health <- maxHealth x HealthPercentage / 100", restated in the
			// same section as "Created health is MaxDamage x HealthPercentage /
			// 100 with integer truncation" [08 R-TRIG-01 §9]. The formula is
			// UNCONDITIONAL. A guard here used to skip it for an authored 0,
			// treating that value as "absent" -- but absent is what the decoder
			// already resolves to 100 (internal/mission/placement.go,
			// [02 "Map files"]), so an authored 0 reached this arm meaning zero
			// and left the unit at full health. No stock content exercises it:
			// a census of all 275 stock .ota files found 57,485 HealthPercentage
			// keys and not one authored 0. The 100 case is the same arithmetic
			// either way, so dropping both arms costs nothing and removes a
			// reading the contract does not have.
			u.Health = missionPlacementHealth(u.MaxHealth, up.HealthPercentage)
			if up.IsImmune() {
				u.Flags |= units.ImmunityStatus
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

// missionPlacementHealth is the spawner's created-health arithmetic:
// `maxHealth x HealthPercentage / 100`, truncating toward zero [01 §8].
func missionPlacementHealth(maxHealth, percent int32) int32 {
	return int32(int64(maxHealth) * int64(percent) / 100)
}

// missionPlacementPosition is the spawner's position fixup helper
// [08 R-ENTRY-01 §6]. A **mobile** definition keeps the authored `x, y, z`
// triple exactly as the OTA record carried it. A **non-mobile** one — the
// structure class, which is `BMcode == 0` and nothing else (`CanMove` does not
// separate the two: stock factories author `CanMove=1`, see SC21) — has its
// `x` and `z` snapped to the centre of a footprint-aligned 16-unit cell and its
// `y` replaced by the spawner's terrain probe at that cell.
//
// The snap is the same pair of expressions the build-site anchor uses
// [07 §9], so it is taken from the world package rather than rewritten:
//
//	cell  = (coord − footprint·2^19 + 2^19) >> 20   world.PlacementAnchor
//	coord = (footprint + 2·cell) << 19              world.PlacementCenter
//
// Before this helper existed, every campaign definition was allocated at its
// authored coordinates, so an authored building sat off the occupancy grid and
// at whatever `YPos` the map wrote (WU-19-205, review finding R08); the stock
// corpus needs the correction on the great majority of its structure records —
// `maps/a shortage of water.ota`'s 5×5 ARMMOHO at (5696, 6544) belongs at
// (5704, 6552).
func missionPlacementPosition(t *world.Terrain, def *content.UnitDef, up mission.UnitPlacement) (x, y, z numeric.Fixed) {
	x = numeric.Fixed(int64(up.X))
	y = numeric.Fixed(int64(up.Y))
	z = numeric.Fixed(int64(up.Z))
	if def == nil || def.BMCode != 0 {
		return x, y, z // a mobile definition is untouched [08 R-ENTRY-01 §6]
	}
	cellX, cellZ := world.PlacementAnchor(x, z, def.FootprintX, def.FootprintZ)
	x, z = world.PlacementCenter(cellX, cellZ, def.FootprintX, def.FootprintZ)
	return x, missionSpawnHeight(t, def, cellX, cellZ), z
}

// missionSpawnHeight is the spawner's terrain height probe, exactly
// [08 R-ENTRY-02 §1]. It shares its aggregate walk with the build-placement
// site height — the minimum low height and maximum high height over the
// footprint cells whose yard byte carries bit 3, falling back to
// `SeaLevel − waterline` when no cell carried it — and adds two things that
// belong to the spawner alone:
//
//   - the bounds guard. Outside `cx > 0`, `cz >= 1`, `cx + fw < cellW` and
//     `cz + fh < cellH` the probe returns 0 and the unit is spawned at y = 0
//     with no diagnostic. It is not clamped to the edge and it is not an error.
//   - the fallback's 8-bit subtraction. `SeaLevel − waterline` is formed as a
//     byte and wraps; `Terrain.SiteHeight` returns the same difference widened,
//     so the result is narrowed here. A `minLow` result is already a height
//     byte, so the narrowing is a no-op on that branch.
//
// The caller shifts the byte left by 16: the spawned y is the height byte in
// whole world units. None of the placement validator's gates apply — the
// spawner never rejects a position for slope, depth or a missing geothermal
// cell [08 R-ENTRY-02 §1].
func missionSpawnHeight(t *world.Terrain, def *content.UnitDef, cellX, cellZ int32) numeric.Fixed {
	if t == nil || def == nil {
		return 0
	}
	footX, footZ := def.FootprintX, def.FootprintZ
	if cellX <= 0 || cellZ < 1 || cellX+footX >= t.CellW || cellZ+footZ >= t.CellH {
		return 0 // outside the guard: y = 0, no diagnostic [08 R-ENTRY-02 §1]
	}
	if footX <= 0 || footZ <= 0 {
		// A degenerate footprint walks no cells, so no cell can carry bit 3 and
		// the probe takes its no-bit-3 result [08 R-ENTRY-02 §1].
		return numeric.Fixed(int64(uint8(int32(t.SeaLevel)-def.Waterline)) << 16)
	}
	yard, err := world.ParseYardMap(def.YardMap, int(footX), int(footZ))
	if err != nil {
		return numeric.Fixed(int64(uint8(int32(t.SeaLevel)-def.Waterline)) << 16)
	}
	height := t.SiteHeight(cellX, cellZ, yard, int(footX), int(footZ), def.Waterline)
	return numeric.Fixed(int64(uint8(height)) << 16)
}

func grantResourcesStrict(s *Session, m *mission.Mission) error {
	if s == nil || s.Econ == nil {
		return fmt.Errorf("session: missing Economy for mission battle entry [05]")
	}
	// Starting resources are credited DIRECTLY to live stock outside the ledger
	// [08 "Placement and battle entry"] via economy.CreditSpawn per C9. No
	// Mirror, Accepted or Carry bucket is touched. Using CreditSpawn preserves
	// I2's float32 stock identity.
	//
	// The amounts are the selected schema's HumanMetal/HumanEnergy for a human
	// slot and ComputerMetal/ComputerEnergy for a computer one. They are
	// `[Schema N]` keys read with the chosen schema current [02 R-MAP-01 §5],
	// which is why they are resolved through mission.StartingResources rather
	// than through the GlobalHeader census: every stock mission authors them in
	// its schemas only, so a GlobalHeader read returned zero for the whole
	// corpus and Arm mission 2 opened with nothing to build with.
	//
	// This grant is the one that survives the tick-0 settlement, and on the
	// mission kind it writes the stocks *and* the storage bonus
	// [08 R-ENTRY-01 §8 step 5][05 R-ECO-01 §4]: the bonus setter takes the
	// same two words, floors each operand at 200 and sets the enable flag, and
	// the settlement adds the bonus to capacity once per pass. Without it the
	// opening stock exceeds capacity — the commander's own storage is far under
	// 1000 — and the post-settlement clamp claws it straight back.
	if m == nil || m.OTA == nil || m.OTA.Global == nil {
		return fmt.Errorf("session: mission has no GlobalHeader for starting resources [08 \"Placement and battle entry\"]")
	}
	res := m.StartingResources() // [02 R-MAP-01 §5]
	for p := 0; p < 10; p++ {
		if !s.Econ.Players[p].Exists {
			continue
		}
		var metal, energy int32
		switch s.Econ.Players[p].ControllerState {
		case 1: // human local
			metal, energy = res.HumanMetal, res.HumanEnergy
		case 2, 3: // computer
			metal, energy = res.ComputerMetal, res.ComputerEnergy
		default:
			if p == 0 {
				metal, energy = res.HumanMetal, res.HumanEnergy
			} else {
				metal, energy = res.ComputerMetal, res.ComputerEnergy
			}
		}
		// The bonus is installed before the credit so a capacity rebuild that
		// observes the new stock also observes the capacity that holds it.
		s.Econ.Players[p].InstallStorageBonus(int(metal), int(energy))
		if metal != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, float32(metal))
		}
		if energy != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, float32(energy))
		}
	}
	// The grant runs after the tick-0 player phase, which rebuilt capacity from
	// the unit sum alone. Rebuild once here so the bonus reaches capacity now
	// rather than at the first 30-tick settlement, matching what the skirmish
	// grant does for the same reason [05 R-ECO-01 §4].
	if s.Units != nil {
		economy.RebuildCapacity(s.Econ, s.Units)
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

// Ensure imports are used for vet.
var (
	_ = content.CanonicalKey
	_ = world.NewWind
)
