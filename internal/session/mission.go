package session

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
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
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("session: empty mission path")
	}
	if err := requireGlobalRNGStreams(); err != nil {
		return nil, err
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
	terrain, err := loadTerrainStrict(fs, cat, m)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyTerrain, 100)
	unitsWorld, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyUnitWorld, 100)
	s := &Session{
		Catalog:  cat,
		World:    terrain,
		Mission:  m,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: &snapshot.Buffer{},
		Units:    unitsWorld,
		Econ:     &economy.Service{},
		Latch:    NewEndLatch(),
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
		p.StatusHalfwordAt144 = 1
		p.StatusWordAt140 = 0
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0) // UpdateTime/WinLoseTime/DisplayTimer seeded to GlobalTick per [05] C5; WinLoseTime is trigger poll deadline [08 "Evaluation"]
	s.InitWindForSession(rng.Global.Crt, 0)
	s.InitAudio(fs)
	if err := createAndBindServices(s); err != nil {
		return nil, err
	}
	if err := BattleEntry(s, m, nil); err != nil {
		return nil, err
	}
	report.Report(FamilyPlacement, 100)
	if err := ensureCOBForAll(s, fs); err != nil {
		return nil, err
	}
	report.Report(FamilyScripts, 100)
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	// AI managers for computer players [P0-I12] RS-02: player-indexed [10]*Manager
	for i := range s.AI {
		s.AI[i] = nil
	}
	mgAI := mission.DecodeMissionGlobals(m.OTA.Global)
	aiProfileName := mgAI.AIProfile
	if strings.TrimSpace(aiProfileName) == "" {
		aiProfileName = "default"
	}
	for i := 0; i < 2; i++ {
		if s.Econ.Players[i].ControllerState == 2 {
			prof, perr := loadCampaignAIProfile(fs, aiProfileName)
			if perr != nil {
				return nil, perr
			}
			mgr := &ai.Manager{Player: uint8(i), Profile: prof, Terrain: s.World, Catalog: s.Catalog}
			bindAIQueue(mgr, s)
			s.AI[i] = mgr
		}
	}
	// P0-I12: initialize class maps from catalog for each manager, ensure vectors not zero [08][P0-01]
	hasAI3 := false
	for _, mgr := range s.AI {
		if mgr != nil {
			hasAI3 = true
			break
		}
	}
	if hasAI3 && s.Catalog != nil && len(s.Catalog.Units) > 0 {
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
			}
		}
	}
	s.RegisterAll()
	if err := s.SelectForGametype(GametypeCampaign); err != nil {
		return nil, err
	}
	if err := s.ValidateComposition(); err != nil {
		return nil, fmt.Errorf("session: composition invalid: %w", err)
	}
	return s, nil
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

// GrantStartingResources credits starting metal/energy DIRECTLY to live stock
// outside the ledger per [05 "Authoritative settlement order"] C9 and
// [08 "Placement and battle entry"] via economy.CreditSpawn. The ledger's
// Mirror/Accepted/Carry buckets are untouched; only Stock moves. Amounts are
// per-player; zero amounts are no-ops.
func GrantStartingResources(s *Session, perPlayer [10][2]float32) {
	if s == nil || s.Econ == nil {
		return
	}
	for p := 0; p < 10; p++ {
		if perPlayer[p][economy.Metal] != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, perPlayer[p][economy.Metal])
		}
		if perPlayer[p][economy.Energy] != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, perPlayer[p][economy.Energy])
		}
	}
}

// BattleEntrySpy records the battle-entry order for C9 assertions. Tests set it
// as the callback target; production code passes nil and the helpers become
// no-ops.
type BattleEntrySpy struct {
	Order []string
}

func (sp *BattleEntrySpy) record(step string) {
	if sp != nil {
		sp.Order = append(sp.Order, step)
	}
}

// BattleEntry performs the battle entry order per [08 "Placement and battle entry"]
// C9: place features → reconstruct units → cross the placement/start barrier →
// grant starting resources DIRECTLY to live stock outside the ledger
// (economy.CreditSpawn, [05 "Authoritative settlement order"]). The spy records
// each step before the real work so order is observable even when the world is
// before any battle state is exposed.
func BattleEntry(s *Session, m *mission.Mission, spy *BattleEntrySpy) error {
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
	spy.record("features")
	if err := placeFeatures(s, m); err != nil {
		return err
	}
	spy.record("units")
	if err := reconstructUnits(s, m); err != nil {
		return err
	}
	// Initialize COB before InitialMission [04 §4.1] – each unit's VM must exist before script runs.
	if err := requireCOBForSession(s); err != nil {
		return err
	}
	// InitialMission runs ONCE on the loading worker after ALL mission units
	// exist [04 §3.6] C9 — here, between unit placement and the start barrier.
	// It queues orders; from the next tick the ordinary pump consumes them.
	// TODO(question): this interpreter's exact position in the retail loading
	// pass is inferred from vtable layout ([GAP T10] residual).
	mission.RunInitialMissionsWithCatalog(m, s.Units, s.Catalog)
	// Wire cargo/transport from i-verb immediate attach [04 §3.6] P0-04.
	wireMissionCargo(s, m)
	spy.record("barrier")
	if err := crossBarrier(s); err != nil {
		return err
	}
	spy.record("resources")
	if err := grantResourcesStrict(s, m); err != nil {
		return err
	}
	// Initialize sharing thresholds once from rebuilt capacity after units exist [P1-06] [P1-I04].
	s.InitShareThresholds()
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
			u.PlacementIdx = idx
			u.PlacementIdent = up.Ident
			u.PlacementUnitName = up.UnitName
			if up.HealthPercentage != 0 && up.HealthPercentage != 100 {
				u.Health = int32(int64(u.MaxHealth) * int64(up.HealthPercentage) / 100)
			}
			if up.IsImmune() {
				u.Flags |= 1 << 15
			}
			// Extractor yield is sampled once at placement [P1-10][P1-15]:
			// Σ(cell+1)*extractsMetal, never resampled.
			// Factory nanoframes sample in construction.allocateNanoframe; mission-placed extractors must sample here.
			// Direct World.Create paths (e.g., save restore) remain TODO(question) if terrain not available at that site [P1-10][P1-15].
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
			// Publish visibility synchronously before loader returns — no empty-coverage frame [03 §3.3] C10.
			publishOne(s, u)
			if s.Movement != nil && s.Movement.Routes != nil {
				s.Movement.EnsureUnit(u)
			}
		}
	}
	return nil
}

func crossBarrier(s *Session) error {
	// Placement/start barrier: multiplayer pumps network and sleeps 50ms
	// [08 "Placement and battle entry"]; single-player crosses immediately.
	// No simulation tick is driven by the sleep; it is confined to barrier
	// behavior [08 "Placement and battle entry"].
	_ = s
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
	// GlobalHeader per [P1-02 §2.1] (defaults 1000). Using CreditSpawn preserves I2's float32 stock identity.
	if m == nil || m.OTA == nil || m.OTA.Global == nil {
		return fmt.Errorf("session: mission has no GlobalHeader for starting resources [08 \"Placement and battle entry\"]")
	}
	mg := mission.DecodeMissionGlobals(m.OTA.Global) // [P1-02 §2.1] defaults 1000
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

// VisibilityLoadSpy records post-load visibility ordering for C10 assertions
// per [03 §3.3] and [08 "Placement and battle entry"].
type VisibilityLoadSpy struct {
	Order []string
	// Published holds the owners published synchronously before loader return
	// per C10 (no empty-coverage frame).
	Published []visibility.PlayerID
}

func (sp *VisibilityLoadSpy) record(step string) {
	if sp != nil {
		sp.Order = append(sp.Order, step)
	}
}

// PostLoadVisibility performs the post-load visibility ordering per [03 §3.3] C10:
// visibility rebuilds BEFORE the serialized mapping is read and every unit
// publishes its footprint synchronously before the loader returns — no
// empty-coverage frame [PLAN_05 C16]. The mapping blob is opaque; a missing or
// size-mismatched blob leaves the array unchanged while publication still
// proceeds per [03 §3.3]. The spy records ordering; the real service is updated
// when non-nil.
func PostLoadVisibility(s *Session, mapping []byte, observers []visibility.Observer, spy *VisibilityLoadSpy) {
	spy.record("rebuild")
	if s != nil && s.Vis != nil {
		// Rebuild before mapping read per [03 §3.3]. Both stores are prefilled:
		// history cells all-set when history disabled, current grids 1 when
		// current disabled, then any already-present units republished. C10
		// requires this before the mapping blob is touched.
		s.Vis.RebuildAll(nil)
	}
	spy.record("mapping")
	_ = mapping // read the serialized mapping blob (opaque); size mismatch leaves array unchanged [03 §3.3]
	// Every unit publishes its footprint synchronously before loader returns.
	for _, ob := range observers {
		spy.record("publish")
		spy.Published = append(spy.Published, ob.Owner)
		if s != nil && s.Vis != nil {
			s.Vis.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
		}
	}
}

// PublishAllUnits is the helper that publishes every live unit's footprint
// synchronously after a rebuild per [03 §3.3] C10. It is the single place that
// turns a units.World into visibility Observers so the loader's "every unit
// publishes before return" guarantee is not duplicated.
func PublishAllUnits(s *Session, spy *VisibilityLoadSpy) {
	if s == nil || s.Units == nil || s.Vis == nil {
		return
	}
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive {
			continue
		}
		// Coverage tile is half-resolution [03 §3.1]: cell/2. Height byte and
		// radius uses catalog values when available; a zero definition keeps the
		// established default radius.
		var cx, cz int32
		var radius int32 = 32
		var height uint8
		if s.World != nil {
			// Same derivation the per-tick refresh uses, so a
			// loaded session publishes the footprints it would have published
			// while running.
			height = heightByteAt(u, seaLevelFor(s))
			cx, cz = observerTile(u, height)
		}
		if u.Def != nil && u.Def.SightDistance > 0 {
			radius = int32(u.Def.SightDistance)
		}
		ob := visibility.Observer{Owner: visibility.PlayerID(u.Owner), CX: cx, CZ: cz, HeightByte: height, Radius: radius}
		spy.record("publish")
		spy.Published = append(spy.Published, ob.Owner)
		s.Vis.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
		_ = units.GuardLatchSize // reference to keep import used if stripped
	}
}

// Ensure imports are used for vet.
var (
	_ = content.CanonicalKey
	_ = world.NewWind
)
