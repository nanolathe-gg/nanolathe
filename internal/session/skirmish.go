package session

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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

// Shell controller states distinguished at lobby boundary before conversion [08 "Skirmish configuration"].
const (
	ShellControllerOpen     = 0 // open/inactive slot [08 "Skirmish configuration"]
	ShellControllerHuman    = 1 // Player/human [08 "Skirmish configuration"]
	ShellControllerComputer = 2 // Computer [08 "Skirmish configuration"]
	ShellControllerObserver = 3 // observer/spectator [GAP T14]
)

// Session controller values stored in SkirmishConfig.Players[].Controller after conversion.
const (
	SkirmishControllerHuman    = 0 // human local [GAP T14]
	SkirmishControllerComputer = 1 // computer AI [GAP T14]
	SkirmishControllerObserver = 3 // observer [GAP T14] (distinct from controller value 2 used as computer)
)

// IsHuman, IsObserver and IsComputer are the exact controller predicates.
//
// Composition used to ask "is the controller nonzero?" to mean "is this a
// computer player". Observer is a distinct nonzero controller, so an observer
// slot was given an AI manager, units, economy actions and a share of result
// ownership. Computer stays "neither human nor observer" so controller value
// controller value 2 keeps being treated as a computer [GAP T14].
func (p SkirmishPlayer) IsHuman() bool { return p.Controller == SkirmishControllerHuman }

func (p SkirmishPlayer) IsObserver() bool { return p.Controller == SkirmishControllerObserver }

func (p SkirmishPlayer) IsComputer() bool { return !p.IsHuman() && !p.IsObserver() }

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

	// Explicit battle RNG seeds [R-CORE-02] DET-01. Retail derives the sim
	// seed from the QPC sum XOR a fixed constant (forced odd) and the CRT seed
	// from the time-of-day helper, both AT BATTLE ENTRY, wiping every
	// pre-battle draw. Nanolathe takes explicit seeds instead; the bootstrap
	// seeds both streams fresh with these before any battle setup draw.
	RNGSimSeed uint32
	RNGCrtSeed uint32

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
// Retail validation stores the supplied player count unchanged in this path;
// the player-count range branch is a compiled no-op [P0-05]. We preserve that as no-op for NumPlayers range
// (only map emptiness is an error for load). [P0-05]
func (c SkirmishConfig) Validate() error {
	if strings.TrimSpace(c.MapName) == "" {
		return fmt.Errorf("session: empty skirmish map [08 \"Skirmish configuration\"]")
	}
	// NumSkirmishPlayers range check is intentionally no-op [P0-05].
	return nil
}

// Normalize is the one canonical setup normalization path used by menu and direct
// battle entry [08 "Skirmish configuration"] [GAP T14].
// It trims map name, defaults missing NumPlayers to 4 only when zero, clamps 0..10,
// clears inactive rows 2..9 beyond NumPlayers, fills per-slot defaults for active
// rows, and validates at least one hostile alliance (all live ally groups equal
// is an error) [08 "Skirmish configuration"]. The production constructor applies
// the separate human/computer presence check after normalization.
// It is idempotent and must be called before session composition.
func (c *SkirmishConfig) Normalize() error {
	c.MapName = strings.TrimSpace(c.MapName)
	if c.MapName == "" {
		return fmt.Errorf("session: empty skirmish map [08 \"Skirmish configuration\"]")
	}
	if c.NumPlayers == 0 {
		c.NumPlayers = SkirmishDefaultPlayers
	}
	n := c.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	c.NumPlayers = n
	// Clear inactive rows beyond NumPlayers to ensure inactive cannot affect result [GAP T14].
	for i := n; i < 10; i++ {
		c.Players[i] = SkirmishPlayer{}
	}
	if !c.rulesDefaultsApplied {
		c.Difficulty = SkirmishDefaultDifficulty
		c.Location = SkirmishDefaultLocation
		c.CommanderDeath = SkirmishDefaultCommanderDeath
		c.Mapping = SkirmishDefaultMapping
		c.LineOfSight = SkirmishDefaultLineOfSight
		c.LOSType = SkirmishDefaultLOSType
		c.rulesDefaultsApplied = true
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
		if i != 0 && p.Color == 0 {
			p.Color = i
		}
		if p.Side == 0 && (i&1) == 1 {
			p.Side = 1
		}
		if len(p.Nickname) > skirmishNickPayload {
			p.Nickname = p.Nickname[:skirmishNickPayload]
		}
	}
	for i := 0; i < 10; i++ {
		if len(c.Players[i].Nickname) > skirmishNickPayload {
			c.Players[i].Nickname = c.Players[i].Nickname[:skirmishNickPayload]
		}
	}
	// Validate skirmish start per [08 "Skirmish configuration"]:
	// The lobby's human/computer presence check is applied by the production
	// constructor after normalization; this method also serves low-level config
	// editing where controller rows may still be incomplete.
	// Hostile alliance: at least one pair with different ally group, with sentinel
	// exception that all groups 5 (unassigned) is allowed per retail [08 "Skirmish configuration"].
	if n >= 2 {
		hostile := false
		base := c.Players[0].AllyGroup
		for i := 1; i < n; i++ {
			if c.Players[i].AllyGroup != base {
				hostile = true
				break
			}
		}
		allSentinel := true
		for i := 0; i < n; i++ {
			if c.Players[i].AllyGroup != SkirmishDefaultAllyGroup {
				allSentinel = false
				break
			}
		}
		if !hostile && !allSentinel {
			return fmt.Errorf("session: skirmish requires at least one hostile alliance (all ally groups %d) [08 \"Skirmish configuration\"]", base)
		}
	}
	return nil
}

// DirectSkirmishConfig returns the canonical direct 1v1 config [08 "Skirmish configuration"] [GAP T14].
// It explicitly sets NumPlayers=2 and clears rows 2..9 inactive, then normalizes.
// It does not rely on ApplyDefaults default 4 remaining.
func DirectSkirmishConfig(mapName string) SkirmishConfig {
	cfg := SkirmishConfig{MapName: mapName}
	cfg.NumPlayers = 2
	// Clear rows 2..9 before defaults to ensure inactive cannot affect result.
	for i := 2; i < 10; i++ {
		cfg.Players[i] = SkirmishPlayer{}
	}
	cfg.Players[0].Controller = SkirmishControllerHuman
	cfg.Players[1].Controller = SkirmishControllerComputer
	// Distinct ally groups to ensure hostile alliance [08 "Skirmish configuration"].
	// Human gets 2 (matching retail ensureRetail's first human ally 2), computer gets 5 (default sentinel distinct) for byte-equivalence with menu path.
	cfg.Players[0].AllyGroup = 2
	cfg.Players[1].AllyGroup = 5
	cfg.Players[0].Side = 0
	cfg.Players[1].Side = 1
	cfg.Players[0].Color = 0
	cfg.Players[1].Color = 1
	cfg.Players[0].Metal = SkirmishDefaultMetal
	cfg.Players[0].Energy = SkirmishDefaultEnergy
	cfg.Players[1].Metal = SkirmishDefaultMetal
	cfg.Players[1].Energy = SkirmishDefaultEnergy
	_ = cfg.Normalize()
	return cfg
}

// LocalOwnerForConfig derives LocalOwner from the configured human row, not zero default [08 "Skirmish configuration"].
func LocalOwnerForConfig(cfg SkirmishConfig) int {
	n := cfg.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		if cfg.Players[i].Controller == SkirmishControllerHuman {
			return i
		}
	}
	return 0
}

// NormalizedBytes returns a deterministic byte representation for equivalence checks.
// It encodes NumPlayers and all player rows in a stable order.
func (c SkirmishConfig) NormalizedBytes() []byte {
	// Simple deterministic encoding: map name + numPlayers + each player's fields.
	var b []byte
	b = append(b, []byte(c.MapName)...)
	b = append(b, byte(c.NumPlayers), byte(c.Difficulty), byte(c.Location), byte(c.CommanderDeath), byte(c.Mapping), byte(c.LineOfSight), byte(c.LOSType))
	for i := 0; i < 10; i++ {
		p := c.Players[i]
		b = append(b, byte(p.Controller), byte(p.Side), byte(p.Color), byte(p.AllyGroup))
		// Metal/Energy as 2-byte little endian (clamped to 0..65535)
		b = append(b, byte(p.Metal), byte(p.Metal>>8), byte(p.Energy), byte(p.Energy>>8))
		b = append(b, []byte(p.Nickname)...)
		b = append(b, 0)
	}
	return b
}

// NewSkirmish is the plan API entry point per PLAN_14 Public API C8.
// It builds a live skirmish session from the VFS and the supplied config.
func NewSkirmish(cfg SkirmishConfig) (*Session, error) {
	return NewSkirmishWithFS(nil, nil, cfg)
}

// NewSkirmishWithFS is the strict production constructor. It never fabricates
// an empty catalog, nil terrain, or invented commander. Missing retail content
// aborts with a diagnostic.
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
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateSkirmishLobby(cfg); err != nil {
		return nil, err
	}
	// DET-01 [R-CORE-02]: battle bootstrap seeds both streams fresh BEFORE any
	// battle setup draw (skirmish slot shuffle, commander placement), wiping
	// every pre-battle draw from the streams' state. The session is the sole
	// RNG authority from here on; production composition supplies this explicit
	// pair, and no package-global stream or implicit seed source is consulted.
	if fs == nil {
		return nil, fmt.Errorf("session: nil filesystem for skirmish battle [02 §5]")
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
	// 4. create retail sliced unit pool [P0-16]. Skirmish is mission mode 2,
	// whose retail comparator path retains the fixed player-slot order. Keep
	// the key seam explicit for mode-3 battle reconstruction [R-P0-16-A].
	var playerSortKeys [pool.PlayerCount]uint32
	unitsWorld, err := newBattleSlicedWorldWithCOB(cat, fs, int(m.Type), playerSortKeys)
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
		if _, err := skirmishCommander(cat, sideIdx, i); err != nil {
			return nil, err
		}
	}
	// Derive LocalOwner from configured human row, not zero default [08 "Skirmish configuration"].
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
		Snapshot:   frame.NewBuffer(),
		Units:      unitsWorld,
		Econ:       &economy.Service{},
		Latch:      NewEndLatch(),
		LocalOwner: uint8(localOwner),
		EnemyOwner: uint8(enemyOwner),
	}
	// Seed both streams fresh at battle bootstrap, before any battle setup
	// draw [R-CORE-02] DET-01.
	s.SeedSessionRNG(cfg.RNGSimSeed, cfg.RNGCrtSeed)
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
		p.SetSettlementStatusPair(1, 0)
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
	// Validate at least one hostile alliance for skirmish start [08 "Skirmish configuration"].
	// Retail sentinel: all groups 5 is allowed (no error) [08 "Skirmish configuration"].
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
	// Battle-entry wind: deadline zeroed, NO draws — the first wind chain runs
	// in sub-tick 1 [R-CORE-02] DET-03.
	s.InitBattleWindForSession()
	// Meteor scheduler state (initial next-strike = per-hit spacing) is
	// written at battle entry; the write consumes no draws [R-CORE-01 §4.4.1].
	s.initMeteor()
	// Audio presentation queue/cache/music owned by session so unit/weapon/feature/UI events can queue without client import cycle [03 §8.3][03 §8.4] I6.
	s.InitAudio(fs)
	// 5. create every required service non-nil and bind ports [08][04 §7.2]
	if err := createAndBindServices(s); err != nil {
		return nil, err
	}
	// Construct every manager in ascending slot order before commander and map
	// unit allocation. Each constructor consumes its exact eight strategic
	// draws from the shared stream [08 R-ENTRY-01 §3 step 24][08 R-AI-01 §9].
	hasManagerOwner := false
	for i := 0; i < nPlayers && i < 10; i++ {
		if !cfg.Players[i].IsObserver() {
			hasManagerOwner = true
			break
		}
	}
	var sharedProf *ai.Profile
	if hasManagerOwner {
		profileName := "default"
		if m != nil && m.OTA != nil && m.OTA.Global != nil {
			if mg := mission.DecodeMissionGlobals(m.OTA.Global); mg != nil && strings.TrimSpace(mg.AIProfile) != "" {
				profileName = mg.AIProfile
			}
		}
		prof, perr := loadSkirmishAIProfile(fs, profileName)
		if perr != nil {
			return nil, perr
		}
		sharedProf = prof
	}
	for i := range s.AI {
		s.AI[i] = nil
	}
	for i := 0; i < nPlayers && i < 10; i++ {
		if cfg.Players[i].IsObserver() {
			continue
		}
		if err := initializeBattleAI(s, uint8(i), sharedProf); err != nil {
			return nil, err
		}
	}
	// 7-9. battle entry: place features → units → resources (InitialMission inside) [08 "Placement and battle entry"] C9
	if err := skirmishBattleEntry(s, cfg, m); err != nil {
		return nil, err
	}
	report.Report(FamilyPlacement, 100)
	// Ensure COB VMs for all units (load via VFS, statics zero-init, piece count from program, Create run) [04 §4.1][P1-I01]
	if err := ensureCOBForAll(s, fs); err != nil {
		return nil, err
	}
	report.Report(FamilyScripts, 100)
	// 8. movement state and visibility state for new units
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	// 11. register every authoritative phase once [01 §4.4] I7
	s.RegisterAll()
	if err := finishBattleEntry(s, func() error {
		clearLiveResourceStocks(s)
		skirmishGrantResourcesDirect(s, cfg)
		s.InitShareThresholds()
		return nil
	}); err != nil {
		return nil, err
	}
	// Alliance-aware skirmish victory: team eliminated when all its commanders
	// are dead; when <=1 hostile team remains, latch result (draw on mutual
	// destruction) [08 "Victory and defeat triggers"][08 "Skirmish configuration"]
	// CommanderDeath==1. Countdown via EndLatch [P1-01 §2.2] before visible.
	// Victory evaluation runs inside authoritativeTick (loop.go) after ledger
	// cleanup [RX-08][ON-09]; victory evaluation is part of the direct session tick.
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

// loadSkirmishAIProfile resolves the authored mission profile through the
// established ai/default.txt fallback. A computer player without a profile
// cannot run the rooted planner, so strict construction fails closed
// [08 "Established AI-facing data and rooted planner"].
func loadSkirmishAIProfile(fs vfs.FSOps, name string) (*ai.Profile, error) {
	prof, err := ai.LoadProfile(fs, name)
	if err != nil {
		return nil, fmt.Errorf("session: ai profile %q: %w", name, err)
	}
	if prof == nil {
		return nil, fmt.Errorf("session: ai profile %q: nil profile", name)
	}
	return prof, nil
}

func validateSkirmishLobby(cfg SkirmishConfig) error {
	n := cfg.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	human, computer := false, false
	for i := 0; i < n; i++ {
		switch cfg.Players[i].Controller {
		case SkirmishControllerHuman:
			human = true
		case SkirmishControllerComputer:
			computer = true
		}
	}
	if !human || !computer {
		return fmt.Errorf("session: There must be at least one player and one computer opponent [08 \"Skirmish configuration\"]")
	}
	return nil
}

// skirmishCommander resolves the configured side commander. Strict callers
// must use the authored side and unit definitions; there is no built-in
// commander substitute [08 "Skirmish configuration"].
func skirmishCommander(cat *content.Catalog, sideIdx, playerIdx int) (*content.UnitDef, error) {
	if cat == nil || sideIdx < 0 || sideIdx >= len(cat.Sides) {
		return nil, fmt.Errorf("session: side %d out of range for player %d [02 §6]", sideIdx, playerIdx)
	}
	sd := cat.Sides[sideIdx]
	if sd == nil || strings.TrimSpace(sd.Commander) == "" {
		return nil, fmt.Errorf("session: side %d missing commander [02 §6]", sideIdx)
	}
	def, ok := cat.Unit(sd.Commander)
	if !ok || def == nil {
		return nil, fmt.Errorf("session: commander %q for side %d not found [02]", sd.Commander, sideIdx)
	}
	return def, nil
}

// skirmishBattleEntry performs the single-player battle-entry order per [08
// "Placement and battle entry"] C9: place features → reconstruct units →
// grant starting resources DIRECTLY to live stock outside the ledger
// (economy.CreditSpawn) [05 "Authoritative settlement order"].
// No second draw path exists [C17].
func skirmishBattleEntry(s *Session, cfg SkirmishConfig, m *mission.Mission) error {
	if s == nil {
		return fmt.Errorf("session: nil session")
	}
	if m == nil {
		return fmt.Errorf("session: nil mission")
	}
	if err := skirmishPlaceFeatures(s, m); err != nil {
		return err
	}
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		return err
	}
	// Reconstructed units may carry lazily-created order queues. Apply the
	// already-composed session binding before any subsequent battle-entry work
	// can dispatch or resolve those queues [04 §3.3][04 §3.5][06 §11.1].
	s.bindExistingOrderQueues()
	// Initialize COB before any scripted orders (mirrors mission path) [04 §4.1]
	if err := requireCOBForSession(s); err != nil {
		return err
	}
	// Wire cargo from i-verb if any scenario units carry attachments (reuses mission helper)
	wireMissionCargo(s, m)
	skirmishGrantResourcesDirect(s, cfg)
	// Initialize sharing thresholds once from rebuilt capacity after units exist [P1-06] [P1-I04].
	// Note: thresholds are bonus-inclusive because RebuildCapacity includes the
	// per-player storage bonus.
	// Retail order is setup pass (0x497C10) before spawn credits/bonus (0x465E30→0x496E90), so
	// thresholds from capacity BEFORE bonus would be stale (0). We keep bonus-inclusive and emit
	// TODO(question): confirm whether retail writes thresholds before or after
	// the storage bonus is applied [02_ledger_exact.md §4.1].
	s.InitShareThresholds()
	return nil
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
		return fmt.Errorf("session: missing Units for skirmish battle entry [01 §6.1]")
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
	// Build the eligible-slot list per [P0-04]: occupied, participating control
	// state, and a non-newline placement terminator.
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
		// TODO(question): the placement terminator is not represented in Session;
		// eligibility is therefore checked through controller state here.
		cs := pl.ControllerState
		if cs != 1 && cs != 2 && cs != 3 {
			continue
		}
		eligible = append(eligible, i)
	}
	n := len(eligible)
	// CRT vs sim streams [P0-04] I4 DET-01: from session, not global.
	crt := s.CrtRNG()
	sim := s.SimRNG()
	// Build the permutation of eligible slots.
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
		// TODO(question): the placement terminator is not represented in Session;
		// eligibility is therefore checked through controller state here.
		if ctrl != 1 && ctrl != 2 && ctrl != 3 {
			continue
		}
		// Side/commander lookup
		sideIdx := cfg.Players[playerIdx].Side
		def, commanderErr := skirmishCommander(s.Catalog, sideIdx, playerIdx)
		if commanderErr != nil {
			return commanderErr
		}
		// Sim jitter with degenerate no-advance [P0-04]
		var jx, jz numeric.Fixed
		// Map dimensions are measured in cells. Non-positive bounds consume no
		// random value, as required by the random helper contract.
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
			}
		}
		if s.World != nil {
			y = s.World.HeightAt(x, z)
			if y == -1 {
				y = 0
			}
		}
		h, err := s.Units.Create(def, uint8(playerIdx), x, y, z)
		if err != nil {
			// The normal allocator may fail; battle entry keeps the sparse
			// creation entry null and continues [08 "Placement and battle entry"].
			continue
		}
		if u := s.Units.Unit(h); u != nil {
			// Mark as commander? No extra flags here but preserve placement linkage for debugging
			u.PlacementIdx = -1
			// Extractor yield is sampled once at placement [P1-10][P1-15] if a
			// commander definition also extracts metal (not typical).
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
					// Scenario placement overwrites the initialized heading only after
					// both common-allocation RNG invocations [R-P28-ANG-01R §2].
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
					// Extractor yield is sampled once at placement [P1-10][P1-15].
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
		// [05 "Storage capacity"] [02_ledger_exact.md §4.1] [OX P1] per-player storage bonus.
		// Retail enables the per-player storage bonus and applies a minimum
		// capacity of 0xC8 (200) to each resource, storing the integer bonus
		// as a float (truncation toward zero, I3).
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
	_ = frame.Buffer{}
	_ = world.NewWind
	_ = numeric.Fixed(0)
	_ = content.CanonicalKey
)

var _ = strings.TrimSpace
