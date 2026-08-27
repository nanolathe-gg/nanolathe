package session

import (
	"fmt"
	"strings"
	"sync"

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
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// cobLoader is the per-process cache for COB programs [04 §4.1][P1-I01].
// It is shared across sessions but never mutated during a tick (I1).
var globalCobLoader = cob.NewCachedLoader()

// parsedModelEntry retains one immutable model and the winning VFS
// provenance for an authored ObjectName/provider identity. Models are safe to
// share between sessions; VM piece state remains per-unit in cob.Binding.
type parsedModelEntry struct {
	model *model.Model
	prov  vfs.Provenance
}

type authoredModelKey struct {
	Identity     string
	LogicalPath  string
	OriginalPath string
	ProviderType string
	SourcePath   string
	MountRoot    string
	Priority     int
	MountOrder   int
}

var parsedModels = struct {
	sync.Mutex
	byProvider map[authoredModelKey]parsedModelEntry
}{byProvider: make(map[authoredModelKey]parsedModelEntry)}

func loadAuthoredModel(fs vfs.FSOps, objectName string) (*model.Model, vfs.Provenance, error) {
	identity := content.CanonicalKey(strings.TrimSpace(objectName))
	if identity == "" {
		return nil, vfs.Provenance{}, fmt.Errorf("session: unit has empty ObjectName")
	}
	path := "objects3d/" + identity + ".3do"
	info, err := fs.Stat(path)
	if err != nil {
		return nil, vfs.Provenance{}, fmt.Errorf("session: model %q unavailable: %w", path, err)
	}
	if info.IsDir {
		return nil, info.Source, fmt.Errorf("session: model %q is a directory (provider %s)", path, info.Source.ProviderID())
	}
	key := authoredModelKey{
		Identity: identity, LogicalPath: info.Source.LogicalPath,
		OriginalPath: info.Source.OriginalPath, ProviderType: info.Source.ProviderType,
		SourcePath: info.Source.SourcePath, MountRoot: info.Source.MountRoot,
		Priority: info.Source.Priority, MountOrder: info.Source.MountOrder,
	}
	parsedModels.Lock()
	if entry, ok := parsedModels.byProvider[key]; ok {
		parsedModels.Unlock()
		return entry.model, entry.prov, nil
	}
	parsedModels.Unlock()
	loaded, err := model.Load(fs, path)
	if err != nil {
		return nil, info.Source, fmt.Errorf("session: model %q from %s: %w", path, info.Source.ProviderID(), err)
	}
	if loaded == nil {
		return nil, info.Source, fmt.Errorf("session: model %q from %s is nil", path, info.Source.ProviderID())
	}
	parsedModels.Lock()
	if entry, ok := parsedModels.byProvider[key]; ok {
		parsedModels.Unlock()
		return entry.model, entry.prov, nil
	}
	parsedModels.byProvider[key] = parsedModelEntry{model: loaded, prov: info.Source}
	entry := parsedModels.byProvider[key]
	parsedModels.Unlock()
	return entry.model, entry.prov, nil
}

// cobPresentationSink admits only already-resolved COB events. It supplies
// the originating unit identity while the collector assigns sequence/order.
type cobPresentationSink struct {
	session  *Session
	source   pool.Handle
	pieceMap []int
}

// SetCOBPieceMap is called by strict binding before mode-I Create. The map is
// immutable after that point and translates VM/COB indices to authored model
// indices at the presentation boundary.
func (s *cobPresentationSink) SetCOBPieceMap(pieceMap []int) {
	if s == nil {
		return
	}
	s.pieceMap = append(s.pieceMap[:0], pieceMap...)
}

func (s *cobPresentationSink) EmitCOBEvent(ev cob.PresentationEvent) {
	if s.session == nil || s.session.Presentation == nil {
		return
	}
	if ev.Piece < 0 || int(ev.Piece) >= len(s.pieceMap) || s.pieceMap[ev.Piece] < 0 {
		// Strict binding diagnostics already reject unresolved pieces. This is a
		// defensive presentation drop for a malformed producer event; never
		// invent a root/model index [I6].
		return
	}
	tick := uint32(0)
	if s.session.Clock != nil {
		tick = s.session.Clock.GlobalTick
	}
	e := frame.Event{Tick: tick, Source: s.source, Piece: int32(s.pieceMap[ev.Piece]), SFXType: ev.SFXType, SFXClass: frame.SFXClass(ev.SFXClass), X: ev.Source[0], Y: ev.Source[1], Z: ev.Source[2], TargetX: ev.Target[0], TargetY: ev.Target[1], TargetZ: ev.Target[2]}
	// Selector is an authored effect discriminator when the producer supplied
	// one. Negative/absent selectors remain unknown; EffectID is unsigned at
	// the snapshot boundary, so never convert the unresolved sentinel [I9].
	if ev.Selector >= 0 {
		e.EffectID = uint32(ev.Selector)
	}
	switch ev.Kind {
	case cob.PresentationSFX:
		s.session.Presentation.EmitCOBSFX(e)
	case cob.PresentationNano:
		s.session.Presentation.EmitNanolathe(e)
	case cob.PresentationMuzzle:
		s.session.Presentation.EmitMuzzleFlash(e)
	case cob.PresentationSmoke:
		s.session.Presentation.EmitSmokeStart(e)
	case cob.PresentationTrail:
		s.session.Presentation.EmitProjectileTrail(e)
	case cob.PresentationImpact:
		s.session.Presentation.EmitImpact(e)
	}
}

func (s *Session) bindUnitCOB(fs vfs.FSOps, u *units.Unit) error {
	if s == nil || u == nil || u.Def == nil {
		return fmt.Errorf("session: cannot bind nil unit")
	}
	mdl, prov, err := loadAuthoredModel(fs, u.Def.ObjectName)
	if err != nil {
		return fmt.Errorf("unit %q model %s: %w", u.Def.UnitName, prov.ProviderID(), err)
	}
	sink := &cobPresentationSink{session: s, source: u.Handle}
	visible := func(_ int, _ int32) bool {
		// This is the established unit-level gameplay visibility gate used by
		// combat acquisition; it never mutates authoritative state [03 §3.2].
		return s.IsUnitVisible(localPlayerForSession(s), u)
	}
	binding, err := units.BindCOBWithPortsAndVisibilityForUnit(fs, u, mdl, s.SimRNG(), sink, visible)
	if err != nil {
		return fmt.Errorf("unit %q model %q script binding: %w", u.Def.UnitName, mdl.Name, err)
	}
	if err := u.AttachCOBBinding(binding); err != nil {
		return fmt.Errorf("unit %q attach strict COB: %w", u.Def.UnitName, err)
	}
	return nil
}

// strictCatalog compiles a single immutable catalog from the VFS. It never
// fabricates an empty fallback. [02 §5]
func strictCatalog(fs vfs.FSOps, cat *content.Catalog) (*content.Catalog, error) {
	return strictCatalogWithProgress(fs, cat, nil)
}

func strictCatalogWithProgress(fs vfs.FSOps, cat *content.Catalog, report content.Progress) (*content.Catalog, error) {
	if cat != nil {
		if err := cat.Validate(); err != nil {
			return nil, fmt.Errorf("session: catalog validate: %w", err)
		}
		return cat, nil
	}
	if fs == nil {
		return nil, fmt.Errorf("session: nil filesystem and nil catalog [02 §5]")
	}
	compiled, err := content.CompileWithProgress(fs, report)
	if err != nil {
		return nil, fmt.Errorf("session: catalog compile: %w", err)
	}
	if err := compiled.Validate(); err != nil {
		return nil, fmt.Errorf("session: catalog validate: %w", err)
	}
	return compiled, nil
}

// loadTerrainStrict loads terrain for the given mission's terrain key and
// applies the selected schema including surface metal before any SampleMetal.
// [03 §2.2][05 "Terrain metal extraction"] Callers must not sample metal before
// this point [C14].
func loadTerrainStrict(fs vfs.FSOps, cat *content.Catalog, m *mission.Mission) (*world.Terrain, error) {
	if fs == nil {
		return nil, fmt.Errorf("session: nil filesystem for terrain")
	}
	if cat == nil {
		return nil, fmt.Errorf("session: nil catalog for terrain")
	}
	if m == nil {
		return nil, fmt.Errorf("session: nil mission for terrain")
	}
	key := m.TerrainKey
	if key == "" {
		return nil, fmt.Errorf("session: empty terrain key")
	}
	terrain, err := world.Load(fs, cat, key)
	if err != nil {
		return nil, fmt.Errorf("session: terrain %q: %w", key, err)
	}
	if err := applySchemaStrict(terrain, cat, m); err != nil {
		return nil, err
	}
	return terrain, nil
}

// applySchemaStrict seeds per-cell metal from the mission's selected schema
// before any extractor samples. [05 "Terrain metal extraction"] [P1-15]
func applySchemaStrict(terrain *world.Terrain, cat *content.Catalog, m *mission.Mission) error {
	if terrain == nil {
		return fmt.Errorf("session: nil terrain for ApplySchema")
	}
	if m == nil {
		return fmt.Errorf("session: nil mission for ApplySchema")
	}
	if cat == nil || len(cat.Maps) == 0 {
		return fmt.Errorf("session: missing map metadata for terrain %q [02 \"Map files\"]", m.TerrainKey)
	}
	key := content.CanonicalKey(m.TerrainKey)
	mh, ok := cat.Maps[key]
	if !ok || mh == nil {
		return fmt.Errorf("session: map header %q not found [02 \"Map files\"]", m.TerrainKey)
	}
	idx := -1
	for i, sch := range mh.Schemas {
		if sch.Name == m.Schema.Name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("session: schema %q not found for map %q", m.Schema.Name, m.TerrainKey)
	}
	if err := terrain.ApplySchema(mh, idx); err != nil {
		return fmt.Errorf("session: ApplySchema: %w", err)
	}
	return nil
}

// newSlicedWorld creates the retail sliced unit pool using the catalog
// definition count. [01 §6.1][P0-16] Use units.NewSliced, never New(600).
func newSlicedWorld(cat *content.Catalog) (*units.World, error) {
	if cat == nil {
		return nil, fmt.Errorf("session: nil catalog for unit pool")
	}
	n := len(cat.Units)
	if n <= 0 {
		return nil, fmt.Errorf("session: catalog has no unit definitions [02 §5]")
	}
	w := units.NewSliced(n, cat)
	if w == nil {
		return nil, fmt.Errorf("session: failed to create sliced pool")
	}
	if !w.IsSliced() {
		return nil, fmt.Errorf("session: pool not sliced [P0-16]")
	}
	return w, nil
}

// newSlicedWorldWithCOB creates the sliced pool and installs the COB loader [04 §4.1][P1-I01].
func newSlicedWorldWithCOB(cat *content.Catalog, fs vfs.FSOps) (*units.World, error) {
	if fs == nil {
		return nil, fmt.Errorf("session: missing filesystem for COB binding [04 §4.1]")
	}
	w, err := newSlicedWorld(cat)
	if err != nil {
		return nil, err
	}
	w.SetCOBSource(fs, globalCobLoader)
	return w, nil
}

// ensureCOBForAll verifies that every production unit has the strict binding
// installed by createAndBindServices. It never repairs a missing binding with
// an empty VM.
func ensureCOBForAll(s *Session, fs vfs.FSOps) error {
	if s == nil {
		return fmt.Errorf("session: nil session while checking COB bindings [04 §4.1]")
	}
	if s.Units == nil {
		return fmt.Errorf("session: missing Units while checking COB bindings [04 §4.1]")
	}
	if fs == nil {
		return fmt.Errorf("session: missing filesystem while checking COB bindings [04 §4.1]")
	}
	if !s.Units.HasCOBBinder() {
		return fmt.Errorf("session: missing COB binder after battle entry [04 §4.1]")
	}
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if u.COBBinding() == nil {
			return fmt.Errorf("session: unit %d has no strict COB binding after battle entry", u.Handle)
		}
	}
	return nil
}

// requireGlobalRNGStreams enforces the process setup contract at strict
// session boundaries. Retail seeds both streams before battle entry; a
// missing stream must be reported rather than replaced with a seed-zero
// stream [01 §7.1][01 §7.2].
func requireGlobalRNGStreams() error {
	if rng.Global.Sim == nil {
		return fmt.Errorf("session: missing global simulation RNG stream [01 §7.1]")
	}
	if rng.Global.Crt == nil {
		return fmt.Errorf("session: missing global CRT RNG stream [01 §7.2]")
	}
	return nil
}

// createAndBindServices creates every required authoritative service and binds
// cross-service ports explicitly. It is the single topology site used by both
// skirmish and campaign. [08 "Placement and battle entry"] [04 §7.2]
func createAndBindServices(s *Session) error {
	if s == nil {
		return fmt.Errorf("session: nil session")
	}
	if s.Catalog == nil {
		return fmt.Errorf("session: missing Catalog for service wiring [02 §5]")
	}
	if s.World == nil {
		return fmt.Errorf("session: missing World for service wiring [03 §2.2]")
	}
	if s.Units == nil {
		return fmt.Errorf("session: missing Units for service wiring [01 §6.1]")
	}
	if err := requireGlobalRNGStreams(); err != nil {
		return err
	}
	if s.Wind == nil {
		return fmt.Errorf("session: missing Wind for service wiring [01 §7.3]")
	}
	cobFS, cobLoader := s.Units.COBSource()
	if (cobFS == nil) != (cobLoader == nil) {
		return fmt.Errorf("session: incomplete COB source for service wiring [04 §4.1]")
	}
	if s.Presentation == nil {
		s.Presentation = frame.NewEventBuffer(frame.Limits{})
	}
	if s.Effects == nil {
		// The render pool is the sole active-effect owner. Presentation only
		// admits detached event views and reads its immutable snapshot [03 §1].
		s.Effects = render.NewEffectServiceWithPool(render.EffectCapacity, &render.FixedEffectPool{})
	}
	// Worlds with an authored source use one binder before battle entry so
	// scenario, construction, and forced-slot creation resolve the same model
	// and script path [04 §4.1].
	if cobFS != nil {
		s.Units.SetCOBBinder(func(u *units.Unit) error { return s.bindUnitCOB(cobFS, u) })
		if !s.Units.HasCOBBinder() {
			return fmt.Errorf("session: missing COB binder for service wiring [04 §4.1]")
		}
	}
	if s.Econ == nil {
		s.Econ = &economy.Service{}
	}
	// Bind authoritative wind and terrain to the one ledger per [05] [P1-I04].
	// All other producers must go through bucket Production/Requested/Accepted;
	// only CreditSpawn (spawn) may write directly to Stock outside the ledger.
	s.Econ.Wind = s.Wind
	s.Econ.Terrain = s.World
	s.Econ.CloakCost = func(u *units.Unit) float32 {
		if u == nil {
			return 0
		}
		return u.CloakCost() // [05 "Cloak debit"] stationary vs moving [P1-I04]
	}
	// Features [05] with terrain, sim, crt, wind
	if s.Features == nil {
		sim := rng.Global.Sim
		crt := rng.Global.Crt
		s.Features = features.NewService(s.World, sim, crt, s.Wind)
		s.Features.PopulateFromTerrain()
	} else if s.Features.Terrain != s.World {
		return fmt.Errorf("session: Features.Terrain mismatch")
	} else {
		// Existing service but world may have been swapped (e.g. load); ensure terrain features are present
		s.Features.PopulateFromTerrain()
	}
	// Visibility [03 §3] dimensions from terrain, mode respects SkirmishConfig
	// Mapping 0 → history disabled (word fills all bits), LineOfSight 0 → current disabled (byte grids fill 1), LOSType 0 → sprite-mask [08 "Skirmish configuration"][03 §3.1] C2.
	mode := visibilityModeForSession(s)
	if s.Vis == nil {
		s.Vis = visibility.New(s.World, mode)
	} else {
		s.Vis.SetMode(mode)
	}
	if s.Catalog != nil {
		if s.Catalog.Sight != nil {
			s.Vis.SetShapes(s.Catalog.Sight)
		}
		if s.Catalog.LOS != nil {
			s.Vis.SetRayTables(s.Catalog.LOS)
		}
	}
	s.Vis.SetLocal(visibility.PlayerID(localPlayerForSession(s)))
	// Sensor backing surfaces are presentation-only (minimap) and never author the LOS word mask [03 §3.4] C11.
	if s.sensorSurfaces == nil {
		s.sensorSurfaces = &sensorSurfacesImpl{}
	}
	s.Vis.SetSurfaces(s.sensorSurfaces)
	if s.visStatus == nil {
		s.visStatus = make(map[int]uint32)
	}
	if s.visDecloak == nil {
		s.visDecloak = make(map[int]uint32)
	}
	// Canonical visibility predicate for combat [03 §3.2] C8 P0-11 — single gameplay gate.
	// Per-session isolated: was package-global combat.VisibilityHook, now Service.Visibility [RS-P0-018][INVARIANTS I1][I6].
	if s.Combat != nil {
		s.Combat.Visibility = func(viewer visibility.PlayerID, target visibility.Target) bool {
			if s.Vis == nil {
				return false
			}
			return s.Vis.IsVisible(viewer, target)
		}
	}
	// Movement [04 §8] with occupancy grid and compiled classes
	if s.Movement == nil {
		grid := movement.NewOccupancyGrid()
		fallback := movement.Profile{FootPrintX: 1, FootPrintZ: 1}
		s.Movement = movement.NewSystem(s.World, fallback, grid)
	}
	if s.Movement.Terrain != s.World {
		return fmt.Errorf("session: Movement.Terrain mismatch")
	}
	// Bind movement classes explicitly [02 "Movement class record"]
	s.Movement.SetClasses(s.Catalog.Movement)
	// Path is alias to movement scheduler; one scheduler only [04 §7.3]
	if s.Movement.Scheduler == nil {
		return fmt.Errorf("session: Movement.Scheduler nil")
	}
	s.Path = s.Movement.Scheduler
	// Construction [05]
	if s.Build == nil {
		s.Build = construction.NewService(s.World, s.Catalog, s.Units, s.Econ)
	}
	// Construction queries the immutable model retained by each strict COB
	// binding. This keeps factory exit placement and mobile QueryNanoPiece on
	// the authored model identity, including future products.
	s.Build.ModelForFactory = func(u *units.Unit) *model.Model {
		if u == nil || u.COBBinding() == nil {
			return nil
		}
		return u.COBBinding().Model
	}
	s.Build.ModelForUnit = func(u *units.Unit) *model.Model {
		if u == nil || u.COBBinding() == nil {
			return nil
		}
		return u.COBBinding().Model
	}
	s.Build.Presentation = s.Presentation
	// Walk-to-site uses normal Move_Ground machinery [04 §3.4][R-P0-06].
	// Bind the movement system so mobile builders walk into nano range before state 2.
	s.Build.Movement = s.Movement
	// Placement release is an independent lifecycle observer. The primary
	// OnDeath hook remains owned by the session loop for triggers/corpse/Killed;
	// this observer only releases unfinished construction occupancy once.
	priorDeathExtra := s.Units.OnDeathExtra
	s.Units.OnDeathExtra = func(h pool.Handle, cause units.DeathCause, u *units.Unit) {
		if priorDeathExtra != nil {
			priorDeathExtra(h, cause, u)
		}
		if s.Build != nil {
			s.Build.ReleasePlacement(h)
		}
	}
	// Combat [06] sole projectile authority
	if s.Combat == nil {
		s.Combat = &combat.Service{}
	}
	// Combat emits immutable authoritative events in impact order. The
	// collector is presentation-only; EventUnitKilled remains a death/corpse
	// notification in Units.OnDeath and is not duplicated here.
	s.Combat.Events = func(ev combat.Event) {
		if s.Presentation == nil {
			return
		}
		pe := frame.Event{
			Tick: ev.Tick, Source: ev.Source, Target: ev.Target,
			X: ev.Position.X, Y: ev.Position.Y, Z: ev.Position.Z,
			Graphic: ev.Graphic, Magnitude: ev.Magnitude,
		}
		switch ev.Kind {
		case combat.EventShake:
			s.Presentation.EmitShake(pe)
		case combat.EventHitSound, combat.EventWaterSound:
			if ev.Sound != "" {
				_, _, _ = s.EmitWeaponHit(ev.Sound, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, ev.Kind == combat.EventWaterSound)
			}
		case combat.EventStartSound:
			// Start sound is emitted by the common initializer before Fire/RockUnit;
			// route the authored alias through the session-owned direct audio queue.
			if ev.Sound != "" {
				_, _, _ = s.EmitWeaponStart(ev.Sound, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z})
			}
		case combat.EventStartSmoke:
			// Target carries the projectile handle solely as presentation identity;
			// no authoritative state is read or mutated at this boundary.
			pe.EffectID = uint32(ev.Target)
			s.Presentation.EmitSmokeStart(pe)
		case combat.EventEndSmoke:
			s.Presentation.EmitSmokeEnd(pe)
		case combat.EventExplosion:
			s.Presentation.EmitExplosion(pe)
		case combat.EventWaterExplosion:
			s.Presentation.EmitWaterImpact(pe)
		case combat.EventProjectileImpact:
			s.Presentation.EmitImpact(pe)
		case combat.EventUnitKilled, combat.EventCorpse:
			// Death/corpse lifecycle is owned by Units.OnDeath exactly once;
			// a separate authored corpse event, when emitted, is presentation-only.
			if ev.Kind == combat.EventCorpse {
				s.Presentation.EmitCorpse(pe)
			}
		}
	}
	// Wire BuildWeapon stockpile admission to the authoritative economy
	// service so per-visit truncated cumulative costs are admitted via
	// economy.UnitBuckets and carry is retained across cancels [06 §11.1]
	// [P1-09 §4][P1-09 §5] I16.
	orders.SetStockpileEconomy(s.Econ)
	if s.Units != nil && s.Econ != nil {
		for _, u := range s.Units.Iter() {
			if u == nil {
				continue
			}
			if q := orders.QueueForUnit(u); q != nil {
				q.StockpileEconomy = s.Econ
			}
		}
	}
	// Ensure Clock and Snapshot are available (AI is fixed [10] per RS-02).
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	if s.Snapshot == nil {
		s.Snapshot = frame.NewBuffer()
	}
	if s.Mission == nil {
		return fmt.Errorf("session: missing Mission [08]")
	}
	// Audio is presentation-only but owned by session so events can queue
	// without client import cycle [03 §8.3][03 §8.4] I6. Init lazily if not yet.
	if s.AudioQueue == nil || s.AudioCache == nil || s.AudioMusic == nil {
		s.InitAudio(s.audioFS)
	}
	return nil
}

// visibilityModeForSession computes the LOS mode word from SkirmishConfig [08 "Skirmish configuration"][03 §3.1] C2.
// Mapping 0 disables history (word fills all bits), LineOfSight 0 disables current (byte grids fill 1), LOSType 0 selects sprite-mask.
func visibilityModeForSession(s *Session) visibility.Mode {
	if s == nil {
		return visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay
	}
	// Skirmish sessions honor the lobby mapping/LOS/LOSType fields.
	if s.Mission != nil && s.Mission.Type == mission.TypeSkirmish {
		var m visibility.Mode
		if s.Skirmish.Mapping != 0 {
			m |= visibility.ModeHistoryEnabled
		}
		if s.Skirmish.LineOfSight != 0 {
			m |= visibility.ModeCurrentEnabled
		}
		if s.Skirmish.LOSType != 0 {
			m |= visibility.ModeTerrainRay
		}
		return m
	}
	// Campaign and other sessions default to fully enabled LOS [03 §3.1].
	return visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay
}

// localPlayerForSession returns the local player slot for fog/sensor predicate [03 §3.2] C15.
// It derives from Session.LocalOwner, not zero default, and falls back to first human.
func localPlayerForSession(s *Session) int {
	if s == nil {
		return 0
	}
	if int(s.LocalOwner) < 10 && s.Econ != nil {
		p := &s.Econ.Players[int(s.LocalOwner)]
		if p.Exists && p.ControllerState == 1 && !p.IsObserver {
			return int(s.LocalOwner)
		}
	}
	// Fallback: first human player (ControllerState==1) [08 "Skirmish configuration"].
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			p := &s.Econ.Players[i]
			if p.Exists && p.ControllerState == 1 && !p.IsObserver {
				return i
			}
		}
	}
	return int(s.LocalOwner)
}

// RecalcLocalOwner recomputes LocalOwner from SkirmishConfig after save restore [08 "Skirmish configuration"].
func (s *Session) RecalcLocalOwner() {
	if s == nil {
		return
	}
	// Prefer economy human first, as after restore Skirmish may be stale 1v1 default
	// while economy holds the true topology with slot3 human.
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			p := &s.Econ.Players[i]
			if p.Exists && p.ControllerState == 1 && !p.IsObserver {
				s.LocalOwner = uint8(i)
				// Also update EnemyOwner to first hostile if needed.
				for j := 0; j < 10; j++ {
					q := &s.Econ.Players[j]
					if q.Exists && q.ControllerState == 2 && !s.Econ.Players[i].Allies[j] {
						s.EnemyOwner = uint8(j)
						break
					}
				}
				return
			}
		}
	}
	if s.Skirmish.NumPlayers > 0 {
		s.LocalOwner = uint8(LocalOwnerForConfig(s.Skirmish))
	}
}

// heightByteFor returns the observer height byte clamped 0..255 [03 §3.2] C5.
// It is the world Y high word (map pixel height) truncated to byte; negative clamps to 0.
// heightByteFor forms the LOS observer's emitter height byte [03 §3.2].
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// where worldY has already been clamped to `(SeaLevel+1)<<16` by the caller at
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// The addend is what makes the terrain-ray raster work at all: the horizon test
// admits a step only when its slope STRICTLY exceeds the retained horizon, so
// an observer whose height equals the ground under it retains a zero slope
// after its first step and every later step ties and is rejected. Sighting from
// the model's top gives the ray a negative slope to spend, which is also why a
// laser tower outranges a peewee at equal sightdistance.
func heightByteFor(u *units.Unit) uint8 {
	return heightByteAt(u, 0)
}

// sensorSurfacesImpl is the presentation-only sensor backing surfaces [03 §3.4] C11 P0-11.
// It is wiped each tick while the LOS word mask persists; radar/sonar/jammer never author the LOS mask.
type sensorSurfacesImpl struct {
	wipes    int
	sensor   [][3]int32
	radarJam [][3]int32
	sonarJam [][3]int32
}

func (r *sensorSurfacesImpl) Wipe() {
	r.wipes++
	r.sensor = r.sensor[:0]
	r.radarJam = r.radarJam[:0]
	r.sonarJam = r.sonarJam[:0]
}
func (r *sensorSurfacesImpl) Sensor(u, v, radius int32) {
	r.sensor = append(r.sensor, [3]int32{u, v, radius})
}
func (r *sensorSurfacesImpl) RadarJam(u, v, radius int32) {
	r.radarJam = append(r.radarJam, [3]int32{u, v, radius})
}
func (r *sensorSurfacesImpl) SonarJam(u, v, radius int32) {
	r.sonarJam = append(r.sonarJam, [3]int32{u, v, radius})
}

// ensureMovementForAll ensures per-unit movement state for every live unit.
func ensureMovementForAll(s *Session) {
	if s == nil || s.Movement == nil || s.Units == nil || s.Movement.Routes == nil {
		return
	}
	for _, u := range s.Units.Iter() {
		s.Movement.EnsureUnit(u)
	}
}

// ensure imports used
var (
	_ = content.CanonicalKey
	_ = world.NewWind
	_ = ai.Manager{}
)

// seaLevelFor is the map's sea-level byte, which the LOS writer clamps the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func seaLevelFor(s *Session) uint8 {
	if s == nil || s.World == nil {
		return 0
	}
	return s.World.SeaLevel
}
