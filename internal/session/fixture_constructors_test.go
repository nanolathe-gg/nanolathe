package session

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

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

// createAndBindServicesForTest supplies the process dependencies that ordinary
// same-package tests used to receive from composition fallbacks. Existing test
// seeds and wind are preserved; absent values use deterministic test streams.
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
		s.InitWindForSession(rng.Global.Crt, 0)
	}
	return createAndBindServices(s)
}

// NewMissionForTest is a fixture-only constructor. It retains lenient loading
// behavior for same-package tests without placing that behavior in shipping
// session construction.
func NewMissionForTest(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int) (*Session, error) {
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
		parts := strings.SplitN(path, ":", 2)
		campaignPath := strings.TrimSpace(parts[0])
		missionPart := strings.TrimSpace(parts[1])
		var idx int
		if strings.HasPrefix(strings.ToLower(missionPart), "mission") {
			num := strings.TrimSpace(missionPart[len("mission"):])
			fmt.Sscanf(num, "%d", &idx)
		} else {
			fmt.Sscanf(missionPart, "%d", &idx)
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
		Catalog: cat,
		Mission: m,
		Latch:   NewEndLatch(),
	}
	if err := s.SelectForGametype(GametypeCampaign); err != nil {
		return nil, err
	}
	s.InitWindForSession(rng.Global.Crt, 0)
	s.InitAudio(fs)
	if s.Econ == nil {
		s.Econ = &economy.Service{}
	}
	for i := 0; i < 2 && i < len(s.Econ.Players); i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = 1
		p.IsObserver = false
		p.StatusHalfwordAt144 = 1
		p.StatusWordAt140 = 0
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0)
	if err := fixtureBattleEntry(s, m, nil); err != nil {
		return nil, err
	}
	if s.World != nil && s.Movement == nil {
		grid := movement.NewOccupancyGrid()
		fallback := movement.Profile{FootPrintX: 1, FootPrintZ: 1}
		s.Movement = movement.NewSystem(s.World, fallback, grid)
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
		s.Units = units.New(600, s.Catalog)
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
		h, err := s.Units.Create(def, owner, numeric.Fixed(int64(up.X)), numeric.Fixed(int64(up.Y)), numeric.Fixed(int64(up.Z)))
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

func fixtureBattleEntry(s *Session, m *mission.Mission, spy *BattleEntrySpy) error {
	if s == nil {
		return fmt.Errorf("session: nil session")
	}
	if m == nil {
		return fmt.Errorf("session: nil mission")
	}
	spy.record("features")
	if err := placeFeatures(s, m); err != nil {
		return err
	}
	spy.record("units")
	if err := reconstructUnitsFixture(s, m); err != nil {
		return err
	}
	initCOBForSession(s)
	mission.RunInitialMissionsWithCatalog(m, s.Units, s.Catalog)
	wireMissionCargo(s, m)
	spy.record("barrier")
	if err := crossBarrier(s); err != nil {
		return err
	}
	spy.record("resources")
	grantResourcesDirect(s, m)
	return nil
}

// NewSkirmishForTest is the fixture constructor. It retains the previous
// lenient fallback (empty catalog, missing TNT tolerated, invented commander)
// so existing deterministic fixtures continue to run. Production must use
// NewSkirmishWithFS.
func NewSkirmishForTest(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig) (*Session, error) {
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
	if cat == nil && fs != nil {
		if compiled, err := content.Compile(fs); err == nil {
			cat = compiled
		} else {
			cat = &content.Catalog{
				Units:    map[string]*content.UnitDef{},
				Features: map[string]*content.FeatureDef{},
				Maps:     map[string]*content.MapHeader{},
				Sides:    []*content.SideDef{},
			}
		}
	}
	if cat == nil {
		cat = &content.Catalog{
			Units:    map[string]*content.UnitDef{},
			Features: map[string]*content.FeatureDef{},
			Maps:     map[string]*content.MapHeader{},
		}
	}
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, cfg.MapName, 0, cfg.NumPlayers, nil)
	if err != nil {
		return nil, fmt.Errorf("session: skirmish map %q: %w", cfg.MapName, err)
	}
	var terrain *world.Terrain
	if t, err := world.Load(fs, cat, cfg.MapName); err == nil {
		terrain = t
		// Fixtures may supply an incomplete catalog/map pair. Use the explicit
		// zero-metal schema only after the authored schema lookup fails.
		if cat.Maps != nil && len(cat.Maps) > 0 {
			if err := applySchemaStrict(terrain, cat, m); err != nil {
				_ = terrain.ApplySchema(nil, 0)
			}
		} else {
			_ = terrain.ApplySchema(nil, 0)
		}
	}
	// Fixture uses sliced when possible, otherwise fallback to unsliced 600 for tiny catalogs
	var unitsWorld *units.World
	if len(cat.Units) > 0 {
		if w, err := newSlicedWorldWithCOB(cat, fs); err == nil {
			unitsWorld = w
		} else {
			unitsWorld = units.New(600, cat)
			if fs != nil {
				unitsWorld.SetCOBSource(fs, globalCobLoader)
			}
		}
	} else {
		unitsWorld = units.New(600, cat)
	}
	// This constructor intentionally uses synthetic unit scripts. Keep the
	// production COB source unset so fixture allocations do not enter strict
	// authored-model binding.
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
		Snapshot:   &snapshot.Buffer{},
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
			p.IsObserver = true
		default:
			ctrlState = 2 // computer [08]
		}
		if cfg.Players[i].Controller == SkirmishControllerObserver {
			p.IsObserver = true
		} else {
			p.IsObserver = false
		}
		p.ControllerState = ctrlState
		p.StatusHalfwordAt144 = 1
		p.StatusWordAt140 = 0
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	// Apply alliances: AllyGroup equality => allied [GAP T14][08 "Skirmish configuration"]
	for i := 0; i < nPlayers && i < 10; i++ {
		for j := 0; j < nPlayers && j < 10; j++ {
			s.Econ.Players[i].Allies[j] = cfg.Players[i].AllyGroup == cfg.Players[j].AllyGroup
		}
		s.Econ.Players[i].Allies[i] = true
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
	s.InitWindForSession(rng.Global.Crt, 0)
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
	if err := skirmishBattleEntryFixture(s, &cfg, m, nil); err != nil {
		return nil, err
	}
	if s.World != nil && s.Movement != nil {
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	if s.Vis != nil && s.World != nil {
		publishVisibilityForAll(s)
	}
	// RS-02: player-indexed — AI is [10]*Manager, no make needed (zero value is nil holes)
	limitAI := nPlayers
	for i, p := range cfg.Players[:limitAI] {
		if i >= 10 {
			break
		}
		if p.Controller == 0 {
			continue
		}
		prof, perr := ai.LoadProfile(fs, "default")
		if perr != nil || prof == nil {
			continue
		}
		mgr := &ai.Manager{Player: uint8(i), Profile: prof}
		mgr.Terrain = s.World
		if s.Catalog != nil {
			mgr.Catalog = s.Catalog
		}
		bindAIQueue(mgr, s)
		s.AI[i] = mgr
	}
	// P0-I12: initialize AI class vectors for fixture managers as well, if catalog present
	hasAI2 := false
	for _, mgr := range s.AI {
		if mgr != nil {
			hasAI2 = true
			break
		}
	}
	if hasAI2 && s.Catalog != nil && len(s.Catalog.Units) > 0 {
		allTypes := make([]string, 0, len(s.Catalog.Units))
		for k := range s.Catalog.Units {
			allTypes = append(allTypes, k)
		}
		sort.Strings(allTypes)
		for _, mgr := range s.AI {
			if mgr == nil {
				continue
			}
			mgr.SetCatalog(s.Catalog)
			mgr.Strategic.Init(allTypes)
			if s.World != nil {
				mgr.Strategic.CenterX = world.CellToWorld(s.World.CellW / 2)
				mgr.Strategic.CenterZ = world.CellToWorld(s.World.CellH / 2)
				mgr.Strategic.Radius = 0
				mgr.OriginX = world.CellToWorld(s.World.CellW / 2)
				mgr.OriginZ = world.CellToWorld(s.World.CellH / 2)
				for _, u := range s.Units.IterSliced() {
					if u != nil && u.Alive && int(u.Owner) == int(mgr.Player) && u.Def != nil && u.Def.Commander {
						mgr.OriginX = u.X
						mgr.OriginZ = u.Z
						mgr.Strategic.CenterX = u.X
						mgr.Strategic.CenterZ = u.Z
						break
					}
				}
				if mgr.OriginX == 0 && mgr.OriginZ == 0 {
					for _, u := range s.Units.IterSliced() {
						if u != nil && u.Alive && int(u.Owner) == int(mgr.Player) {
							mgr.OriginX = u.X
							mgr.OriginZ = u.Z
							break
						}
					}
				}
				// Bind actual SurfaceMetal from uniform terrain metal byte [05][P0-03][RS-11]; no 255 forcing.
				if s.World != nil && len(s.World.Plot) > 0 {
					mgr.SurfaceMetal = int32(s.World.Plot[0].Metal())
				} else {
					mgr.SurfaceMetal = 0
				}
			} else {
				mgr.SurfaceMetal = 0
			}
		}
	}
	if s.World != nil && s.Movement == nil {
		s.Movement = movement.NewSystem(s.World, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
		if cat != nil {
			s.Movement.SetClasses(cat.Movement)
		}
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	s.RegisterAll()
	// Victory evaluation runs inside authoritativeTick [RX-08].
	_ = s.SelectForGametype(GametypeMultiplayer)
	return s, nil
}
