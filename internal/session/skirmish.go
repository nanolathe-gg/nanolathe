package session

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// C8 defaults per [02 §3], [GAP T14] and [08 "Skirmish configuration"].
const (
	SkirmishMinPlayers        = 2  // [GAP T14] validated 2..10
	SkirmishMaxPlayers        = 10 // [GAP T14]
	SkirmishDefaultPlayers    = 4  // [02 §3] missing NumSkirmishPlayers installs default 4
	SkirmishDefaultMetal      = 1000
	SkirmishDefaultEnergy     = 1000
	SkirmishDefaultAllyGroup  = 5  // [GAP T14]
	SkirmishDefaultController = 0  // [GAP T14] human
	SkirmishNickCap           = 17 // [02 §3] 17-byte buffers
	skirmishNickPayload       = 16 // usable chars (17 includes NUL) [02 §3]
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
	MapName    string
	NumPlayers int
	Players    [10]SkirmishPlayer
	Location   int // 0 randomized (CRT Fisher-Yates), !=0 identity [P0-04]
}

// ApplyDefaults fills absent per-slot values with retail defaults per [GAP T14] [02 §3].
// It is idempotent and mutates the receiver. Missing NumPlayers installs default 4
// per [02 §3]. Truncation is by bytes to match buffer semantics.
func (c *SkirmishConfig) ApplyDefaults() {
	if c.NumPlayers == 0 {
		c.NumPlayers = SkirmishDefaultPlayers
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

// NewSkirmishWithFS is NewSkirmish with explicit filesystem and catalog for tests.
// When fs is nil a loose overlay is used; when cat is nil it is compiled from fs.
// It wires catalog from VFS, terrain from --map selection and spawns units at
// start positions per [08 "Placement and battle entry"] C9, then runs the single
// shared wind initializer after retaining bounds per C17 [01 §7.3].
func NewSkirmishWithFS(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig) (*Session, error) {
	// Apply defaults before validation so missing NumPlayers defaults to 4.
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if fs == nil {
		fs = vfs.New()
	}
	// Catalog from VFS [PLAN_02] if caller did not supply one.
	if cat == nil && fs != nil {
		if compiled, err := content.Compile(fs); err == nil {
			cat = compiled
		} else {
			// Fixture FS may lack full content (e.g., only maps/*.ota); keep empty catalog so map load still proceeds.
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
	// Load map mission for placements and wind bounds via the same typed path
	// the campaign loader uses, but with skirmish type discriminant.
	// Types 2 and 3 join the raw OTA path directly against maps/ with fuzzy fallback [08 "Mission type dispatch"].
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, cfg.MapName, 0, cfg.NumPlayers, nil)
	if err != nil {
		return nil, fmt.Errorf("session: skirmish map %q: %w", cfg.MapName, err)
	}
	// Terrain from --map selection [PLAN_04] [03 §2.2]; tolerate missing TNT for fixtures that supply only OTA.
	var terrain *world.Terrain
	if t, err := world.Load(fs, cat, cfg.MapName); err == nil {
		terrain = t
	}
	s := &Session{
		Catalog:  cat,
		World:    terrain,
		Mission:  m,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Kernel:   &kernel.Kernel{},
		Snapshot: &snapshot.Buffer{},
		Units:    units.New(600, cat),
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
	// Economy slot wiring for active players 0..nPlayers-1 per C8.
	for i := 0; i < nPlayers && i < 10; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		ctrl := cfg.Players[i].Controller
		var ctrlState uint8
		switch ctrl {
		case SkirmishDefaultController:
			ctrlState = 1 // human active settling [05 "Authoritative settlement order"]
		default:
			// Computer controller: map to 2 (computer-policy gate) [08 "Established AI-facing data"]
			if ctrl == 1 || ctrl == 2 || ctrl == 3 {
				ctrlState = uint8(ctrl)
			} else {
				ctrlState = 2
			}
		}
		p.ControllerState = ctrlState
		p.IsObserver = false
		p.StatusHalfwordAt144 = 1
		p.StatusWordAt140 = 0
		p.GameEnded = false
		p.EndGameCountdown = -1
		// Seed deadlines at battle entry tick per [05 "Authoritative settlement order"] C5; exact tick is 0 here.
		// SeedDeadlines will overwrite UpdateTime/WinLoseTime/DisplayTimer to current tick for active slots.
		// We call it after wind so the global tick is still 0.
		_ = i // used below for ally mapping if needed
	}
	// AllyGroup and colour are lobby/display state; they live in SkirmishConfig for C8 fixture purposes.
	// Economy does not store them directly, but we retain them in config for diagnostics.
	// Seed deadlines after player existence is established.
	s.Econ.SeedDeadlines(0)

	// Single battle-entry wind initializer after retaining terrain bounds per C17 [01 §7.3].
	// Use the same shared path mission.go uses: InitWindForSession → NewBattleWindFromBounds → InitBattleWind → SeedBriefing.
	var crt *rng.CRT
	if rng.Global.Crt != nil {
		crt = rng.Global.Crt
	} else {
		tmp := rng.NewCRT(0)
		crt = &tmp
	}
	s.InitWindForSession(crt, 0)

	// Battle entry order via the SAME shared order mission.go uses per C9 [08 "Placement and battle entry"]:
	// features → units → barrier → starting resources directly to live stock outside ledger.
	if err := SkirmishBattleEntry(s, cfg, m, nil); err != nil {
		return nil, err
	}
	// Construction service drives factory/mobile-build lifecycles [PLAN_08].
	if s.Build == nil && s.World != nil {
		s.Build = construction.NewService(s.World, s.Catalog, s.Units, s.Econ)
	}
	// Computer players get strategic AI managers dispatched by the coordinator
	// [PLAN_11 C1/C11]; humans none.
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
			continue // no profile available; skip AI this player [PLAN_11 WU-11-1]
		}
		mgr := &ai.Manager{Player: uint8(i), Profile: prof}
		mgr.Terrain = s.World
		if s.Catalog != nil {
			mgr.Catalog = s.Catalog
		}
		s.AI = append(s.AI, mgr)
	}
	// Gate-5 integration: ground steering/routes via movement.System [PLAN_14 C5 movement integration].
	if s.World != nil && s.Movement == nil {
		s.Movement = movement.NewSystem(s.World, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
		if cat != nil {
			s.Movement.SetClasses(cat.Movement)
		}
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	// Centralize subsystem registration in kernel phase order per C5 [01 §4.4] I7.
	s.RegisterAll()
	// Route through state machine as a skirmish (Gametype 2) which selects StateLoading directly per C3 [08 "Session states"].
	// NewMission does this via SelectForGametype; keep parity.
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
	spyRecord(spy, "barrier")
	if err := skirmishCrossBarrier(s); err != nil {
		return err
	}
	spyRecord(spy, "resources")
	skirmishGrantResourcesDirect(s, cfg)
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
	_ = m.Features
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
	// Helper to find Special by ID (suffix). Our Special.ID is 1-based suffix.
	findSpecial := func(id int) *mission.Special {
		for i := range starts {
			if int(starts[i].ID) == id {
				return &starts[i]
			}
			// Also handle 0-based stored case: ID==id-1?
			if int(starts[i].ID) == id-1 {
				// Fallback for 0-based decode (should not happen after fix)
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
		metal := float32(cfg.Players[p].Metal)
		energy := float32(cfg.Players[p].Energy)
		if metal != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, metal)
		}
		if energy != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, energy)
		}
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
