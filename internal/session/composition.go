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
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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
// The session reference backs the strip producers this sink fronts
// [R-STRIP-01 §1]; it is authoritative sim state appended at the producer,
// independent of the presentation emission below.
type cobPresentationSink struct {
	publication *publicationState
	clock       *clock.State
	source      pool.Handle
	session     *Session
	pieceMap    []int
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

// pieceWorldPos resolves the world position of one COB piece of the sink's
// source unit — the same unit-origin-plus-composed-piece path the weapon
// muzzle uses [06 §4.1]. The emit-sfx producers spawn at the piece's world
// position [R-STRIP-01 §1 strips 2/7/9][04 §4.4]. Unresolvable pieces
// (dangling unit, unresolved binding) report false and the caller drops the
// strip append rather than inventing a position [I9].
func (s *cobPresentationSink) pieceWorldPos(cobPiece int) ([3]numeric.Fixed, bool) {
	var zero [3]numeric.Fixed
	if s == nil || s.session == nil || s.session.Units == nil {
		return zero, false
	}
	u := s.session.Units.Unit(s.source)
	if u == nil || u.COBBinding() == nil {
		return zero, false
	}
	origin, ok := u.COBBinding().ComposePiece(cobPiece, u.Move.Heading, u.Move.Pitch, u.Move.Bank)
	if !ok {
		return zero, false
	}
	return [3]numeric.Fixed{u.X.Add(origin[0]), u.Y.Add(origin[1]), u.Z.Add(origin[2])}, true
}

// emitSFXStripProducers is the session edge of retail's emit-sfx type switch
// [R-STRIP-01 §1 strips 2/7/9][04 §4.4]. The switch dispatches on the
// emit-sfx type word: vector types 0–5 are piece-direction effects (0/1 the
// wake pair → strip-7 flame-stream trail; 2/3 the thrust pair → strip-2
// sprinkle at 16- then 8-tick puff spacing; 4/5 the same pair with the two
// piece points swapped), and the point types use the piece world position
// (0x101 white smoke and 0x102 black smoke → strip-9 smoke; 0x103 spawns at
// the water line and lands a strip-7 sprinkle). Every case is gated on local
// visibility upstream of this sink, exactly as retail gates the whole switch
// [04 §4.4]; the appended strip objects are authoritative sim state swept in
// phase 11 [R-STRIP-01 §2].
//
// TODO(question): the piece's second effect vertex — the direction-vertex
// point that vector types pass beside the piece origin. Its derivation from
// the piece's model geometry is untraced, so the wake pair (0/1, whose trail
// flies from the origin to that vertex) and the swapped thrust pair (4/5,
// which spawns at the vertex) are left unwired rather than given an invented
// target; a trace of the piece record's second vertex would settle both.
func (s *cobPresentationSink) emitSFXStripProducers(ev cob.PresentationEvent) {
	if s == nil || s.session == nil || s.session.strips == nil {
		return
	}
	pos, ok := s.pieceWorldPos(ev.Piece)
	if !ok {
		return
	}
	switch ev.SFXType {
	case 0x101, 0x102:
		// White and black smoke point types: the emit-sfx switch's strip-9
		// smoke sites [R-STRIP-01 §1 strip 9]. Each appends one smoke
		// container whose constructor spawns its first puff immediately;
		// the site's container window and GAF variant selection are
		// unestablished (see appendStripSmokePuffer's TODO).
		s.session.appendStripSmokePuffer(9, pos, 0, smokeDefaultFrameDelay)
	case 0x103:
		// Sub-bubbles: the spawn height is forced to the water line and the
		// sprinkle variant lands on strip 7 with 8-tick spacing [04 §4.4]
		// [R-STRIP-01 §1 strip 7].
		if s.session.World != nil {
			pos[1] = s.session.World.SeaLevelWorld()
		}
		s.session.appendStripSprinkle(7, pos, 8, 0)
	case 2, 3:
		// The thrust pair: strip-2 sprinkle, 16-tick spacing for type 2 and
		// 8-tick for type 3 [R-STRIP-01 §1 strip 2].
		spacing := int32(16)
		if ev.SFXType == 3 {
			spacing = 8
		}
		s.session.appendStripSprinkle(2, pos, spacing, 1)
	case 0, 1, 4, 5:
		// Unwired pending the second effect vertex (see the TODO above):
		// 0/1 lay a strip-7 trail from the piece origin to the vertex, and
		// 4/5 spawn the strip-2 sprinkle at the swapped point
		// [R-STRIP-01 §1 strips 2/7].
	}
}

func (s *cobPresentationSink) EmitCOBEvent(ev cob.PresentationEvent) {
	if s == nil || s.publication == nil || s.publication.events == nil {
		return
	}
	if ev.Piece < 0 || int(ev.Piece) >= len(s.pieceMap) || s.pieceMap[ev.Piece] < 0 {
		// Strict binding diagnostics already reject unresolved pieces. This is a
		// defensive presentation drop for a malformed producer event; never
		// invent a root/model index [I6].
		return
	}
	tick := uint32(0)
	if s.clock != nil {
		tick = s.clock.GlobalTick
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
		s.publication.events.EmitCOBSFX(e)
		// The emit-sfx type switch is a strip producer family: its vector
		// and point cases append strip-2/7/9 objects [R-STRIP-01 §1 strips
		// 2/7/9][04 §4.4]. Authoritative sim state appended at the
		// producer; the presentation emission above is separate [I6].
		s.emitSFXStripProducers(ev)
	case cob.PresentationNano:
		// Script-emitted nano events are beam-family strip-6 effects with the
		// same geometry gate as construction/reclaim work [03 §5.5][R-P0-06 §5].
		e.Producer = frame.ProducerBeam
		e.PaletteRow = 6
		e.NanolatheGeometryKnown = true
		s.publication.events.EmitNanolathe(e)
		// Strip-6 producer [R-STRIP-01 §1 strip 6]: nano records append to
		// strip 6 of the ten-strip family, one emitter per accepted
		// submission [03 §5.5][05 "Established — the record constructor and
		// allocator epilogue"]. The emitter is authoritative sim state; its
		// first five particles spawn here (thirty CRT draws at the producer)
		// and the rest advance in phase 11 [03 §5.5].
		if s.session != nil {
			s.session.appendStripNanoEmitter(
				[3]numeric.Fixed{e.X, e.Y, e.Z},
				[3]numeric.Fixed{e.TargetX, e.TargetY, e.TargetZ})
		}
	case cob.PresentationMuzzle:
		s.publication.events.EmitMuzzleFlash(e)
	case cob.PresentationSmoke:
		// The COB emit-sfx smoke point types (0x101 white / 0x102 black)
		// are strip-9 producers reached through the PresentationSFX switch
		// above, not through this Smoke kind [R-STRIP-01 §1 strip 9].
		s.publication.events.EmitSmokeStart(e)
	case cob.PresentationTrail:
		s.publication.events.EmitProjectileTrail(e)
	case cob.PresentationImpact:
		s.publication.events.EmitImpact(e)
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
	sink := &cobPresentationSink{publication: s.publication, clock: s.Clock, source: u.Handle, session: s}
	visible := func(_ int, _ int32) bool {
		// This is the established unit-level gameplay visibility gate used by
		// combat acquisition; it never mutates authoritative state [03 §3.2].
		return s.IsUnitVisible(localPlayerForSession(s), u)
	}
	registeredPlacement := false
	if !u.Def.BMCode && s.Build != nil {
		// Port 18 may run synchronously from COB Create. Install the transaction
		// and the exact unit-creation stamp before strict binding starts Create;
		// the callback therefore observes the cached placement and can commit the
		// bit before restamping [04 §4.7 port 18][04 R-COLL-01 §4].
		u.SetYardOpenTransaction(func(requested bool) {
			s.Build.YardOpenTransaction(u, requested)
		})
		if err := s.Build.RegisterBuildingPlacement(u); err != nil {
			return fmt.Errorf("unit %q building placement: %w", u.Def.UnitName, err)
		}
		registeredPlacement = true
	}
	binding, err := units.BindCOBWithPortsAndVisibilityForUnit(fs, u, mdl, s.SimRNG(), sink, visible)
	if err != nil {
		if registeredPlacement {
			s.Build.ReleasePlacement(u.Handle)
		}
		return fmt.Errorf("unit %q model %q script binding: %w", u.Def.UnitName, mdl.Name, err)
	}
	if err := u.AttachCOBBinding(binding); err != nil {
		if registeredPlacement {
			s.Build.ReleasePlacement(u.Handle)
		}
		return fmt.Errorf("unit %q attach strict COB: %w", u.Def.UnitName, err)
	}
	return nil
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
	// The uniform seed is not the whole story: indestructible metal-bearing
	// features overwrite the byte across their footprint, and that pass runs
	// after the feature stamps [05 R-FEAT-01 §7] — an explicit correction to
	// the earlier "canonical maps keep the uniform seed everywhere" reading of
	// [05 R-PROD-01 §6]. Terrain-file features are already stamped by
	// world.Load before this point, so the deposits they carry seed here.
	//
	// The pass runs once per placement source: here for the terrain-file
	// stamps, and again after each mission/skirmish feature-placement helper.
	// Re-running it is idempotent — every anchor rewrites the same byte over
	// the same footprint, and no reader of the byte sits between the calls —
	// so the composed result is the single trailing pass over the finished
	// plot that [05 R-FEAT-01 §7] describes.
	terrain.SeedFeatureMetalDeposits()
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
	return newBattleSlicedWorldWithCOB(cat, fs, 0, [pool.PlayerCount]uint32{})
}

// newBattleSlicedWorldWithCOB computes the complete player order once at
// battle entry and injects it into the sliced pool. The sort-key array is an
// explicit seam for the mode-3 player records; mode 0 is the identity wrapper
// used by fixture-only construction [R-P0-16-A].
func newBattleSlicedWorldWithCOB(cat *content.Catalog, fs vfs.FSOps, mode int, sortKeys [pool.PlayerCount]uint32) (*units.World, error) {
	if fs == nil {
		return nil, fmt.Errorf("session: missing filesystem for COB binding [04 §4.1]")
	}
	if cat == nil {
		return nil, fmt.Errorf("session: nil catalog for unit pool")
	}
	if len(cat.Units) <= 0 {
		return nil, fmt.Errorf("session: catalog has no unit definitions [02 §5]")
	}
	order := pool.PlayerPermutationForMode(mode, sortKeys)
	w, err := units.NewSlicedWithOrder(len(cat.Units), cat, order)
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

func (s *Session) newOrderBinding() *orders.QueueBinding {
	if s == nil {
		return nil
	}
	worldQueries := &orders.WorldQueryAdapter{
		LookupUnit: func(h pool.Handle) *units.Unit {
			if s.Units == nil {
				return nil
			}
			return s.Units.Unit(h)
		},
		Hostile: func(actor, target *units.Unit) bool {
			if actor == nil || target == nil {
				return false
			}
			if actor.Def != nil && target.Def != nil && actor.Def.Side != "" && target.Def.Side != "" {
				return actor.Def.Side != target.Def.Side
			}
			return actor.Owner != target.Owner
		},
		ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
			if s.Units == nil || visit == nil {
				return
			}
			stopped := false
			s.Units.VisitActiveSlots(func(v units.SlotVisit) {
				if stopped {
					return
				}
				if visit(v.Handle, v.Unit) {
					stopped = true
					return
				}
			})
		},
		LookupFeature: func(cx, cz int32) (orders.FeatureView, bool) {
			if s.Features == nil {
				return orders.FeatureView{}, false
			}
			inst := s.Features.InstanceAt(int(cx), int(cz))
			if inst == nil {
				return orders.FeatureView{}, false
			}
			id := uint16(world.PlotFeatureNone)
			if s.World != nil {
				if cell := s.World.PlotAt(cx, cz); cell != nil {
					id = cell.Feature()
				}
			}
			var key string
			if inst.Def != nil {
				key = inst.Def.CanonicalKey
			}
			return orders.FeatureView{ID: id, CX: int32(inst.CX), CZ: int32(inst.CZ), X: inst.X, Z: inst.Z, DefinitionKey: key}, true
		},
		ForEachFeature: func(visit func(orders.FeatureView) bool) {
			if s.Features == nil || visit == nil {
				return
			}
			for _, inst := range s.Features.Instances() {
				if inst == nil {
					continue
				}
				id := uint16(world.PlotFeatureNone)
				if s.World != nil {
					if cell := s.World.PlotAt(int32(inst.CX), int32(inst.CZ)); cell != nil {
						id = cell.Feature()
					}
				}
				var key string
				if inst.Def != nil {
					key = inst.Def.CanonicalKey
				}
				if visit(orders.FeatureView{ID: id, CX: int32(inst.CX), CZ: int32(inst.CZ), X: inst.X, Z: inst.Z, DefinitionKey: key}) {
					break
				}
			}
		},
		TerrainHeight: func(x, z numeric.Fixed) (numeric.Fixed, bool) {
			if s.World == nil {
				return 0, false
			}
			return s.World.HeightAt(x, z), true
		},
		SeaLevel: func() uint8 {
			if s.World == nil {
				return 0
			}
			return s.World.SeaLevel
		},
	}
	movementGoals := &orders.MovementGoalAdapter{}
	if s.Movement != nil {
		movementGoals.Ready = func() bool { return s.Movement != nil }
		movementGoals.InstallPoint = func(req orders.PointGoalRequest) bool {
			if req.Node == nil {
				return false
			}
			s.Movement.BindMoveGoal(req.Owner, req.Node, req.X, req.Z)
			return true
		}
		movementGoals.Release = func(node *orders.Node) bool {
			if node == nil {
				return false
			}
			s.Movement.ClearMoveGoal(node.Owner)
			return true
		}
	}
	return &orders.QueueBinding{
		StockpileEconomy: s.Econ,
		Lookup:           worldQueries.LookupUnit,
		Hostility:        worldQueries.Hostile,
		SimRNG:           s.SimRNG(),
		CurrentTick: func() uint32 {
			if s.Clock == nil {
				return 0
			}
			return s.Clock.GlobalTick
		},
		Movement: movementGoals,
		World:    worldQueries,
		Work: &orders.WorkAdapter{Ready: func() bool {
			return s.Build != nil
		}},
		Weapons: &orders.WeaponAdapter{Ready: func() bool {
			return s.Combat != nil
		}},
		Presentation: &orders.PresentationAdapter{Ready: func() bool {
			return s.publication != nil && s.publication.events != nil
		}},
	}
}

// bindOrderQueue installs the session-owned order context immediately after a
// constructor allocates a unit. The construction service retains the same
// binding for later product queues and queue replacement.
func (s *Session) bindOrderQueue(u *units.Unit) {
	if s == nil || u == nil {
		return
	}
	if s.Build == nil {
		s.Build = construction.NewService(s.World, s.Catalog, s.Units, s.Econ)
	}
	// Combat is a required single-player owner of weapon state. Construct it
	// before validating the queue seam so a normal session cannot enter the
	// binding check with an absent, but later-created, service [P0-00 A.3].
	if s.Combat == nil {
		s.Combat = &combat.Service{}
	}
	if s.Build.OrderBinding == nil {
		s.Build.OrderBinding = s.newOrderBinding()
	}
	orders.BindQueueBinding(u, s.Build.OrderBinding)
}

// bindExistingOrderQueue transfers the session-owned binding only when a
// producer has already installed a concrete queue. It deliberately never
// calls QueueForUnit: a unit visit must not allocate an empty queue [04 §3.3]
// [04 §3.5][06 §11.1].
func (s *Session) bindExistingOrderQueue(u *units.Unit) {
	if s == nil || u == nil || s.Build == nil || s.Build.OrderBinding == nil {
		return
	}
	if q := orders.QueueOfUnit(u); q != nil {
		q.SetBinding(s.Build.OrderBinding)
	}
}

// bindExistingOrderQueues transfers the one session-owned binding to queues
// that were created lazily during placement or InitialMission. It deliberately
// does not call QueueForUnit: absent queues stay absent until an order producer
// asks for one [04 §3.3][04 §3.5].
func (s *Session) bindExistingOrderQueues() {
	if s == nil || s.Units == nil || s.Build == nil || s.Build.OrderBinding == nil {
		return
	}
	for _, u := range s.Units.Iter() {
		if u == nil {
			continue
		}
		if q, ok := u.Orders.(*orders.Queue); ok && q != nil {
			q.SetBinding(s.Build.OrderBinding)
		}
	}
}

// createAndBindServices creates every required authoritative service and binds
// cross-service ports explicitly. It is the single topology site used by both
// skirmish and campaign. [08 "Placement and battle entry"] [04 §7.2]
// DET-01: the session owns both RNG streams for its lifetime; services receive
// s.SimRNG()/s.CrtRNG() directly. There is no process-global stream gate left
// to check — battle bootstrap seeds explicitly via SeedSessionRNG [R-CORE-02].
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
	if s.Wind == nil {
		return fmt.Errorf("session: missing Wind for service wiring [01 §7.3]")
	}
	// Bind the battle's single Park-Miller stream before any mission,
	// commander, or factory allocation reaches the common unit initializer
	// [01 §7.1][R-P28-ANG-01R §2].
	s.Units.SetSimulationRNG(s.SimRNG())
	cobFS, cobLoader := s.Units.COBSource()
	if (cobFS == nil) != (cobLoader == nil) {
		return fmt.Errorf("session: incomplete COB source for service wiring [04 §4.1]")
	}
	// Composition is the central topology site for the session's committed-frame
	// publication boundary [01 §4.4][03 §1]. The helper is idempotent so an
	// existing staged event window or effect pool survives re-binding.
	s.ensurePublicationState()
	// Radar surface cadence is transient and rebuilt at every battle entry,
	// including save/load re-entry; it is not restored from save data
	// [R-CORE-03][CRD-008].
	s.resetRadarBlink()
	// The ten effect strips are allocated at battle entry; a re-entry (retry)
	// replaces the table, destroying every object of the previous battle
	// [R-CORE-01 §4.4.1]. Producers may append from here on.
	s.strips = newStripTable()
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
	// Features [05] with terrain, sim, crt, wind — DET-01 injected from session.
	if s.Features == nil {
		sim := s.SimRNG()
		crt := s.CrtRNG()
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
	// Sensor callbacks remain an internal visibility snapshot. Presentation
	// consumes Frame.Radar after commit and does not bind a mutable surface sink
	// to the authoritative session [03 §3.4][I6].
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
		// Retail backs the scratch record for an unresolvable movement class
		// with the startup template — slopes 255, depths ±10000, i.e.
		// unauthored means unlimited [02 §5 "Movement class record"][04 §6.1
		// R-DOC04-A]. A zeroed record would be a real record whose zero
		// thresholds block every slope and depth band.
		fallback := movement.Template()
		s.Movement = movement.NewSystem(s.World, fallback, grid)
	}
	if s.Movement.Terrain != s.World {
		return fmt.Errorf("session: Movement.Terrain mismatch")
	}
	// Bind movement classes explicitly [02 "Movement class record"]
	s.Movement.SetClasses(s.Catalog.Movement)
	// Path work is shared across the existing session players and uses the
	// session unit-limit word as its pressure divisor [04 R-PATH-01 §6].
	s.Movement.ConfigurePath(s.activePlayerCount(), sessionPathUnitLimit(s))
	// Path is alias to movement scheduler; one scheduler only [04 §7.3]
	if s.Movement.Scheduler == nil {
		return fmt.Errorf("session: Movement.Scheduler nil")
	}
	s.Path = s.Movement.Scheduler
	// Construct the work owner before composing its readiness adapter. The
	// adapter reports the concrete service's presence; it is not an inert
	// placeholder used to let a battle enter composition [P0-00 A.3].
	if s.Build == nil {
		s.Build = construction.NewService(s.World, s.Catalog, s.Units, s.Econ)
	}
	// Combat is a required single-player owner of weapon state. Construct it
	// before validating the queue seam so a normal session cannot enter the
	// binding check with an absent, but later-created, service [P0-00 A.3].
	if s.Combat == nil {
		s.Combat = &combat.Service{}
	}
	// Every newly-created queue receives this one session-owned binding. It
	// carries the economy admission service, target lookup, hostility predicate,
	// deterministic world traversal, and simulation RNG together so producer
	// seams can bind before dispatch and replacements can copy one value
	// [04 §3.3][04 §3.4][06 §11.1][I4]. Create it after movement and features so
	// the adapter callbacks capture the complete battle topology.
	queueBinding := s.newOrderBinding()
	if queueBinding == nil {
		return fmt.Errorf("session: failed to compose order binding")
	}
	if err := queueBinding.ValidateSinglePlayerBinding(); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	// Construction [05]
	if s.Build == nil {
		s.Build = construction.NewService(s.World, s.Catalog, s.Units, s.Econ)
	}
	s.Build.OrderBinding = queueBinding
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
	s.Build.Presentation = s.publication.events
	// Walk-to-site uses normal Move_Ground machinery [04 §3.4][R-P0-06].
	// Bind the movement system so mobile builders walk into nano range before state 2.
	s.Build.Movement = s.Movement
	// Placement release is an independent lifecycle observer. The primary
	// OnDeath hook remains owned by the session loop for triggers/corpse/Killed;
	// this observer releases the leaving unit's retained construction placement,
	// including completed building occupancy, once.
	priorDeathExtra := s.Units.OnDeathExtra
	s.Units.OnDeathExtra = func(h pool.Handle, cause units.DeathCause, u *units.Unit) {
		if priorDeathExtra != nil {
			priorDeathExtra(h, cause, u)
		}
		if s.Build != nil {
			s.Build.ReleasePlacement(h)
		}
	}
	// Combat emits immutable authoritative events in impact order. The
	// collector is presentation-only; EventUnitKilled remains a death/corpse
	// notification in Units.OnDeath and is not duplicated here.
	s.Combat.Events = func(ev combat.Event) {
		if s.publication == nil || s.publication.events == nil {
			return
		}
		pe := frame.Event{
			Tick: ev.Tick, Source: ev.Source, Target: ev.Target,
			X: ev.Position.X, Y: ev.Position.Y, Z: ev.Position.Z,
			Graphic: ev.Graphic, Magnitude: ev.Magnitude,
		}
		switch ev.Kind {
		case combat.EventShake:
			// DET-04: the shake request routes to the session's authoritative
			// phase-10 state — NOT through the presentation event stream. The
			// impact dispatcher stays the request source [R-CORE-01 §4.4.1]
			// [06 §13.2]; phase 10 draws the CRT jitter and publishes the
			// offset on the committed frame.
			s.RequestShake(ev.Magnitude, ev.Duration)
		case combat.EventHitSound, combat.EventWaterSound:
			if ev.Sound != "" {
				_, _, _ = s.EmitWeaponHit(ev.Sound, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, ev.Kind == combat.EventWaterSound)
			}
		case combat.EventStartSound:
			// Start sound is emitted by the common initializer before Fire/RockUnit;
			// route the authored alias into the committed presentation event stream.
			if ev.Sound != "" {
				_, _, _ = s.EmitWeaponStart(ev.Sound, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z})
			}
		case combat.EventStartSmoke:
			// Target carries the projectile handle solely as presentation identity;
			// no authoritative state is read or mutated at this boundary.
			pe.EffectID = uint32(ev.Target)
			s.publication.events.EmitSmokeStart(pe)
			// Strip-9 smoke [R-STRIP-01 §1 strip 9, the weapon-fire smoke
			// sites the census lists as the emit-sfx family's local
			// variants]: the start puff emits from the successful root
			// creation path [06 §13.2], and the census places a strip-9
			// smoke producer behind each of its two variant flags.
			s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, 0, smokeDefaultFrameDelay)
		case combat.EventEndSmoke:
			// Strip-9 smoke [R-STRIP-01 §1 strip 9, the authoritative
			// impact dispatcher under a weapon-definition flag]: the end
			// puff is land-branch-only in the central impact and replaces
			// the explosion art [06 §13.2]; the dispatcher's smoke producer
			// sits on that same weapon-flag branch.
			s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, 0, smokeDefaultFrameDelay)
			s.publication.events.EmitSmokeEnd(pe)
		case combat.EventTrailSmoke:
			// Strip-9 smoke [R-STRIP-01 §1 strip 9, the projectile phase's
			// trail-window and expiry branches]: trail puffs and the
			// non-burn-blow expiry puff are the same trail-style smoke at
			// the projectile's position [06 §13.2].
			pe.EffectID = uint32(ev.Target)
			s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, 0, smokeDefaultFrameDelay)
			s.publication.events.EmitSmokeStart(pe)
		case combat.EventExplosion, combat.EventWaterExplosion:
			// Strip-9 smoke [R-STRIP-01 §1 strip 9, the land/water/lava
			// impact effect variants under a second weapon flag]: the
			// explosion GAF variant functions each carry a strip-9 smoke
			// producer gated on the weapon's start-smoke flag, which the
			// event carries as Smoke.
			if ev.Smoke {
				s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, 0, smokeDefaultFrameDelay)
			}
			if ev.Kind == combat.EventExplosion {
				s.publication.events.EmitExplosion(pe)
			} else {
				s.publication.events.EmitWaterImpact(pe)
			}
		case combat.EventProjectileImpact:
			s.publication.events.EmitImpact(pe)
		case combat.EventUnitKilled, combat.EventCorpse:
			// Death/corpse lifecycle is owned by Units.OnDeath exactly once;
			// a separate authored corpse event, when emitted, is presentation-only.
			if ev.Kind == combat.EventCorpse {
				s.publication.events.EmitCorpse(pe)
			}
		}
	}
	// Still-unwired strip producer rows [R-STRIP-01 §1], left for the units
	// that own their trigger sites rather than invented here:
	//   - strip 5, the flame-weapon area scan: the weapon-class dispatch
	//     that walks the attacker's definition-relative box and appends one
	//     30-tick flame-stream object per unit inside it lives in the
	//     combat death/ignition dispatch, outside this unit's ownership.
	//   - strip 5, the burning-feature smoke: the feature phase's burning
	//     tick owns the site, but reaching the session's strip table from
	//     internal/features needs a producer port on its Service, which is
	//     outside this unit's file ownership (TODO at the burn site).
	//   - strip 9, the sinking-wreck 900-tick smoke column: the producer
	//     parameters are established (15-tick interval, 900-tick window,
	//     appendStripSmokePuffer ready) but the trigger — the wreck-sinking
	//     start in the features sinking path — is likewise outside this
	//     unit's ownership.
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
	// Audio arbitration and media state belong to internal/audio; initialize
	// its concrete owner before event producers are installed [03 §8.2–§8.4].
	if s.Audio == nil {
		s.InitAudio(nil)
	}
	return nil
}

func sessionPathUnitLimit(s *Session) int32 {
	// Skirmish copies the clamped Preferences UnitLimit, whose established
	// missing-value default is 250 [08 R-SKIR-01 §6].
	limit := int32(250)
	if s != nil && s.Mission != nil && s.Mission.Type == mission.TypeCampaign && s.Mission.OTA != nil {
		limit = mission.DecodeMissionGlobals(s.Mission.OTA.Global).MaxUnits
	}
	// TODO(question): surface the loaded Preferences UnitLimit and restored
	// save Summary word on Session; until then skirmish/save composition can
	// only supply the established missing-preference default above.
	return limit
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
// Retail builds it as `clamp(worldY_high + modelTopHigh, 0, 255)`,
// where worldY has already been clamped to `(SeaLevel+1)<<16` by the caller
// and modelTopHigh is the model's top extent in whole world units
// [03 §3.2].
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
// observer's world Y up to before forming the height byte [03 §3.2].
func seaLevelFor(s *Session) uint8 {
	if s == nil || s.World == nil {
		return 0
	}
	return s.World.SeaLevel
}
