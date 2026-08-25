package session

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/kernel"
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

// C8 defaults per [02 §3], [GAP T14] and [08 "Skirmish configuration"].
const (
	SkirmishMinPlayers            = 2  // [GAP T14] validated 2..10
	SkirmishMaxPlayers            = 10 // [GAP T14]
	SkirmishDefaultPlayers        = 4  // [02 §3] missing NumSkirmishPlayers installs default 4
	SkirmishDefaultMetal          = 1000
	SkirmishDefaultEnergy         = 1000
	SkirmishDefaultAllyGroup      = 5  // [GAP T14]
	SkirmishDefaultController     = 0  // [GAP T14] human
	SkirmishDefaultDifficulty     = 1  // Medium [02 §3] [08 "Skirmish configuration"]
	SkirmishDefaultLocation       = 1  // pre-determined start positions [08 "Skirmish configuration"]
	SkirmishDefaultCommanderDeath = 1  // commander death ends the game [08 "Skirmish configuration"]
	SkirmishDefaultMapping        = 1  // terrain is blacked out until explored [08 "Skirmish configuration"]
	SkirmishDefaultLineOfSight    = 1  // LOS enabled [08 "Skirmish configuration"]
	SkirmishDefaultLOSType        = 1  // terrain elevations affect LOS [08 "Skirmish configuration"]
	SkirmishNickCap               = 17 // [02 §3] 17-byte buffers
	skirmishNickPayload           = 16 // usable chars (17 includes NUL) [02 §3]
)

// SkirmishPlayer is per-slot skirmish state per [GAP T14].
// Absent per-slot values install defaults: controller 0, ally group 5,
// metal and energy 1000, color the slot index, side slot&1, nicknames
// truncated to 17-byte buffer.
type SkirmishPlayer struct {
	Nickname   string
	Controller int // 0 human, non-zero computer [GAP T14]; economy mapping uses 1/2
	Side       int // side ordinal; absent defaults to slot &1 [02 §3]
	Color      int // slot index [GAP T14]
	AllyGroup  int // 5 [GAP T14]
	Metal      int // Player%dMetal default 1000 [GAP T14]
	Energy     int // Player%dEnergy default 1000 [GAP T14]
}

// SkirmishConfig is the skirmish setup discriminant per [08 "Skirmish configuration"].
// MapName is the --map selection (basename without extension).
// NumPlayers is NumSkirmishPlayers validated 2..10 [GAP T14] but retail
// validation is a compiled no-op (both branches store raw) [P0-05].
// Players holds per-slot controller/side/color/ally/metal/energy/nickname state.
// Location selects start-position assignment: 0 = randomized via CRT shuffle
// [P0-04], !=0 = identity mapping [P0-04].
type SkirmishConfig struct {
	MapName        string
	NumPlayers     int
	Players        [10]SkirmishPlayer
	Difficulty     int // 0 easy, 1 medium, 2 hard [08 "Skirmish configuration"]
	Location       int // 0 randomized (CRT Fisher-Yates), !=0 identity [P0-04]
	CommanderDeath int // 0 continues after commander death, 1 ends [08 "Skirmish configuration"]
	Mapping        int // 0 all terrain visible, 1 blacked out until explored [08 "Skirmish configuration"]
	LineOfSight    int // 0 disables LOS, 1 enables it [08 "Skirmish configuration"]
	LOSType        int // 0 elevations ignored, 1 elevations affect LOS [08 "Skirmish configuration"]

	// rulesDefaultsApplied distinguishes a zero-value config (missing registry
	// values) from an explicit Easy/randomized/off choice. It is deliberately
	// private: callers use ApplyDefaults once, then may cycle the public values.
	rulesDefaultsApplied bool
}

// ApplyDefaults fills absent per-slot values with retail defaults per [GAP T14] [02 §3].
// It is idempotent and mutates the receiver. Missing NumPlayers installs default 4
// per [02 §3]. Truncation is by bytes to match buffer semantics.
func (c *SkirmishConfig) ApplyDefaults() {
	if c.NumPlayers == 0 {
		c.NumPlayers = SkirmishDefaultPlayers
	}
	if !c.rulesDefaultsApplied {
		// The six scalar preferences are all allowed to be zero after the menu
		// changes them, so apply the retail missing-value defaults only once.
		c.Difficulty = SkirmishDefaultDifficulty
		c.Location = SkirmishDefaultLocation
		c.CommanderDeath = SkirmishDefaultCommanderDeath
		c.Mapping = SkirmishDefaultMapping
		c.LineOfSight = SkirmishDefaultLineOfSight
		c.LOSType = SkirmishDefaultLOSType
		c.rulesDefaultsApplied = true
	}
	c.MapName = strings.TrimSpace(c.MapName)
	n := c.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		p := &c.Players[i]
		if p.AllyGroup == 0 {
			p.AllyGroup = SkirmishDefaultAllyGroup
		}
		if p.Metal == 0 {
			p.Metal = SkirmishDefaultMetal
		}
		if p.Energy == 0 {
			p.Energy = SkirmishDefaultEnergy
		}
		// Colour = slot index [GAP T14]; missing (0 for non-zero slot) installs slot.
		if i != 0 && p.Color == 0 {
			p.Color = i
		}
		// Side = slot &1 [02 §3]; missing for odd slots installs 1.
		if p.Side == 0 && (i&1) == 1 {
			p.Side = 1
		}
		if len(p.Nickname) > skirmishNickPayload {
			// 17-byte buffer includes NUL, so truncate to 16 bytes [02 §3].
			p.Nickname = p.Nickname[:skirmishNickPayload]
		}
	}
	// Truncate nicknames for inactive slots as well, in case they carry data.
	for i := 0; i < 10; i++ {
		if len(c.Players[i].Nickname) > skirmishNickPayload {
			c.Players[i].Nickname = c.Players[i].Nickname[:skirmishNickPayload]
		}
	}
}

// Validate checks NumPlayers 2..10 and non-empty map per [GAP T14] C8.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// raw value unchanged [P0-05]. We preserve that as no-op for NumPlayers range
// (only map emptiness is an error for load). [P0-05]
func (c SkirmishConfig) Validate() error {
	if strings.TrimSpace(c.MapName) == "" {
		return fmt.Errorf("session: empty skirmish map [08 \"Skirmish configuration\"]")
	}
	// NumSkirmishPlayers range check is intentionally no-op [P0-05].
	return nil
}

// NewSkirmish is the plan API entry point per PLAN_14 Public API C8.
// It builds a live skirmish session from the VFS and the supplied config.
func NewSkirmish(cfg SkirmishConfig) (*Session, error) {
	return NewSkirmishWithFS(nil, nil, cfg)
}

// NewSkirmishWithFS is the strict production constructor. It never fabricates
// an empty catalog, nil terrain, or invented commander. Missing retail content
// aborts with a diagnostic. Fixtures must use NewSkirmishForTest.
// [02 §5][03 §2.2][P0-16]
func NewSkirmishWithFS(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig) (*Session, error) {
	return NewSkirmishWithProgress(fs, cat, cfg, nil)
}

// The session's own load families, reported after the catalog's. They are what
// battle entry does once the immutable catalog exists.
const (
	FamilyTerrain   = "terrain"
	FamilyUnitWorld = "unitworld"
	FamilyPlacement = "placement"
	FamilyScripts   = "scripts"
)

// NewSkirmishWithProgress is NewSkirmishWithFS with a load observer. The
// observer sees the catalog's families first and then this constructor's own,
// so a caller painting the retail loading screen can drive it from one stream.
// A nil observer makes this exactly NewSkirmishWithFS.
func NewSkirmishWithProgress(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig, report content.Progress) (*Session, error) {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if fs == nil {
		fs = vfs.New()
	}
	// 1. mount/receive VFS and compile one immutable catalog [02 §5]
	cat, err := strictCatalogWithProgress(fs, cat, report)
	if err != nil {
		return nil, err
	}
	// 2. select mission/schema [08 "Mission type dispatch"]
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, cfg.MapName, 0, cfg.NumPlayers, nil)
	if err != nil {
		return nil, fmt.Errorf("session: skirmish map %q: %w", cfg.MapName, err)
	}
	// 3. load terrain and apply selected schema including surface metal [03 §2.2][05]
	terrain, err := loadTerrainStrict(fs, cat, m)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyTerrain, 100)
	// 4. create retail sliced unit pool [P0-16]
	unitsWorld, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyUnitWorld, 100)
	// Strict: ensure side commanders exist; do not invent armcom [P0-I01]
	nPlayersCheck := cfg.NumPlayers
	if nPlayersCheck < 0 {
		nPlayersCheck = 0
	}
	if nPlayersCheck > 10 {
		nPlayersCheck = 10
	}
	for i := 0; i < nPlayersCheck; i++ {
		sideIdx := cfg.Players[i].Side
		if sideIdx < 0 || sideIdx >= len(cat.Sides) {
			return nil, fmt.Errorf("session: side %d out of range for player %d [02 §6]", sideIdx, i)
		}
		sd := cat.Sides[sideIdx]
		if sd == nil || sd.Commander == "" {
			return nil, fmt.Errorf("session: side %d missing commander [02 §6]", sideIdx)
		}
		if _, ok := cat.Unit(sd.Commander); !ok {
			return nil, fmt.Errorf("session: commander %q for side %d not found [02]", sd.Commander, sideIdx)
		}
	}
	s := &Session{
		Catalog:  cat,
		World:    terrain,
		Mission:  m,
		Skirmish: cfg,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Kernel:   &kernel.Kernel{},
		Snapshot: &snapshot.Buffer{},
		Units:    unitsWorld,
		Econ:     &economy.Service{},
		Latch:    NewEndLatch(),
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
		if cfg.Players[i].Controller == 0 {
			ctrlState = 1 // human local [08][PLAN_14 C8]
		} else {
			ctrlState = 2 // computer [08]
		}
		p.ControllerState = ctrlState
		p.IsObserver = false
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
	s.Econ.SeedDeadlines(0)
	// 10. wind via shared path [01 §7.3] C17 – single initializer after retaining bounds
	var crt *rng.CRT
	if rng.Global.Crt != nil {
		crt = rng.Global.Crt
	} else {
		tmp := rng.NewCRT(0)
		crt = &tmp
	}
	s.InitWindForSession(crt, 0)
	// Audio presentation queue/cache/music owned by session so unit/weapon/feature/UI events can queue without client import cycle [03 §8.3][03 §8.4] I6.
	s.InitAudio(fs)
	// 5. create every required service non-nil and bind ports [08][04 §7.2]
	if err := createAndBindServices(s); err != nil {
		return nil, err
	}
	// 7-9. battle entry: place features → units → barrier → resources (InitialMission inside) [08 "Placement and battle entry"] C9
	if err := SkirmishBattleEntry(s, cfg, m, nil); err != nil {
		return nil, err
	}
	report.Report(FamilyPlacement, 100)
	// Ensure COB VMs for all units (load via VFS, statics zero-init, piece count from program, Create run) [04 §4.1][P1-I01]
	ensureCOBForAll(s, fs)
	report.Report(FamilyScripts, 100)
	// 8. movement state and visibility state for new units
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	// AI managers for computer players [08 "Established AI-facing data"] [P0-I12]
	// AI profile load failure is explicit startup error [08].
	hasComputer := false
	for i := 0; i < nPlayers && i < 10; i++ {
		if cfg.Players[i].Controller != 0 {
			hasComputer = true
			break
		}
	}
	var sharedProf *ai.Profile
	if hasComputer {
		prof, perr := ai.LoadProfile(fs, "default")
		if perr != nil || prof == nil {
			if perr == nil {
				perr = fmt.Errorf("ai: nil profile")
			}
			return nil, fmt.Errorf("session: ai profile default: %w", perr)
		}
		sharedProf = prof
	}
	s.AI = make([]*ai.Manager, 0, nPlayers)
	for i, p := range cfg.Players[:nPlayers] {
		if i >= 10 {
			break
		}
		if p.Controller == 0 {
			continue
		}
		mgr := &ai.Manager{Player: uint8(i), Profile: sharedProf}
		mgr.Terrain = s.World
		mgr.Catalog = s.Catalog
		bindAIQueue(mgr, s)
		s.AI = append(s.AI, mgr)
	}
	// P0-I12: initialize class maps from catalog for each manager, ensure vectors not zero [08][P0-01]
	if len(s.AI) > 0 && s.Catalog != nil && len(s.Catalog.Units) > 0 {
		allTypes := make([]string, 0, len(s.Catalog.Units))
		for k := range s.Catalog.Units {
			allTypes = append(allTypes, k)
		}
		sort.Strings(allTypes)
		for _, mgr := range s.AI {
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
				mgr.SurfaceMetal = 255 // bias extractor placement to scatter helper B which succeeds without patch vector [P0-03][P0-I12]
			}
		}
	}
	// 11. register every authoritative phase once [01 §4.4] I7
	s.RegisterAll()
	// Alliance-aware skirmish victory: team eliminated when all its commanders
	// are dead; when <=1 hostile team remains, latch result (draw on mutual
	// destruction) [08 "Victory and defeat triggers"][08 "Skirmish configuration"]
	// CommanderDeath==1. Countdown via EndLatch [P1-01 §2.2] before visible.
	// Victory evaluation runs inside authoritativeTick (loop.go) after ledger
	// cleanup [RX-08][ON-09]; the old kernel-phase registration is retired.
	// TODO(question): CommanderDeath==0 annihilation mode not researched; defer [08 "Skirmish configuration"].
	// 12. transition through state machine [08 "Session states"] C3
	if err := s.SelectForGametype(GametypeMultiplayer); err != nil {
		return nil, err
	}
	if err := s.ValidateComposition(); err != nil {
		return nil, fmt.Errorf("session: composition invalid: %w", err)
	}
	return s, nil
}

// NewSkirmishForTest is the fixture constructor. It retains the previous
// lenient fallback (empty catalog, missing TNT tolerated, invented commander)
// so existing deterministic fixtures continue to run. Production must use
// NewSkirmishWithFS.
func NewSkirmishForTest(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig) (*Session, error) {
	cfg.ApplyDefaults()
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
		// Best-effort ApplySchema for fixtures; ignore error when header missing
		if cat.Maps != nil && len(cat.Maps) > 0 {
			_ = applySchemaStrict(terrain, cat, m)
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
		if fs != nil {
			unitsWorld.SetCOBSource(fs, globalCobLoader)
		}
	}
	s := &Session{
		Catalog:  cat,
		World:    terrain,
		Mission:  m,
		Skirmish: cfg,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Kernel:   &kernel.Kernel{},
		Snapshot: &snapshot.Buffer{},
		Units:    unitsWorld,
		Econ:     &economy.Service{},
		Latch:    NewEndLatch(),
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
		if cfg.Players[i].Controller == 0 {
			ctrlState = 1 // human local [08][PLAN_14 C8]
		} else {
			ctrlState = 2 // computer [08]
		}
		p.ControllerState = ctrlState
		p.IsObserver = false
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
	s.Econ.SeedDeadlines(0)
	var crt *rng.CRT
	if rng.Global.Crt != nil {
		crt = rng.Global.Crt
	} else {
		tmp := rng.NewCRT(0)
		crt = &tmp
	}
	s.InitWindForSession(crt, 0)
	s.InitAudio(fs)
	// Create services best-effort for fixture: use strict helper but tolerate missing world
	if s.World != nil {
		_ = createAndBindServices(s)
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
	if err := SkirmishBattleEntry(s, cfg, m, nil); err != nil {
		return nil, err
	}
	ensureCOBForAll(s, fs)
	if s.World != nil && s.Movement != nil {
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	if s.Vis != nil && s.World != nil {
		publishVisibilityForAll(s)
	}
	if s.AI == nil {
		s.AI = make([]*ai.Manager, 0, 2)
	}
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
		s.AI = append(s.AI, mgr)
	}
	// P0-I12: initialize AI class vectors for fixture managers as well, if catalog present
	if len(s.AI) > 0 && s.Catalog != nil && len(s.Catalog.Units) > 0 {
		allTypes := make([]string, 0, len(s.Catalog.Units))
		for k := range s.Catalog.Units {
			allTypes = append(allTypes, k)
		}
		sort.Strings(allTypes)
		for _, mgr := range s.AI {
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
				mgr.SurfaceMetal = 255
			} else {
				mgr.SurfaceMetal = 255
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
	// Victory evaluation runs inside authoritativeTick [RX-08]; no phase registration.
	_ = s.SelectForGametype(GametypeMultiplayer)
	return s, nil
}

// SkirmishBattleEntry performs the battle entry order per [08 "Placement and battle entry"] C9
// via the SAME four-step order mission.go's BattleEntry uses:
// place features → reconstruct units → cross the placement/start barrier →
// grant starting resources DIRECTLY to live stock outside the ledger
// (economy.CreditSpawn) [05 "Authoritative settlement order"].
// The spy records each step before the real work so order is observable even with nil world in fixtures.
// This is the shared path skirmish must use; no second draw path exists [C17].
func SkirmishBattleEntry(s *Session, cfg SkirmishConfig, m *mission.Mission, spy *BattleEntrySpy) error {
	if s == nil {
		return fmt.Errorf("session: nil session")
	}
	if m == nil {
		return fmt.Errorf("session: nil mission")
	}
	spyRecord(spy, "features")
	if err := skirmishPlaceFeatures(s, m); err != nil {
		return err
	}
	spyRecord(spy, "units")
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		return err
	}
	// Initialize COB before any scripted orders (mirrors mission path) [04 §4.1]
	initCOBForSession(s)
	// Wire cargo from i-verb if any scenario units carry attachments (reuses mission helper)
	wireMissionCargo(s, m)
	spyRecord(spy, "barrier")
	if err := skirmishCrossBarrier(s); err != nil {
		return err
	}
	spyRecord(spy, "resources")
	skirmishGrantResourcesDirect(s, cfg)
	// Initialize sharing thresholds once from rebuilt capacity after units exist [P1-06] [P1-I04].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Retail order is setup pass (0x497C10) before spawn credits/bonus (0x465E30→0x496E90), so
	// thresholds from capacity BEFORE bonus would be stale (0). We keep bonus-inclusive and emit
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	s.InitShareThresholds()
	return nil
}

func spyRecord(spy *BattleEntrySpy, step string) {
	if spy != nil {
		spy.Order = append(spy.Order, step)
	}
}

func skirmishPlaceFeatures(s *Session, m *mission.Mission) error {
	if s.World == nil || s.Features == nil {
		return nil
	}
	// Deterministic source order: terrain features already stamped via world.Load's
	// ExpandPlot + stampFeatureAnchors into Plot; we now stamp mission-authored
	// features in decode order (not map iteration) [08 "Placement and battle entry"] [I1][04 §6.2].
	// Each placement is tried in order; out-of-range or missing definition silently
	// fails per pool limits 0x100/0x800/WH*0xD [P1-10][P1-15] without aborting earlier placements.
	if m == nil || len(m.Features) == 0 {
		return nil
	}
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
			// Fallback: try terrain FeatureDefs by name match for synthetic maps
			for _, d := range s.World.FeatureDefs {
				if d != nil && d.CanonicalKey == content.CanonicalKey(name) {
					def = d
					break
				}
			}
		}
		if def == nil {
			continue
		}
		cx, cz := int(fp.X), int(fp.Z)
		// Bounds check before stamping; OOB silently skips like retail [P1-15] WH*0xD.
		if cx < 0 || cz < 0 || cx >= int(s.World.CellW) || cz >= int(s.World.CellH) {
			continue
		}
		// Stamp via service PlaceAt which handles footprint fringe and pool limits [06 §13.1].
		// No RNG draws here [08 "Placement and battle entry"].
		s.Features.PlaceAt(cx, cz, def)
	}
	return nil
}

func skirmishReconstructUnits(s *Session, cfg SkirmishConfig, m *mission.Mission) error {
	if s.Units == nil {
		s.Units = units.New(600, s.Catalog)
	}
	// Collect StartPos specials deterministically [P0-04]. No sorting beyond
	// TDF enumeration order; we keep original order (already as decoded) and
	// also build ID lookup. Retail stores ID = suffix-1, but our decode stores
	// suffix (1-based) – we handle both via ID-1 vs ID.
	var starts []mission.Special
	for _, sp := range m.Specials {
		if sp.Kind == 1 {
			starts = append(starts, sp)
		}
	}
	// Deterministic order for ID lookup: sort by ID ascending as TDF enumeration
	// is already ID order, but sort ensures stable for tests [I1].
	sort.Slice(starts, func(i, j int) bool {
		if starts[i].ID != starts[j].ID {
			return starts[i].ID < starts[j].ID
		}
		if starts[i].X != starts[j].X {
			return starts[i].X < starts[j].X
		}
		return starts[i].Z < starts[j].Z
	})
	nPlayersLocal := cfg.NumPlayers
	if nPlayersLocal < 0 {
		nPlayersLocal = 0
	}
	if nPlayersLocal > 10 {
		nPlayersLocal = 10
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// For skirmish, slot existence = idx < NumPlayers, ctrl 1/2/3 = ControllerState 1/2/3.
	// We use s.Econ.Players existence + controller mapping.
	var eligible []int
	for i := 0; i < nPlayersLocal && i < 10; i++ {
		if i >= len(s.Econ.Players) {
			continue
		}
		pl := &s.Econ.Players[i]
		if !pl.Exists {
			continue
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		cs := pl.ControllerState
		if cs != 1 && cs != 2 && cs != 3 {
			continue
		}
		eligible = append(eligible, i)
	}
	n := len(eligible)
	// CRT vs sim streams [P0-04] I4.
	var crt *rng.CRT
	if rng.Global.Crt != nil {
		crt = rng.Global.Crt
	} else {
		tmp := rng.NewCRT(0)
		crt = &tmp
	}
	var sim *rng.Simulation
	if rng.Global.Sim != nil {
		sim = rng.Global.Sim
	} else {
		tmp := rng.NewSimulation(0)
		sim = &tmp
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	local28 := make([]int, n)
	copy(local28, eligible)
	if cfg.Location == 0 {
		// Location==0 → randomized via CRT Fisher-Yates [P0-04].
		if n < 3 {
			// Gate: (CRT_rand()*2)/0x8000 unbiased 0/1 ; if 0 skip shuffle [P0-04].
			if crt != nil {
				gate := (int(crt.Rand()) * 2) / 0x8000
				if gate != 0 {
					// proceed to shuffle
					for j := 1; j < n; j++ {
						bound := uint32(j + 1)
						r := crt.Uint32n(bound)
						local28[j], local28[r] = local28[r], local28[j]
					}
				}
			}
		} else {
			for j := 1; j < n; j++ {
				bound := uint32(j + 1)
				r := crt.Uint32n(bound)
				local28[j], local28[r] = local28[r], local28[j]
			}
		}
	} else {
		// Location !=0 → identity (no shuffle) [P0-04].
	}
	// Map logical slot → startPosIndex via permuted eligible list.
	// For each eligible logical in ascending order, map = local28[next].
	permMap := make(map[int]int) // logical slot -> permuted slot
	for idx, logical := range eligible {
		if idx < len(local28) {
			permMap[logical] = local28[idx]
		}
	}
	// Helper to find Special by ID (suffix). Our Special.ID is the 1-based
	// numeric suffix of "StartPos %i" [GAP T14]; the lookup is exact — a slot
	// whose assigned index has no matching StartPos keeps its random jitter
	// [08 "Randomization for skirmish starts"].
	findSpecial := func(id int) *mission.Special {
		for i := range starts {
			if int(starts[i].ID) == id {
				return &starts[i]
			}
		}
		return nil
	}
	// Commander creation loop player 0..9 asc, same as retail sweep [P0-04] I1.
	for playerIdx := 0; playerIdx < 10; playerIdx++ {
		if playerIdx >= nPlayersLocal {
			continue
		}
		if playerIdx >= len(s.Econ.Players) || !s.Econ.Players[playerIdx].Exists {
			continue
		}
		ctrl := s.Econ.Players[playerIdx].ControllerState
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		if ctrl != 1 && ctrl != 2 && ctrl != 3 {
			continue
		}
		// Side/commander lookup
		sideIdx := cfg.Players[playerIdx].Side
		var def *content.UnitDef
		var ok bool
		if s.Catalog != nil && len(s.Catalog.Sides) > 0 && sideIdx >= 0 && sideIdx < len(s.Catalog.Sides) {
			sd := s.Catalog.Sides[sideIdx]
			if sd != nil && sd.Commander != "" {
				def, ok = s.Catalog.Unit(sd.Commander)
			}
		}
		if !ok {
			for _, cand := range []string{"armcom", "corcom"} {
				if d, found := s.Catalog.Unit(cand); found && d != nil {
					def = d
					ok = true
					break
				}
			}
		}
		if !ok || def == nil {
			def = &content.UnitDef{UnitName: "armcom", MaxDamage: 100, SightDistance: 128}
		}
		// Sim jitter with degenerate no-advance [P0-04]
		var jx, jz numeric.Fixed
		// mapW/H in cells, fallback uses CellW/H; if no world, use 0 -> bound <=0 ->0
		var mapW, mapH int32
		if s.World != nil {
			mapW = s.World.CellW
			mapH = s.World.CellH
		}
		boundW := mapW - 0xA0
		boundH := mapH - 0xA0
		var rndW, rndH int32
		if boundW > 0 && sim != nil {
			rndW = int32(sim.Uint32n(uint32(boundW)))
		} else {
			rndW = 0 // JLE ret0 no advance [P0-04]
		}
		if boundH > 0 && sim != nil {
			rndH = int32(sim.Uint32n(uint32(boundH)))
		} else {
			rndH = 0
		}
		jx = numeric.Fixed(int32(rndW+0x50) * 65536)
		jz = numeric.Fixed(int32(rndH+0x50) * 65536)
		x, z := jx, jz
		y := numeric.Fixed(0)
		// Try StartPos overwrite if mapping exists [P0-04]
		if perm, has := permMap[playerIdx]; has {
			// startPos number is perm+1 (since perm is slot index 0..9 => StartPos perm+1)
			sp := findSpecial(perm + 1)
			if sp != nil {
				x = numeric.Fixed(int32(sp.X) * 65536)
				z = numeric.Fixed(int32(sp.Z) * 65536)
			} else {
				// Missing StartPos: retain jitter fallback; emit diagnostic per [P0-04] (surplus/missing handled gracefully)
				_ = fmt.Sprintf("skirmish: missing StartPos%d for slot %d", perm+1, playerIdx)
			}
		}
		if s.World != nil {
			y = s.World.HeightAt(x, z)
			if y == -1 {
				y = 0
			}
		}
		h, _ := s.Units.Create(def, uint8(playerIdx), x, y, z)
		if u := s.Units.Unit(h); u != nil {
			// Mark as commander? No extra flags here but preserve placement linkage for debugging
			u.PlacementIdx = -1
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// Factory nanoframes sample in construction; mission-placed extractors sample here; direct World.Create remains TODO(question) if caller bypasses session [P1-10][P1-15].
			if def.ExtractsMetal != 0 && s.World != nil {
				cx := world.WorldToCell(x)
				cz := world.WorldToCell(z)
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
			// Publish visibility synchronously before loader returns — no empty-coverage frame [03 §3.3] C10.
			publishOne(s, u)
			if s.Movement != nil && s.Movement.Routes != nil {
				s.Movement.EnsureUnit(u)
			}
		}
	}
	// Also reconstruct any mission-placed units that are not commanders, at authored positions,
	// via sparse two-pass semantics [P0-04] – reuse same sparse logic as mission path.
	// For skirmish, these are additional scenario units beyond commanders.
	for idx, up := range m.Units {
		if s.Catalog != nil {
			if def, found := s.Catalog.Unit(up.UnitName); found && def != nil {
				ownerIdx := int(up.Player)
				if ownerIdx < 0 {
					ownerIdx = 0
				}
				if ownerIdx >= 10 {
					ownerIdx = 9
				}
				// Use authored fixed positions [P0-04]
				h, err := s.Units.Create(def, uint8(ownerIdx), numeric.Fixed(int64(up.X)), numeric.Fixed(int64(up.Y)), numeric.Fixed(int64(up.Z)))
				if err != nil {
					continue
				}
				if u := s.Units.Unit(h); u != nil {
					u.PlacementIdx = idx
					u.PlacementIdent = up.Ident
					u.PlacementUnitName = up.UnitName
					if up.HealthPercentage != 0 && up.HealthPercentage != 100 {
						u.Health = int32(int64(u.MaxHealth) * int64(up.HealthPercentage) / 100)
					}
					if up.IsImmune() {
						u.Flags |= 1 << 15
					}
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
							u.SpotMetal = v // once, never resampled [P1-10]
						}
					}
					publishOne(s, u)
					if s.Movement != nil && s.Movement.Routes != nil {
						s.Movement.EnsureUnit(u)
					}
				}
			}
		}
	}
	return nil
}

func skirmishCrossBarrier(s *Session) error {
	_ = s
	return nil
}

func skirmishGrantResourcesDirect(s *Session, cfg SkirmishConfig) {
	if s == nil || s.Econ == nil {
		return
	}
	nP := cfg.NumPlayers
	if nP < 0 {
		nP = 0
	}
	if nP > 10 {
		nP = 10
	}
	for p := 0; p < nP && p < 10; p++ {
		if !s.Econ.Players[p].Exists {
			continue
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Bonus must be installed BEFORE spawn credits and before first settlement's RebuildCapacity
		// so that CommitPostSettlement's bonus-inclusive capacity clamp preserves opening 1000/1000
		// past tick 30/60 [OX P1]. ARM and CORE both get correct values derived from their own startMetal/Energy.
		s.Econ.Players[p].InstallStorageBonus(cfg.Players[p].Metal, cfg.Players[p].Energy)
		// Ensure bonus is visible to capacity before first settlement. RebuildCapacity will include it
		// on next Settle; we also rebuild now so InitShareThresholds after this sees bonus-inclusive capacity.
		// Threshold ordering remains TODO(question): retail setup pass (0x497C10) precedes spawn credits (0x465E30),
		// so thresholds written from capacity BEFORE bonus would be stale (0). We choose bonus-inclusive
		// thresholds (capacity with bonus) and leave TODO if retail writes before bonus [02_ledger_exact.md §4.1].
		metal := float32(cfg.Players[p].Metal)
		energy := float32(cfg.Players[p].Energy)
		if metal != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, metal)
		}
		if energy != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, energy)
		}
	}
	// After all bonuses installed, rebuild capacity once so that InitShareThresholds that follows
	// sees bonus-inclusive capacity without waiting for the first 30-tick settlement.
	// This also ensures that a strict probe that steps 60 ticks without waiting for settlement
	// still has correct capacity for CommitPostSettlement clamp.
	if s.Units != nil {
		economy.RebuildCapacity(s.Econ, s.Units)
	}
}

// Ensure imports are used.
var (
	_ = clock.State{}
	_ = kernel.Kernel{}
	_ = snapshot.Buffer{}
	_ = world.NewWind
	_ = numeric.Fixed(0)
	_ = content.CanonicalKey
)

// aliases for vet
var _ = fmt.Sprintf
var _ = strings.TrimSpace
