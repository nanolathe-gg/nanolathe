// Package headless composes and advances authoritative sessions without a
// presentation client or platform device.
package headless

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

const DefaultTickLimit uint32 = 30 * 600

var ErrTickLimit = errors.New("headless tick limit reached")

type ScenarioKind string

const (
	ScenarioDirectOTA ScenarioKind = "direct_ota"
	ScenarioSkirmish  ScenarioKind = "skirmish"
	ScenarioCampaign  ScenarioKind = "campaign"
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
	Kind               ScenarioKind
	Map                string
	Mission            string
	CampaignIndex      int
	CampaignSlot       int
	Difficulty         int
	Skirmish           session.SkirmishConfig
	LocalOwner         int
	Watching           bool
	SimulationSeed     uint32
	CRTSeed            uint32
	FS                 vfs.FSOps
	Catalog            *content.Catalog
	Progress           content.Progress
	PresentationWidth  int32
	PresentationHeight int32
}

// FreshBattle is the authoritative result of composition. Presentation owns
// every object derived after this boundary; no client or platform object can
// enter Session through this value [I6].
type FreshBattle struct {
	Session        *session.Session
	Kind           ScenarioKind
	Identity       string
	SimulationSeed uint32
	CRTSeed        uint32
	LocalOwner     uint8
	Watching       bool
	TerrainWidth   int32
	TerrainHeight  int32
	InitialHash    string
	CampaignIndex  int
	CampaignSlot   int
	PresentationW  int32
	PresentationH  int32
}

// Request is the displayless battle boundary. Seeds and the tick limit are
// explicit so equal requests can be compared without consulting host time.
type Request struct {
	Root           string
	Map            string
	Mission        string
	Difficulty     int
	SimulationSeed uint32
	CRTSeed        uint32
	TickLimit      uint32
}

// Run mounts a retail install and enters the ordinary session composition and
// Step path. The returned report is populated even when ErrTickLimit is returned.
func Run(request Request) (Report, error) {
	if err := validateRoot(request.Root); err != nil {
		return Report{}, err
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(request.Root); err != nil {
		return Report{}, diagnostic("mounting install failed: "+err.Error(), request.Root, nil, "a readable Total Annihilation install")
	}
	defer fs.Close()
	for _, required := range []string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"} {
		if _, err := fs.Stat(required); err != nil {
			return Report{}, diagnostic("required content is missing", required, providerNames(fs), "a mounted archive or loose file supplying it")
		}
	}
	return RunWithContent(request, fs, nil)
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
	composed, err := ComposeFreshBattle(FreshBattleRequest{
		Kind:           freshKind,
		Map:            request.Map,
		Mission:        request.Mission,
		Difficulty:     request.Difficulty,
		LocalOwner:     -1,
		SimulationSeed: request.SimulationSeed,
		CRTSeed:        request.CRTSeed,
		FS:             fs,
		Catalog:        catalog,
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

	var sess *session.Session
	switch kind {
	case ScenarioCampaign:
		sess, err = session.NewMissionWithProgressSeeds(request.FS, request.Catalog, identity, request.Difficulty, request.SimulationSeed, request.CRTSeed, request.Progress)
	case ScenarioDirectOTA, ScenarioSkirmish:
		sess, err = session.NewSkirmishWithProgress(request.FS, request.Catalog, cfg, request.Progress)
	default:
		err = fmt.Errorf("unsupported fresh battle kind %q", kind)
	}
	if err != nil {
		return FreshBattle{}, diagnostic("session load failed: "+err.Error(), identity, providersFromOps(request.FS), "a valid skirmish map or campaign mission")
	}
	if sess == nil || sess.World == nil {
		return FreshBattle{}, diagnostic("session load failed: constructor returned no terrain", identity, providersFromOps(request.FS), "a complete authoritative session")
	}

	watching := request.Watching
	owner := sess.LocalOwner
	if int(owner) < len(sess.Skirmish.Players) && sess.Skirmish.Players[owner].IsObserver() {
		watching = true
	}
	if request.LocalOwner >= 0 && request.LocalOwner != int(owner) {
		return FreshBattle{}, diagnostic(fmt.Sprintf("session load failed: local owner resolved to %d, request requires %d", owner, request.LocalOwner), identity, providersFromOps(request.FS), "the authored local player record")
	}

	initialHash, err := sess.ParityAuthoritativeHash()
	if err != nil {
		return FreshBattle{}, diagnostic("session load failed: initial authoritative hash failed: "+err.Error(), identity, providersFromOps(request.FS), "a hashable authoritative session")
	}
	return FreshBattle{
		Session: sess, Kind: kind, Identity: identity,
		SimulationSeed: request.SimulationSeed, CRTSeed: request.CRTSeed,
		LocalOwner: owner, Watching: watching,
		TerrainWidth: int32(sess.World.CellW * 16), TerrainHeight: int32(sess.World.CellH * 16),
		InitialHash: initialHash, CampaignIndex: request.CampaignIndex, CampaignSlot: request.CampaignSlot,
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
	case ScenarioDirectOTA, ScenarioSkirmish:
		if mapName == "" || missionName != "" {
			return "", "", cfg, diagnostic("session load failed: map identity is missing", "<request>", nil, "one skirmish map")
		}
		cfg = request.Skirmish
		if cfg.MapName == "" {
			cfg = session.DirectSkirmishConfig(mapName)
		}
		cfg.MapName = mapName
		cfg.ApplyDefaults()
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
	return providerNames(concrete)
}

func providerNames(fs *vfs.FS) []string {
	providers := fs.Providers()
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, filepath.Base(provider.ID))
	}
	return names
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
	for sess.State != session.StatePostBattle && sess.Clock.GlobalTick < limit {
		remaining := limit - sess.Clock.GlobalTick
		delta := int32(5)
		if remaining < uint32(delta) {
			delta = int32(remaining)
		}
		scaledNow += delta
		sess.Step(scaledNow)
		observer.scan(sess, true)
		observer.sampleGroups(sess)
	}
}
