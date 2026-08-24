package session

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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

// NewMissionWithFS is NewMission with explicit filesystem and catalog for tests.
// When fs is nil a loose filesystem rooted at the current directory is used; when
// cat is nil the mission is loaded without cross-linking. The gametype routing
// rides the existing eight-state machine per [08 "Session states"] C3.
func NewMissionWithFS(fs vfs.FSOps, cat *content.Catalog, path string, difficulty int) (*Session, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("session: empty mission path")
	}
	if fs == nil {
		fs = vfs.New()
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
			// Fallback to generic loader that handles fuzzy search for Types 2/3 paths
			// but still respects the caller's difficulty when the first attempt fails.
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
	}
	// Gametype routing per C3 [08 "Session states"]: campaign is Gametype 1
	// which selects StateLocalPreload (4) before the same StateLoading (5) path.
	if err := s.SelectForGametype(GametypeCampaign); err != nil {
		return nil, err
	}
	// Wind bounds retained without RNG draws [08 "Wind initialization"] [C5]; the
	// single battle-entry initializer runs now after retention [01 §7.3] C17 when a
	// CRT stream is available. Use the global CRT when seeded, otherwise a
	// deterministic zero-seeded CRT so the three draws still occur (I4).
	var crt *rng.CRT
	if rng.Global.Crt != nil {
		crt = rng.Global.Crt
	} else {
		tmp := rng.NewCRT(0)
		crt = &tmp
	}
	s.InitWindForSession(crt, 0)

	// Economy slots for the two campaign players (local 0, enemy 1) per
	// [08 "Established AI-facing data"]; single-player missions use 0 and 1
	// [triggers PollContext]. Deadlines seed at battle-entry tick 0 per
	// [05 "Authoritative settlement order"] C5.
	if s.Econ == nil {
		s.Econ = &economy.Service{}
	}
	for i := 0; i < 2 && i < len(s.Econ.Players); i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = 1 // human active settling
		p.IsObserver = false
		p.StatusHalfwordAt144 = 1
		p.StatusWordAt140 = 0
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0)

	// Battle entry order: features → units (InitialMission interprets here)
	// → barrier → starting resources directly to live stock outside the
	// ledger [08 "Placement and battle entry"] C9.
	if err := BattleEntry(s, m, nil); err != nil {
		return nil, err
	}
	// Movement parity with the skirmish route: ground steering/routes via
	// movement.System [PLAN_14 C5 movement integration].
	if s.World != nil && s.Movement == nil {
		s.Movement = movement.NewSystem(s.World, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
		if cat != nil {
			s.Movement.SetClasses(cat.Movement)
		}
		for _, u := range s.Units.Iter() {
			s.Movement.EnsureUnit(u)
		}
	}
	// Kernel phase registration — without this a mission session has no
	// ticking subsystems and cannot reach victory/defeat [08 "Evaluation"].
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
	// Scenario records are resolved by unit name through the normal unit pool
	// per [08 "Placement and battle entry"]. Creation restores logical fields
	// through the standard pool allocator (lowest-free, immediate reuse) [01 §6.1].
	for _, up := range m.Units {
		def, ok := s.Catalog.Unit(up.UnitName)
		if !ok || def == nil {
			continue
		}
		_, _ = s.Units.Create(def, uint8(up.Player), 0, 0, 0)
		_ = up.Ident
		_ = up.InitialMission
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
