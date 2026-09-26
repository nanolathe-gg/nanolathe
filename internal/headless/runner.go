package headless

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// DefaultTickLimit is the tick ceiling a request that names none runs to: ten
// minutes of authoritative time at 30 Hz.
const DefaultTickLimit uint32 = 30 * 600

// ErrTickLimit is returned when a run stops because it reached its tick
// ceiling rather than because the battle resolved.
var ErrTickLimit = errors.New("headless: tick limit reached")

// ScenarioKind names how a run was composed, and is reported verbatim.
type ScenarioKind string

const (
	ScenarioDirectOTA ScenarioKind = "direct_ota"
	ScenarioSkirmish  ScenarioKind = "skirmish"
	ScenarioCampaign  ScenarioKind = "campaign"
	// ScenarioSurvival is a Survival battle (docs/DESIGN_SURVIVAL.md §10).
	ScenarioSurvival ScenarioKind = "survival"
	// ScenarioMission is retained as the report spelling used by the existing
	// displayless command. Fresh composition classifies the same request as a
	// campaign before constructing the session.
	ScenarioMission ScenarioKind = "mission"
)

// FreshBattleRequest is the immutable battle-entry value shared by graphical
// and displayless adapters. It carries only established constructor inputs;
// presentation dimensions are inert until a windowed adapter derives its
// camera and HUD from the completed authoritative session [08 R-ENTRY-01
// §2–§8][I6].
type FreshBattleRequest struct {
	BuilderOptions   *orders.BuilderOptions
	CommunitySources session.CommunitySources
	Gameplay         gameplay.Mode
	SelectedSide     int
	SelectedSideSet  bool
	Kind             ScenarioKind
	Map              string
	Mission          string
	CampaignIndex    int
	CampaignSlot     int
	Difficulty       int
	Skirmish         session.SkirmishConfig
	LocalOwner       int
	Watching         bool
	SimulationSeed   uint32
	CRTSeed          uint32
	FS               vfs.FSOps
	Catalog          *content.Catalog
	// ContentLimits are the table sizes the session's own catalog compile runs
	// under when Catalog is nil. An adapter resolves them from the mounted
	// content set's profile (docs/DESIGN_CONTENT_VFS.md §5 "Content
	// profiles"); the zero value is the retail baseline, and a supplied
	// Catalog already carries the limits it was compiled under.
	ContentLimits      content.Limits
	Progress           content.Progress
	PresentationWidth  int32
	PresentationHeight int32
	// AutomatedPlayers composes a skirmish with no human row (AI arena).
	AutomatedPlayers bool
	// Mutators are the battle's global multipliers, forwarded to the session
	// entry options, which apply them to the entry's catalog clone in every
	// gameplay mode (docs/DESIGN_MODS_MUTATORS.md §6). The zero value applies
	// none, so a request that names none composes the unchanged battle.
	Mutators content.Mutators
	// AIOverrides are the Modern AI computer players' configured parameters,
	// forwarded to the session entry options
	// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player"). The
	// zero value configures none.
	AIOverrides session.AIOverrides
}

// FreshBattle is the authoritative result of composition. Presentation owns
// every object derived after this boundary; no client or platform object can
// enter Session through this value [I6].
type FreshBattle struct {
	Session            *session.Session
	Kind               ScenarioKind
	Identity           string
	SimulationSeed     uint32
	CRTSeed            uint32
	LocalOwner         uint8
	Watching           bool
	TerrainWidth       int32
	TerrainHeight      int32
	InitialFingerprint string // Versioned partial state fingerprint; coverage is defined in the runtime design.
	CampaignIndex      int
	CampaignSlot       int
	PresentationW      int32
	PresentationH      int32
}

// Request is the displayless battle boundary. Seeds and the tick limit are
// explicit so equal requests can be compared without consulting host time.
type Request struct {
	GameplayFeatures  community.Overrides
	GameplayOverrides []community.Overrides
	ProfileFeatures   []community.Overrides
	Gameplay          gameplay.Mode
	Root              string   // fallback for callers supplying one root
	Roots             []string // load order; omitted roots enable installation discovery
	Map               string
	Mission           string
	Difficulty        int
	SimulationSeed    uint32
	CRTSeed           uint32
	TickLimit         uint32
	UnitLimit         int // zero uses the skirmish default; campaign keeps authored maxunits
	// Survival selects a Survival battle on Map with SurvivalBuddies allied
	// computer players (docs/DESIGN_SURVIVAL.md §10).
	Survival        session.SurvivalOptions
	SurvivalBuddies int
	// ComputerAI marks computer rows Classic or Modern by lobby row number
	// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
	// "Per-player selection"): row 2 is the direct skirmish's computer
	// player, rows 2 and 3 a Survival battle's buddies. A row that is not a
	// computer player of the battle is an error. Nil leaves every computer
	// player Classic.
	ComputerAI []session.ComputerAI
	// ContentProfile selects the mounted content set's directory table and
	// limits by name or by the path of a profile JSON file. Empty detects the
	// profile from the mounted markers. Run overwrites it with the resolved
	// name, so the report always carries the profile the run actually used
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	ContentProfile string
	// Mutators are the battle's global multipliers; the report prints the
	// canonical set the session bound (docs/DESIGN_MODS_MUTATORS.md §6.6).
	Mutators content.Mutators
	// Mod names the installed mod mounted as the last of Roots,
	// `<id>@<version>`, for the report; empty when none is mounted. The host
	// resolves and mounts it; the runner only reports it (§6.6).
	Mod string
}

// Run mounts a retail install and enters the ordinary session composition and
// Step path. The returned report is populated even when ErrTickLimit is returned.
func Run(request Request) (Report, error) {
	fs, err := mountContentRoots(request.Root, request.Roots)
	if err != nil {
		return Report{}, err
	}
	defer fs.Close()

	// The content profile is resolved after mounting and before anything
	// reads content, because detection asks the mounted overlay for its
	// markers. Everything downstream reads the returned view, so the
	// required-product check below already goes through the directory table.
	view, profile, err := contentProfileView(fs, request.ContentProfile)
	if err != nil {
		return Report{}, err
	}
	request.ContentProfile = profile.Name
	request.ProfileFeatures = profile.GameplaySources()

	for _, required := range []string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"} {
		if _, err := view.Stat(required); err != nil {
			return Report{}, diagnostic("required content is missing", required, fs.ProviderIDs(), "a mounted archive or loose file supplying it")
		}
	}
	// The catalog compiles here rather than inside composition because the
	// table limits are the profile's, and the profile is only known after the
	// mount. A retail content set resolves to the retail baseline, so this is
	// the same catalog the session would have compiled for itself.
	catalog, err := content.CompileWithOptions(view, content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	if err != nil {
		return Report{}, diagnostic("catalog compile failed: "+err.Error(), request.Map, fs.ProviderIDs(), "a complete compiled catalog")
	}
	return RunWithContent(request, view, catalog)
}

// RunWithContent reuses an already mounted VFS. Passing a nil catalog keeps
// the existing strict session constructors responsible for compilation.
func RunWithContent(request Request, fs vfs.FSOps, catalog *content.Catalog) (Report, error) {
	kind, identity, err := scenario(request)
	if err != nil {
		return Report{}, err
	}
	if fs == nil {
		return Report{}, diagnostic("session load failed: content mount is unavailable", identity, nil, "a mounted skirmish map or campaign mission")
	}

	freshKind := ScenarioDirectOTA
	if kind == ScenarioMission {
		freshKind = ScenarioCampaign
	}
	cfg := session.DirectSkirmishConfig(request.Map)
	if request.Survival.Enabled {
		if kind != ScenarioSurvival {
			return Report{}, diagnostic("session load failed: survival needs a map", identity, nil, "one skirmish map and no mission")
		}
		freshKind = ScenarioSurvival
		cfg = session.SurvivalSkirmishConfig(request.Map, request.SurvivalBuddies, request.Survival)
	}
	if request.UnitLimit != 0 {
		cfg.UnitLimit = request.UnitLimit
	}
	if len(request.ComputerAI) != 0 {
		if kind == ScenarioMission {
			return Report{}, diagnostic("session load failed: a computer AI choice names a lobby row", identity, nil, "a skirmish or Survival map")
		}
		if err := cfg.ApplyComputerAI(request.ComputerAI); err != nil {
			return Report{}, diagnostic("session load failed: "+err.Error(), identity, nil, "a computer player's lobby row")
		}
	}
	composed, err := ComposeFreshBattle(FreshBattleRequest{
		CommunitySources: session.CommunitySources{Content: request.ProfileFeatures, Player: request.GameplayFeatures, CommandLine: request.GameplayOverrides},
		Kind:             freshKind,
		Gameplay:         request.Gameplay,
		Skirmish:         cfg,
		Map:              request.Map,
		Mission:          request.Mission,
		Difficulty:       request.Difficulty,
		LocalOwner:       -1,
		SimulationSeed:   request.SimulationSeed,
		CRTSeed:          request.CRTSeed,
		FS:               fs,
		Catalog:          catalog,
		Mutators:         request.Mutators,
	})
	if err != nil {
		return Report{}, err
	}
	return RunSession(request, composed.Session)
}

// ComposeFreshBattle is the one strict authoritative constructor seam. Every
// adapter passes a value request and receives a fully constructed Session
// before it may create camera, client, HUD, or audio state [08 R-ENTRY-01
// §2–§8].
func ComposeFreshBattle(request FreshBattleRequest) (FreshBattle, error) {
	kind, identity, cfg, err := normalizeFreshBattleRequest(request)
	if err != nil {
		return FreshBattle{}, err
	}
	if request.FS == nil {
		return FreshBattle{}, diagnostic("session load failed: content mount is unavailable", identity, nil, "a mounted skirmish map or campaign mission")
	}

	cfg.Gameplay = request.Gameplay
	var sess *session.Session
	switch kind {
	case ScenarioCampaign:
		sess, err = session.NewMissionWithEntryOptions(request.FS, request.Catalog, identity, request.Difficulty, request.SimulationSeed, request.CRTSeed, session.MissionEntryOptions{BuilderOptions: request.BuilderOptions, CommunitySources: request.CommunitySources, Gameplay: request.Gameplay, SelectedSide: request.SelectedSide, SelectedSideSet: request.SelectedSideSet, ContentLimits: request.ContentLimits, Mutators: request.Mutators, AIOverrides: request.AIOverrides}, request.Progress)
	case ScenarioDirectOTA, ScenarioSkirmish, ScenarioSurvival:
		sess, err = session.NewSkirmishWithEntryOptions(request.FS, request.Catalog, cfg, session.SkirmishEntryOptions{BuilderOptions: request.BuilderOptions, CommunitySources: request.CommunitySources, Progress: request.Progress, ContentLimits: request.ContentLimits, Mutators: request.Mutators, AIOverrides: request.AIOverrides, AutomatedPlayers: request.AutomatedPlayers})
	default:
		err = fmt.Errorf("headless: unsupported fresh battle kind %q", kind)
	}
	if err != nil {
		return FreshBattle{}, diagnostic("session load failed: "+err.Error(), identity, providersFromOps(request.FS), "a valid skirmish map or campaign mission")
	}
	if sess == nil || sess.World == nil {
		return FreshBattle{}, diagnostic("session load failed: constructor returned no terrain", identity, providersFromOps(request.FS), "a complete authoritative session")
	}

	watching := request.Watching
	owner := sess.LocalOwner
	// The PLAYER RECORD, not the setup row: battle entry copies the setup
	// row's observer controller into the record, and a load restores only the
	// rule words and the map name into the setup record, so a restored battle
	// reads every setup row back as an ordinary participant
	// [08 R-SKIR-01 §2] "Save persistence".
	if sess.OwnerIsObserver(int(owner)) {
		watching = true
	}
	if request.LocalOwner >= 0 && request.LocalOwner != int(owner) {
		return FreshBattle{}, diagnostic(fmt.Sprintf("session load failed: local owner resolved to %d, request requires %d", owner, request.LocalOwner), identity, providersFromOps(request.FS), "the authored local player record")
	}

	initialFingerprint, err := sess.PartialStateFingerprint()
	if err != nil {
		return FreshBattle{}, diagnostic("session load failed: initial partial fingerprint failed: "+err.Error(), identity, providersFromOps(request.FS), "a readable session diagnostic snapshot")
	}
	return FreshBattle{
		Session: sess, Kind: kind, Identity: identity,
		SimulationSeed: request.SimulationSeed, CRTSeed: request.CRTSeed,
		LocalOwner: owner, Watching: watching,
		TerrainWidth: int32(sess.World.CellW * 16), TerrainHeight: int32(sess.World.CellH * 16),
		InitialFingerprint: initialFingerprint, CampaignIndex: request.CampaignIndex, CampaignSlot: request.CampaignSlot,
		PresentationW: request.PresentationWidth, PresentationH: request.PresentationHeight,
	}, nil
}

func normalizeFreshBattleRequest(request FreshBattleRequest) (ScenarioKind, string, session.SkirmishConfig, error) {
	mapName := strings.TrimSpace(request.Map)
	missionName := strings.TrimSpace(request.Mission)
	kind := request.Kind
	if kind == "" {
		switch {
		case missionName != "":
			kind = ScenarioCampaign
		case mapName != "":
			kind = ScenarioDirectOTA
		}
	}
	if mapName != "" && missionName != "" {
		return "", "", session.SkirmishConfig{}, diagnostic("session load failed: map and mission are mutually exclusive", "<request>", nil, "exactly one skirmish map or campaign mission")
	}

	var cfg session.SkirmishConfig
	switch kind {
	case ScenarioCampaign:
		if missionName == "" || mapName != "" {
			return "", "", cfg, diagnostic("session load failed: campaign identity is missing", "<request>", nil, "one campaign mission selector")
		}
		return kind, missionName, cfg, nil
	case ScenarioDirectOTA, ScenarioSkirmish, ScenarioSurvival:
		if mapName == "" || missionName != "" {
			return "", "", cfg, diagnostic("session load failed: map identity is missing", "<request>", nil, "one skirmish map")
		}
		cfg = request.Skirmish
		if cfg.MapName == "" {
			cfg = session.DirectSkirmishConfig(mapName)
		}
		cfg.MapName = mapName
		cfg.ApplyDefaults()
		// ApplyDefaults installs the missing-value default (Medium) on its
		// first call, then locks the scalar defaults so later callers may
		// cycle them explicitly [session.SkirmishConfig.ApplyDefaults]. The
		// requested difficulty is the caller's explicit choice, so it is
		// written after defaults land, exactly like the two RNG seeds below —
		// this is the same word the campaign path already threads through
		// unconditionally via NewMissionWithEntryOptions.
		cfg.Difficulty = request.Difficulty
		cfg.RNGSimSeed = request.SimulationSeed
		cfg.RNGCrtSeed = request.CRTSeed
		return kind, mapName, cfg, nil
	default:
		return "", "", cfg, diagnostic(fmt.Sprintf("session load failed: unsupported battle kind %q", kind), "<request>", nil, "campaign, direct OTA, or skirmish")
	}
}

// RunSession advances a composed session through its ordinary bounded Step
// loop. It is exposed so adapters and synthetic displayless tests share the
// same observer and report path without creating another phase loop.
func RunSession(request Request, sess *session.Session) (Report, error) {
	kind, identity, err := scenario(request)
	if err != nil {
		return Report{}, err
	}
	if sess == nil || sess.Clock == nil {
		return Report{}, diagnostic("session load failed: constructor returned no clock", identity, nil, "a runnable authoritative session")
	}
	observer := observe(sess)
	limit := request.TickLimit
	if limit == 0 {
		limit = DefaultTickLimit
	}
	advance(sess, limit, observer)
	report, err := buildReport(request, kind, identity, sess, observer)
	if err != nil {
		return report, err
	}
	if sess.State != session.StatePostBattle {
		report.Status = "tick_limit"
		return report, ErrTickLimit
	}
	report.Status = "terminal"
	return report, nil
}

// mountContentRoots is the common host mount boundary for runs and benchmarks.
func mountContentRoots(root string, roots []string) (*vfs.FS, error) {
	if len(roots) == 0 && root != "" {
		roots = []string{root}
	}
	roots, err := install.Resolve(roots)
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		if err := validateRoot(root); err != nil {
			return nil, err
		}
	}
	fs := vfs.New()
	if err := fs.MountGameDirectories(roots); err != nil {
		fs.Close()
		return nil, diagnostic("mounting install failed: "+err.Error(), "<content roots>", roots, "readable Total Annihilation content directories")
	}
	return fs, nil
}

// contentProfileView resolves the content profile for a mounted overlay and
// returns the read view the loaders should use, plus the resolved profile —
// its name for the report and its limits for the catalog compile. A retail content set resolves to an empty directory
// table, and an empty table returns the overlay itself — so an unmodified
// install keeps the concrete overlay, its manifest identity and its catalog
// hash (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
func contentProfileView(fs *vfs.FS, selector string) (vfs.FSOps, profiles.Profile, error) {
	profile, err := profiles.Resolve(fs, selector)
	if err != nil {
		return nil, profiles.Profile{}, err
	}
	return profile.Layout().Apply(fs), profile, nil
}

func validateRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return diagnostic("install root is not readable", root, nil, "a Total Annihilation install directory")
	}
	return nil
}

func scenario(request Request) (ScenarioKind, string, error) {
	mapName := strings.TrimSpace(request.Map)
	missionName := strings.TrimSpace(request.Mission)
	switch {
	case mapName != "" && missionName != "":
		return "", "<request>", diagnostic("session load failed: map and mission are mutually exclusive", "<request>", nil, "exactly one skirmish map or campaign mission")
	case missionName != "":
		return ScenarioMission, missionName, nil
	case mapName != "" && request.Survival.Enabled:
		return ScenarioSurvival, mapName, nil
	case mapName != "":
		return ScenarioSkirmish, mapName, nil
	default:
		return "", "<request>", diagnostic("session load failed: no map or mission was selected", "<request>", nil, "exactly one skirmish map or campaign mission")
	}
}

type diagnosticError struct {
	what      string
	logical   string
	providers []string
	expected  string
}

func diagnostic(what, logical string, providers []string, expected string) error {
	return &diagnosticError{what: what, logical: logical, providers: providers, expected: expected}
}

func (e *diagnosticError) Error() string {
	providers := "none"
	if len(e.providers) != 0 {
		providers = strings.Join(e.providers, ", ")
	}
	return fmt.Sprintf("nanolathe: %s: logical path %s, providers searched [%s], expected %s", e.what, e.logical, providers, e.expected)
}

func providersFromOps(fs vfs.FSOps) []string {
	concrete, ok := fs.(*vfs.FS)
	if !ok {
		return nil
	}
	return concrete.ProviderIDs()
}

type observer struct {
	created     [10]int
	submitted   [10]int
	active      [10]map[*orders.Node]struct{}
	intents     [10]map[string]int
	firstAttack [10]*AttackEvent
	groups      [10][]GroupSample
	sampled     bool
	lastSample  uint32
}

func observe(sess *session.Session) *observer {
	o := &observer{}
	if sess.Units != nil {
		for i := range o.created {
			o.created[i] = sess.Units.LiveCountForPlayer(i)
		}
		previous := sess.Units.OnCreate
		sess.Units.OnCreate = func(handle pool.Handle, unit *units.Unit) {
			if unit != nil && int(unit.Owner) < len(o.created) {
				o.created[unit.Owner]++
			}
			if previous != nil {
				previous(handle, unit)
			}
		}
	}
	for i, manager := range sess.AI {
		if manager == nil || manager.QueueBuildTyped == nil {
			continue
		}
		player := i
		previous := manager.QueueBuildTyped
		manager.QueueBuildTyped = func(request ai.BuildRequest) error {
			err := previous(request)
			if err == nil {
				o.submitted[player]++
			}
			return err
		}
	}
	o.scan(sess, false)
	o.sampleGroups(sess)
	return o
}

func (o *observer) scan(sess *session.Session, countNew bool) {
	if o == nil || sess.Units == nil {
		return
	}
	var current [10]map[*orders.Node]struct{}
	for _, unit := range sess.Units.IterSliced() {
		if unit == nil || int(unit.Owner) >= len(sess.AI) || sess.AI[unit.Owner] == nil {
			continue
		}
		queue := orders.QueueOfUnit(unit)
		if queue == nil {
			continue
		}
		player := int(unit.Owner)
		if current[player] == nil {
			current[player] = make(map[*orders.Node]struct{})
		}
		for _, nodes := range [2][]*orders.Node{queue.Primary(), queue.Secondary()} {
			for _, node := range nodes {
				if node == nil {
					continue
				}
				current[player][node] = struct{}{}
				if !countNew {
					continue
				}
				if _, exists := o.active[player][node]; exists {
					continue
				}
				intent := descriptorName(node.ID)
				if intent != "" {
					if o.intents[player] == nil {
						o.intents[player] = make(map[string]int)
					}
					o.intents[player][intent]++
					if isAttackIntent(intent) && sess.Clock != nil {
						o.noteAttack(player, sess.Clock.GlobalTick, intent, unit.Handle, node.Target)
					}
				}
				if node.BuildDefKey == "" {
					o.submitted[player]++
				}
			}
		}
	}
	o.active = current
}

func advance(sess *session.Session, limit uint32, observer *observer) {
	scaledNow := sess.Clock.ScaledAnchor
	var discarded []frame.EventView
	for sess.State != session.StatePostBattle && sess.Clock.GlobalTick < limit {
		remaining := limit - sess.Clock.GlobalTick
		delta := int32(5)
		if remaining < uint32(delta) {
			delta = int32(remaining)
		}
		scaledNow += delta
		sess.Step(scaledNow)
		// A displayless run has no presentation consumer. Drain the committed
		// event stream at its host-pump boundary so retained event payloads do
		// not accumulate; Buffer retains any exact overflow count for Report.
		if sess.Snapshot != nil {
			discarded = sess.Snapshot.DrainCommittedEvents(discarded)
		}
		observer.scan(sess, true)
		observer.sampleGroups(sess)
	}
}
