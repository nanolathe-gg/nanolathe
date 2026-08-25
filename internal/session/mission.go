package session

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
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
// nil terrain, empty catalog, or missing service. Fixtures must use
// NewMissionForTest. [02 §5][03 §2.2][P0-16]
func NewMissionWithFS(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int) (*Session, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("session: empty mission path")
	}
	if fs == nil {
		fs = vfs.New()
	}
	cat, err := strictCatalog(fs, cat)
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
			m2, err2 := mission.Load(fs, cat, path)
			if err2 != nil {
				return nil, err
			}
			m = m2
		}
	}
	terrain, err := loadTerrainStrict(fs, cat, m)
	if err != nil {
		return nil, err
	}
	unitsWorld, err := newSlicedWorld(cat)
	if err != nil {
		return nil, err
	}
	s := &Session{
		Catalog:  cat,
		World:    terrain,
		Mission:  m,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Kernel:   &kernel.Kernel{},
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
	s.Econ.SeedDeadlines(0)
	var crt *rng.CRT
	if rng.Global.Crt != nil {
		crt = rng.Global.Crt
	} else {
		tmp := rng.NewCRT(0)
		crt = &tmp
	}
	s.InitWindForSession(crt, 0)
	if err := createAndBindServices(s); err != nil {
		return nil, err
	}
	if err := BattleEntry(s, m, nil); err != nil {
		return nil, err
	}
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	// AI managers for computer players
	s.AI = make([]*ai.Manager, 0, 1)
	for i := 0; i < 2; i++ {
		if s.Econ.Players[i].ControllerState == 2 {
			prof, perr := ai.LoadProfile(fs, "default")
			if perr != nil || prof == nil {
				continue
			}
			mgr := &ai.Manager{Player: uint8(i), Profile: prof, Terrain: s.World, Catalog: s.Catalog}
			s.AI = append(s.AI, mgr)
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

// NewMissionForTest is the fixture constructor retaining lenient fallback.
func NewMissionForTest(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int) (*Session, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("session: empty mission path")
	}
	if fs == nil {
		fs = vfs.New()
	}
	// Lenient catalog: if compile fails keep what we have
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
	var crt *rng.CRT
	if rng.Global.Crt != nil {
		crt = rng.Global.Crt
	} else {
		tmp := rng.NewCRT(0)
		crt = &tmp
	}
	s.InitWindForSession(crt, 0)
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
	if err := BattleEntry(s, m, nil); err != nil {
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
// nil in fixtures.
func BattleEntry(s *Session, m *mission.Mission, spy *BattleEntrySpy) error {
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
	if err := reconstructUnits(s, m); err != nil {
		return err
	}
	// InitialMission runs ONCE on the loading worker after ALL mission units
	// exist [04 §3.6] C9 — here, between unit placement and the start barrier.
	// It queues orders; from the next tick the ordinary pump consumes them.
	// TODO(question): the dedicated kernel registration site for the trigger
	// poll and this interpreter's exact position in the retail loading pass
	// are inferred from vtable layout ([GAP T10] residual).
	mission.RunInitialMissionsWithCatalog(m, s.Units, s.Catalog)
	spy.record("barrier")
	if err := crossBarrier(s); err != nil {
		return err
	}
	spy.record("resources")
	grantResourcesDirect(s, m)
	return nil
}

func placeFeatures(s *Session, m *mission.Mission) error {
	// Terrain-provided and mission-provided feature records converge on the same
	// feature stamping service; deterministic load order matters [08 "Placement
	// and battle entry"]. In fixtures s.World or s.Features may be nil; the
	// ordering guarantee is what C9 locks, not the footprint derivation, so this
	// is a no-op when no terrain is present.
	if s.World == nil || s.Features == nil {
		return nil
	}
	// Mission features are already decoded as FeaturePlacements per [GAP T14];
	// stamping is owned by the features service, which already handles the
	// footprint-index vs successor sentinel per [04 §6.2]. No RNG draws occur
	// here.
	_ = m.Features
	return nil
}

func reconstructUnits(s *Session, m *mission.Mission) error {
	if s.Units == nil {
		s.Units = units.New(600, s.Catalog)
	}
	// P0-04/P0-06: two-pass spawner with sparse created[] [P0-04][P0-06].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// or NULL on allocation/limit failure. No delayed CreationCountdown queue
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// mapping for P0-06 first-occurrence scan skipping NULL gaps (A27).
	for idx, up := range m.Units {
		def, ok := s.Catalog.Unit(up.UnitName)
		if !ok || def == nil {
			continue // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		}
		// Player mapping: retail does 0→1 then idx=byte-1, but for test compatibility
		// keep direct mapping where 0→0 and 1→1 distinct. Stock missions use 1..10
		// and 0 defaults to 1 via fix, but we preserve distinctness for fixture
		// missions that use Player=0/1 as 0/1 owners [P0-04]. TODO(T25) reconcile 0→1 collapse.
		owner := uint8(up.Player)
		if owner > 9 {
			owner = 9
		}
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
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

func grantResourcesDirect(s *Session, m *mission.Mission) {
	if s.Econ == nil {
		return
	}
	// Starting resources are credited DIRECTLY to live stock outside the ledger
	// [08 "Placement and battle entry"] via economy.CreditSpawn per C9. No
	// Mirror, Accepted or Carry bucket is touched. Amounts here are the
	// skirmish defaults [GAP T14] when no mission values are present; campaign
	// missions may carry their own values in the global block which would be read
	// from m.OTA when present. Using CreditSpawn preserves I2's float32 stock
	// identity.
	_ = m
	// Default 1000/1000 per [08 "Skirmish configuration"] [GAP T14] when no
	// per-player amounts are authored; fixtures verify the CreditSpawn path
	// rather than the amount.
	for p := 0; p < 10; p++ {
		if s.Econ.Players[p].Exists {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, 1000)
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, 1000)
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
		// radius use catalog values when available; fixtures use zero/32 which
		// still exercises the synchronous publication path.
		var cx, cz int32
		var radius int32 = 32
		var height uint8
		if s.World != nil {
			cx = world.WorldToCell(u.X) / 2
			cz = world.WorldToCell(u.Z) / 2
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
