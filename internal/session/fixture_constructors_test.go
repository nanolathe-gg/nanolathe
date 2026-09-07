package session

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// fixtureCOBProgram supplies the smallest authored program needed by tests
// whose subject is not script execution. Unit creation rejects a missing or
// empty program before allocation [R-COB-04 §8]; RETURN is a complete COB
// instruction [04 §4.3]. Production composition never calls this helper.
func fixtureCOBProgram() *cob.Program {
	return &cob.Program{
		Code:    []uint32{0x10065000},
		Scripts: map[string]int{},
		Pieces:  []string{"base"},
	}
}

type sessionFixtureCOBFS struct{}

func (sessionFixtureCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }

func (sessionFixtureCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if !strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return nil, vfs.ErrNotFound
	}
	// This is an independently authored one-script COB container [fmt cob].
	// Its Create entry point immediately returns [04 §4.3].
	const headerSize = 44
	data := make([]byte, 64)
	put := func(off, value uint32) { binary.LittleEndian.PutUint32(data[off:], value) }
	put(0x00, 4)
	put(0x04, 1)
	put(0x08, 0)
	put(0x0c, 1)
	put(0x18, headerSize)
	put(0x1c, headerSize+4)
	put(0x24, headerSize+8)
	put(headerSize, 0)
	put(headerSize+4, headerSize+12)
	put(headerSize+8, 0x10065000) // RETURN [04 §4.3].
	copy(data[headerSize+12:], "Create\x00")
	return data, nil
}

func (sessionFixtureCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (sessionFixtureCOBFS) Stat(string) (vfs.EntryInfo, error) {
	return vfs.EntryInfo{}, vfs.ErrNotFound
}
func (sessionFixtureCOBFS) CacheStamp(string) (string, error) { return "session-fixture", nil }

// newSessionFixtureWorld keeps non-COB tests on the production strict-binding
// boundary while supplying their independently authored no-op program
// [R-COB-04 §8]. Tests of missing-script rejection construct worlds directly.
func newSessionFixtureWorld(maxDefs int, cat *content.Catalog) *units.World {
	w := units.NewSliced(maxDefs, cat)
	w.SetCOBSource(sessionFixtureCOBFS{}, cob.NewCachedLoader())
	return w
}

func installFixtureCOB(cat *content.Catalog) {
	if cat == nil {
		return
	}
	keys := make([]string, 0, len(cat.Units))
	for key := range cat.Units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		def := cat.Units[key]
		if def != nil && (def.Script == nil || len(def.Script.Code) == 0) {
			def.Script = fixtureCOBProgram()
		}
	}
}

// scopeTestRNGStreams temporarily supplies deterministic streams to a fixture
// constructor and restores only streams it installed.
func scopeTestRNGStreams() func() {
	oldSim, oldCRT := rng.Global.Sim, rng.Global.Crt
	installedSim, installedCRT := false, false
	if rng.Global.Sim == nil {
		sim := rng.NewSimulation(0)
		rng.Global.Sim = &sim
		installedSim = true
	}
	if rng.Global.Crt == nil {
		crt := rng.NewCRT(0)
		rng.Global.Crt = &crt
		installedCRT = true
	}
	return func() {
		if installedSim {
			rng.Global.Sim = oldSim
		}
		if installedCRT {
			rng.Global.Crt = oldCRT
		}
	}
}

// createAndBindServicesForTest supplies deterministic process dependencies to
// the hand-authored fixtures. Existing test seeds and wind are preserved;
// absent values use deterministic test streams.
func createAndBindServicesForTest(t *testing.T, s *Session) error {
	if t != nil {
		t.Helper()
	}
	restore := scopeTestRNGStreams()
	if t != nil {
		t.Cleanup(restore)
	} else {
		defer restore()
	}
	if s != nil && s.Wind == nil {
		s.InitBattleWindForSession()
	}
	return createAndBindServices(s)
}

// NewSyntheticMissionForTest is a test-only fixture constructor. It accepts
// deliberately incomplete authored test inputs; production composition uses
// NewMissionWithFS and never reaches this file.
func NewSyntheticMissionForTest(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int) (*Session, error) {
	restoreRNG := scopeTestRNGStreams()
	defer restoreRNG()
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("session: empty mission path")
	}
	if fs == nil {
		fs = vfs.New()
	}
	if cat == nil {
		if compiled, err := content.Compile(fs); err == nil {
			cat = compiled
		}
	}
	var m *mission.Mission
	var err error
	if strings.Contains(path, ":") {
		campaignPath, idx, parseErr := parseCampaignMissionSelector(path)
		if parseErr != nil {
			return nil, parseErr
		}
		m, err = mission.LoadCampaignWithSink(fs, campaignPath, idx, difficulty, 0, nil)
	} else {
		m, err = mission.LoadWithType(fs, mission.TypeCampaign, path, difficulty, 0, nil)
		if err != nil {
			m2, err2 := mission.Load(fs, cat, path)
			if err2 != nil {
				return nil, err
			}
			m = m2
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	s := &Session{
		Catalog:      cat,
		Mission:      m,
		Latch:        NewEndLatch(),
		CampaignSlot: m.CampaignIndex,
	}
	if err := s.SelectForGametype(GametypeCampaign); err != nil {
		return nil, err
	}
	s.InitBattleWindForSession()
	s.InitAudio(fs)
	if s.Econ == nil {
		s.Econ = &economy.Service{}
	}
	for i := 0; i < 2 && i < len(s.Econ.Players); i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = 1
		p.IsObserver = false
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0)
	if err := placeFeatures(s, m); err != nil {
		return nil, err
	}
	installFixtureCOB(cat)
	if err := reconstructUnitsFixture(s, m); err != nil {
		return nil, err
	}
	initCOBForSession(s)
	// The interpreter owns `i name`; nothing re-reads those verbs afterwards
	// [04 §3.6] (WU-19-205, review finding R09).
	mission.RunInitialMissionsWithCatalog(m, s.Units, s.Catalog)
	grantResourcesDirect(s, m)
	if s.World != nil && s.Movement == nil {
		grid := movement.NewOccupancyGrid()
		s.Movement = movement.NewSystem(s.World, movement.Template(), grid)
		if cat != nil {
			s.Movement.SetClasses(cat.Movement)
		}
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	s.RegisterAll()
	return s, nil
}

func reconstructUnitsFixture(s *Session, m *mission.Mission) error {
	if s.Units == nil {
		s.Units = units.NewSliced(600, s.Catalog)
	}
	for idx, up := range m.Units {
		def, ok := s.Catalog.Unit(up.UnitName)
		if !ok || def == nil {
			continue
		}
		owner := uint8(up.Player)
		if owner > 9 {
			owner = 9
		}
		// The spawner's position fixup, as production runs it: a structure is
		// snapped and re-seated, a mobile record is untouched [08 R-ENTRY-01 §6].
		px, py, pz := missionPlacementPosition(s.World, def, up)
		h, err := s.Units.Create(def, owner, px, py, pz)
		if err != nil {
			continue
		}
		u := s.Units.Unit(h)
		if u != nil {
			u.PlacementIdx = idx
			u.PlacementIdent = up.Ident
			u.PlacementUnitName = up.UnitName
			if up.HealthPercentage != 0 && up.HealthPercentage != 100 {
				u.Health = int32(int64(u.MaxHealth) * int64(up.HealthPercentage) / 100)
			}
			if up.IsImmune() {
				u.Flags |= 1 << 15
			}
			if def.ExtractsMetal != 0 && s.World != nil {
				cx := world.WorldToCell(numeric.Fixed(int64(up.X)))
				cz := world.WorldToCell(numeric.Fixed(int64(up.Z)))
				footX := int(def.FootprintX)
				footZ := int(def.FootprintZ)
				if footX <= 0 {
					footX = 1
				}
				if footZ <= 0 {
					footZ = 1
				}
				cx -= int32(footX / 2)
				cz -= int32(footZ / 2)
				if v, err := s.World.SampleMetal(cx, cz, footX, footZ, float32(def.ExtractsMetal)); err == nil {
					u.SpotMetal = v
				}
			}
			publishOne(s, u)
			if s.Movement != nil && s.Movement.Routes != nil {
				s.Movement.EnsureUnit(u)
			}
		}
	}
	return nil
}

// NewSyntheticSkirmishForTest is a test-only fixture constructor. It accepts
// deliberately incomplete authored test inputs; production composition uses
// NewSkirmishWithFS and never reaches this file.
func NewSyntheticSkirmishForTest(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig) (*Session, error) {
	restoreRNG := scopeTestRNGStreams()
	defer restoreRNG()
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if fs == nil {
		fs = vfs.New()
	}
	if cat == nil {
		if compiled, err := content.Compile(fs); err == nil {
			cat = compiled
		} else {
			cat = &content.Catalog{
				Units: map[string]*content.UnitDef{}, Features: map[string]*content.FeatureDef{},
				Maps: map[string]*content.MapHeader{}, Sides: []*content.SideDef{},
			}
		}
	}
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, cfg.MapName, 0, cfg.NumPlayers, nil)
	if err != nil {
		return nil, fmt.Errorf("session: skirmish map %q: %w", cfg.MapName, err)
	}
	var terrain *world.Terrain
	if t, err := world.Load(fs, cat, cfg.MapName); err == nil {
		terrain = t
		if len(cat.Maps) > 0 {
			if err := applySchemaStrict(terrain, cat, m); err != nil {
				_ = terrain.ApplySchema(nil, 0)
			}
		} else {
			_ = terrain.ApplySchema(nil, 0)
		}
	}
	// Synthetic fixtures use a sliced world even when authored model assets are
	// absent. Real sessions never call this test-only constructor.
	var unitsWorld *units.World
	if len(cat.Units) > 0 {
		if w, err := newSlicedWorldWithCOB(cat, fs); err == nil {
			unitsWorld = w
		} else {
			unitsWorld = units.NewSliced(600, cat)
			unitsWorld.SetCOBSource(fs, globalCobLoader)
		}
	} else {
		unitsWorld = units.NewSliced(600, cat)
	}
	unitsWorld.SetCOBSource(nil, nil)
	localOwner := LocalOwnerForConfig(cfg)
	enemyOwner := 0
	for i := 0; i < cfg.NumPlayers && i < 10; i++ {
		if i == localOwner {
			continue
		}
		if cfg.Players[i].IsComputer() && cfg.Players[i].AllyGroup != cfg.Players[localOwner].AllyGroup {
			enemyOwner = i
			break
		}
	}
	if enemyOwner == 0 && cfg.NumPlayers > 1 {
		for i := 0; i < cfg.NumPlayers && i < 10; i++ {
			if i != localOwner && cfg.Players[i].IsComputer() {
				enemyOwner = i
				break
			}
		}
	}
	s := &Session{
		Catalog:    cat,
		World:      terrain,
		Mission:    m,
		Skirmish:   cfg,
		Clock:      &clock.State{Requested: 10, Active: 10},
		Snapshot:   &frame.Buffer{},
		Units:      unitsWorld,
		Econ:       &economy.Service{},
		Latch:      NewEndLatch(),
		LocalOwner: uint8(localOwner),
		EnemyOwner: uint8(enemyOwner),
	}
	nPlayers := cfg.NumPlayers
	if nPlayers < 0 {
		nPlayers = 0
	}
	if nPlayers > 10 {
		nPlayers = 10
	}
	for i := 0; i < nPlayers && i < 10; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		var ctrlState uint8
		switch cfg.Players[i].Controller {
		case SkirmishControllerHuman:
			ctrlState = 1 // human local [08][PLAN_14 C8]
		case SkirmishControllerObserver:
			ctrlState = 1
		default:
			ctrlState = 2 // computer [08]
		}
		p.IsObserver = cfg.Players[i].Controller == SkirmishControllerObserver
		p.ControllerState = ctrlState
		// The row→player conversion's colour and side, as battle entry writes
		// them [08 R-SKIR-01 §2]; the commander-identity test reads the side
		// off the player record, so a fixture that skipped this write would
		// resolve every slot to side 0.
		p.Side = playerRecordByte(cfg.Players[i].Side)
		p.Logo = playerRecordByte(cfg.Players[i].Color)
		p.Name = skirmishSlotName(ctrlState, int(p.Side))
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	// Apply the skirmish first alliance row through the same predicate battle
	// entry uses, so a fixture session's rows say what a real session's rows
	// say. The fixture used to test raw AllyGroup equality, which allied every
	// row of a default setup because they all carry the unassigned sentinel 5;
	// group 5 is not a team and allies no two distinct players
	// [08 R-SKIR-01 §2]. Nothing read the fixture's rows for a result before
	// the victory sweep of [08 R-TRIG-01 §6] did, which is why the divergence
	// went unnoticed.
	for i := 0; i < nPlayers && i < 10; i++ {
		for j := 0; j < nPlayers && j < 10; j++ {
			s.Econ.Players[i].Allies[j] = skirmishPlayersAllied(cfg, i, j)
		}
	}
	// Validate hostile for fixture as well (allow sentinel all 5) [08 "Skirmish configuration"].
	hostile := false
	for i := 0; i < nPlayers && i < 10; i++ {
		for j := i + 1; j < nPlayers && j < 10; j++ {
			if !s.Econ.Players[i].Allies[j] {
				hostile = true
				break
			}
		}
		if hostile {
			break
		}
	}
	allSentinel := true
	for i := 0; i < nPlayers && i < 10; i++ {
		if cfg.Players[i].AllyGroup != SkirmishDefaultAllyGroup {
			allSentinel = false
			break
		}
	}
	if nPlayers >= 2 && !hostile && !allSentinel {
		return nil, fmt.Errorf("session: skirmish requires at least one hostile alliance [08 \"Skirmish configuration\"]")
	}
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	s.InitAudio(fs)
	// Create the services needed by the fixture without entering the authored
	// COB composition boundary.
	if s.World != nil {
		s.Features = features.NewService(s.World, rng.Global.Sim, rng.Global.Crt, s.Wind)
		_ = createAndBindServicesForTest(nil, s)
	} else {
		// No terrain: still need at least combat etc for Validate? For fixture without world, we skip full binding
		if s.Build == nil {
			s.Build = &construction.Service{}
		}
		if s.Combat == nil {
			s.Combat = &combat.Service{}
		}
		s.Combat.ControlByte = func(owner uint8) uint8 {
			if s.Econ == nil || int(owner) >= len(s.Econ.Players) || !s.Econ.Players[owner].Exists {
				return combat.ControlByteAbsent
			}
			return s.Econ.Players[owner].ControllerState
		}
		if s.Features == nil {
			s.Features = &features.Service{}
		}
		if s.Vis == nil {
			s.Vis = &visibility.Service{}
		}
		if s.Movement == nil {
			s.Movement = &movement.System{}
		}
		if s.Path == nil && s.Movement != nil && s.Movement.Scheduler != nil {
			s.Path = s.Movement.Scheduler
		}
	}
	prepareFixtureSkirmishCatalog(cat, &cfg)
	// Keep the direct fixture on the production entry topology: every live
	// non-observer owns a manager record and consumes its constructor draws
	// before any unit allocation [08 R-ENTRY-01 §3 step 24].
	for i := 0; i < nPlayers && i < 10; i++ {
		if cfg.Players[i].IsObserver() {
			continue
		}
		prof, perr := ai.LoadProfile(fs, "default")
		if perr != nil || prof == nil {
			continue
		}
		if s.World != nil {
			if err := initializeBattleAI(s, uint8(i), prof, sessionKindSkirmish); err != nil {
				return nil, err
			}
		} else {
			// Deliberately terrain-less fixtures can lock player-record topology,
			// but cannot claim rally or metal-vector initialization. Preserve the
			// exact eight-draw constructor and leave terrain-owned state unbound.
			mgr := &ai.Manager{Player: uint8(i), Profile: prof, Catalog: s.Catalog, RNG: s.SimRNG()}
			if !mgr.Strategic.InitializeRandomState(s.SimRNG()) {
				return nil, fmt.Errorf("session fixture: AI strategic state initialization failed for player %d", i)
			}
			bindAIQueue(mgr, s)
			s.AI[i] = mgr
		}
	}
	installFixtureCOB(cat)
	if err := skirmishPlaceFeatures(s, m); err != nil {
		return nil, err
	}
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		return nil, err
	}
	initCOBForSession(s)
	// A skirmish never reaches the InitialMission interpreter [04 §3.6], so no
	// attach pass runs here either (WU-19-205, review finding R09).
	if s.Econ != nil {
		for p := 0; p < cfg.NumPlayers && p < len(s.Econ.Players); p++ {
			if s.Econ.Players[p].Exists {
				// Keep the synthetic entry seam aligned with production battle
				// entry: retail installs the per-player storage bonus before
				// direct spawn credit [05 "Storage capacity"] [08
				// "Placement and battle entry"]. The old helper only credited
				// stock, so tests that exercised the production contract saw a
				// misleading zero bonus.
				s.Econ.Players[p].InstallStorageBonus(cfg.Players[p].Metal, cfg.Players[p].Energy)
				economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, float32(cfg.Players[p].Metal))
				economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, float32(cfg.Players[p].Energy))
			}
		}
		economy.RebuildCapacity(s.Econ, s.Units)
	}
	if s.World != nil && s.Movement != nil {
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	if s.Vis != nil && s.World != nil {
		publishVisibilityForAll(s)
	}
	if s.World != nil && s.Movement == nil {
		s.Movement = movement.NewSystem(s.World, movement.Template(), movement.NewOccupancyGrid())
		s.Movement.SetClasses(cat.Movement)
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	s.RegisterAll()
	if err := finishBattleEntry(s, func() error {
		clearLiveResourceStocks(s)
		skirmishGrantResourcesDirect(s, cfg)
		s.InitShareThresholds()
		return nil
	}); err != nil {
		return nil, err
	}
	// Victory evaluation runs inside authoritativeTick [RX-08].
	_ = s.SelectForGametype(GametypeMultiplayer)
	return s, nil
}
