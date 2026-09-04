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

// effectWorldPoint turns one composed model-space triple into the world point
// the effect opcode passes to its constructors: the unit's position plus the
// triple, with the THIRD component subtracted rather than added — the
// model-Z-versus-world-Z inversion the piece transform applies
// [04 R-COB-03 §6]. Both clauses of that section use it: the vector types on
// each transformed vertex, the point types on the piece's cached offset.
//
// This is the effect opcode's own traced rule, not model.go's standing
// ComposePiece question. The other consumers of a composed offset (weapon
// muzzles, cargo attach, the factory build plate) still add Z unnegated and
// stay on that open probe; nothing here changes them.
func effectWorldPoint(u *units.Unit, v [3]numeric.Fixed) [3]numeric.Fixed {
	return [3]numeric.Fixed{u.X.Add(v[0]), u.Y.Add(v[1]), u.Z.Sub(v[2])}
}

// pieceWorldPos resolves the world point one COB piece of the sink's source
// unit contributes to the emit-sfx POINT types — the piece's composed offset
// carried into world space by effectWorldPoint [04 R-COB-03 §6]
// [R-STRIP-01 §1 strips 2/7/9][04 §4.4]. Unresolvable pieces (dangling unit,
// unresolved binding) report false and the caller drops the strip append
// rather than inventing a position [I9].
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
	return effectWorldPoint(u, origin), true
}

// pieceEffectPoints resolves the two world points the emit-sfx VECTOR types
// (0..5) pass to their constructors. Retail reads the piece's transformed
// vertex list and takes vertices zero and one, turning each into a world point
// the same way [04 R-COB-03 §6]; the transform it reads is the unit's cached
// render transform, which this build recomposes from the VM's live piece
// states exactly as ComposePiece does [03 §2.4] C21.
//
// A piece with fewer than two vertices has no second point: retail would read
// past its own list, so this build drops the strip append instead of inventing
// a target [I9][I11 — the bounds check is ours, not retail's].
func (s *cobPresentationSink) pieceEffectPoints(cobPiece int) (a, b [3]numeric.Fixed, ok bool) {
	var zero [3]numeric.Fixed
	if s == nil || s.session == nil || s.session.Units == nil {
		return zero, zero, false
	}
	u := s.session.Units.Unit(s.source)
	if u == nil {
		return zero, zero, false
	}
	binding := u.COBBinding()
	if binding == nil || binding.Model == nil || binding.VM == nil {
		return zero, zero, false
	}
	if cobPiece < 0 || cobPiece >= len(binding.PieceMap) {
		return zero, zero, false
	}
	modelPiece := binding.PieceMap[cobPiece]
	if modelPiece < 0 || modelPiece >= len(binding.Model.Pieces) {
		return zero, zero, false
	}
	vertices := binding.Model.Pieces[modelPiece].Vertices
	if len(vertices) < 2 {
		return zero, zero, false
	}
	// The same VM→model piece-state remap and root-angle fold ComposePiece
	// performs; only the product differs, because the vector types need the
	// whole transform rather than the origin alone [03 §2.4] C21 [04 §4.1].
	states := make([]model.PieceState, len(binding.Model.Pieces))
	for cobIndex, modelIndex := range binding.PieceMap {
		if cobIndex < len(binding.VM.Pieces) && modelIndex >= 0 && modelIndex < len(states) {
			states[modelIndex] = binding.VM.Pieces[cobIndex]
		}
	}
	model.FoldRootAngles(states, binding.Model.Root, u.Move.Heading, u.Move.Pitch, u.Move.Bank)
	xf := model.Compose(binding.Model, states, modelPiece)
	return effectWorldPoint(u, xf.Apply(vertices[0])), effectWorldPoint(u, xf.Apply(vertices[1])), true
}

// appendStripFlameTrail creates a strip-7 flame-stream trail container: the
// emit-sfx wake pair's effect [03 R-FX-01 §3][03 R-FX-02 §1]. Its init lays
// one segment immediately and one per tick while `nextSpawn ≤ deadline`, so
// `lifetime + 1` segments in all, each flying from a fresh copy of A toward B
// at `((B − A) · trunc(65536 / lifetime)) >> 16` per tick and expiring with
// the container's deadline. The family spends no random draws at all
// [R-STRIP-01 §3].
func (s *Session) appendStripFlameTrail(strip int, a, b [3]numeric.Fixed, hold, lifetime int32) {
	if s == nil || s.strips == nil {
		return
	}
	if s.strips.poolFull() {
		return
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	o := stripObject{
		family:        stripFamilyFlameTrail,
		windowEnd:     tick + uint32(lifetime),
		nextSpawn:     tick + 1,
		spawnInterval: 1,
		particleLife:  lifetime,
		phaseModulus:  hold,
		src:           a,
		dst:           b,
	}
	// The trail family draws nothing, so the init spawn runs whether or not a
	// CRT stream is bound [R-STRIP-01 §3].
	o.spawnOnce(tick, s.CrtRNG())
	s.strips.append(strip, o)
}

// emitSFXStripProducers is the session edge of retail's emit-sfx type switch
// [04 R-COB-03 §6][R-STRIP-01 §1 strips 2/7/9][04 §4.4].
//
// The two geometry clauses are what the switch dispatches over. VECTOR types
// (0..5) take the piece's transformed vertices zero and one; POINT types
// (0x101..0x103) take the piece's composed offset once. Both become world
// points through effectWorldPoint [04 R-COB-03 §6].
//
// Then the six-way switch over 0..5: types 0 and 1 build the strip-7
// flame-stream trail (`vertex 0 → vertex 1`, hold 1, lifetime 6 and 7); types
// 2 and 3 build the strip-2 sprinkle (`vertex 0 → vertex 1`, spacing 16 and 8,
// colour flag 1); types 4 and 5 build the same sprinkle with THE TWO POINTS
// EXCHANGED and the same two spacings [03 R-FX-01 §3][03 R-FX-02 §1]. White
// and black smoke land a strip-9 smoke puffer at the point; the sub-bubble
// type overwrites its second point with the first's X and Z and the map's
// sea-level height before landing a strip-7 sprinkle. Vector types from 6 up
// match no case and are ignored, exactly as retail's switch is.
//
// Every case is gated on local visibility upstream of this sink, exactly as
// retail gates the whole opcode [04 R-COB-03 §6][04 §4.4]; the appended strip
// objects are authoritative sim state swept in phase 11 [R-STRIP-01 §2].
func (s *cobPresentationSink) emitSFXStripProducers(ev cob.PresentationEvent) {
	if s == nil || s.session == nil || s.session.strips == nil {
		return
	}
	switch ev.SFXType {
	case 0, 1, 2, 3, 4, 5:
		s.emitSFXVectorProducer(ev)
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
		// 0x101 white takes the trail puff's parameters on `smoke 1`; 0x102
		// black takes the same shape on the second entry [06 R-WFX-01 §5].
		init := SmokePuffTrail
		if ev.SFXType == 0x102 {
			init = SmokePuffBlack
		}
		s.session.appendStripSmokePuffer(9, pos, init)
	case 0x103:
		// Sub-bubbles. The type does NOT move its spawn point to the water
		// line: it overwrites the SECOND point with the first point's X and Z
		// and a height taken from the map's sea-level byte shifted into 16.16,
		// then lands a strip-7 sprinkle with 8-tick spacing and colour flag 0
		// [04 R-COB-03 §6][03 R-FX-01 §3 strip 7]. The bubbles therefore rise
		// from the piece toward the surface; before this the whole container
		// sat at sea level with A == B and a zero step.
		toSurface := pos
		if s.session.World != nil {
			toSurface[1] = s.session.World.SeaLevelWorld()
		}
		s.session.appendStripSprinkle(7, pos, toSurface, 8, 0)
	}
}

// emitSFXVectorProducer runs the emit-sfx six-way switch over vector types
// 0..5 [04 R-COB-03 §6]. Every one of them takes the piece's transformed
// vertices zero and one; 0/1 build the strip-7 flame-stream trail and 2..5 the
// strip-2 impact sprinkle, with 4/5 passing the two points exchanged
// [03 R-FX-01 §3][03 R-FX-02 §1].
func (s *cobPresentationSink) emitSFXVectorProducer(ev cob.PresentationEvent) {
	a, b, ok := s.pieceEffectPoints(ev.Piece)
	if !ok {
		return
	}
	switch ev.SFXType {
	case 0, 1:
		// The VTOL/thrust wake pair: one trail per opcode, hold 1 at both
		// sites, lifetime 6 for type 0 and 7 for type 1 — the selector that is
		// the only difference between the two constructor calls
		// [03 R-FX-01 §3 strip 7][03 R-FX-02 §1].
		lifetime := int32(6)
		if ev.SFXType == 1 {
			lifetime = 7
		}
		s.session.appendStripFlameTrail(7, a, b, 1, lifetime)
	case 2, 3, 4, 5:
		// The wake pair proper: strip-2 sprinkle, spacing 16 for the even
		// types and 8 for the odd ones, colour flag 1 at all four sites. Types
		// 4 and 5 are 2 and 3 with the two points exchanged — vertex 1 → vertex
		// 0 [03 R-FX-01 §3 strip 2][03 R-FX-02 §1][04 R-COB-03 §6].
		spacing := int32(16)
		if ev.SFXType == 3 || ev.SFXType == 5 {
			spacing = 8
		}
		if ev.SFXType >= 4 {
			a, b = b, a
		}
		s.session.appendStripSprinkle(2, a, b, spacing, 1)
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
	// Port 16 (GROUND_HEIGHT) is bound here rather than in the instance port
	// table units.unitPortHandlers builds: it is a pure world query with no
	// per-unit state, so it needs no D+wake-ordering guarantee relative to
	// Create the way the state-bearing ports (1/5/6/18/19/20) do [04 §4.4].
	// The height query is the session's own terrain, matching the query
	// s.World.HeightAt already answers for order handlers above
	// [03 §2.3][R-COB-03 §3].
	if binding.VM != nil && s.World != nil {
		binding.VM.BindPort(cob.Port(16), cob.GroundHeightPortFunc(s.World.HeightAt))
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
//
// It sizes the pool from the established missing-value unit limit. Every
// battle-entry site that knows its own limit — a skirmish's configured
// `UnitLimit`, a campaign's OTA `maxunits` — calls the Sized form instead.
func newBattleSlicedWorldWithCOB(cat *content.Catalog, fs vfs.FSOps, mode int, sortKeys [pool.PlayerCount]uint32) (*units.World, error) {
	if cat == nil {
		return nil, fmt.Errorf("session: nil catalog for unit pool")
	}
	return newBattleSlicedWorldWithCOBSized(cat, fs, mode, sortKeys, SkirmishDefaultUnitLimit)
}

// newBattleSlicedWorldWithCOBSized is newBattleSlicedWorldWithCOB with the
// session's per-player unit limit stated explicitly.
//
// The pool is sized once at battle entry as `limit × 10 + 1` records, with
// exactly `limit` records per player slot and record 0 the null identity — and
// that is true regardless of how many slots participate or how many
// definitions the catalog holds [05 R-SHARE-01 §7]. The limit comes from the
// mission's `maxunits` in campaign mode and from the configured
// `[Preferences] UnitLimit` in skirmish mode [08 R-SKIR-01 §6]; the caller
// owns that choice, which is why it is a parameter rather than a read of
// `cat`. It is also why a caller handing in a *restricted* catalog — the
// campaign `UseOnlyUnits` filter of [08 R-ENTRY-01 §2 step 4], which can cut
// the table to a dozen definitions — cannot starve the pool: the record count
// never consults the table at all.
func newBattleSlicedWorldWithCOBSized(cat *content.Catalog, fs vfs.FSOps, mode int, sortKeys [pool.PlayerCount]uint32, perPlayerRecords int) (*units.World, error) {
	if fs == nil {
		return nil, fmt.Errorf("session: missing filesystem for COB binding [04 §4.1]")
	}
	if cat == nil {
		return nil, fmt.Errorf("session: nil catalog for unit pool")
	}
	if len(cat.Units) <= 0 {
		return nil, fmt.Errorf("session: catalog has no unit definitions [02 §5]")
	}
	if perPlayerRecords < 1 {
		// Retail applies NO clamp at the sizing site: the OTA `maxunits`
		// integer is truncated into a 16-bit word and the record count is the
		// signed 16-bit `limit × 10 + 1` read back unsigned. A zero limit
		// therefore builds a one-record pool (the null record) with every slice
		// empty, so every creation — the mission spawner's included — is
		// refused silently and the session starts with no units; a negative
		// limit wraps the slice bounds past the allocation and overruns the
		// heap, which is undefined behaviour, not a contract [05 R-SHARE-01 §7].
		// The 20..500 clamp covers only the skirmish/lobby copy [08 R-SKIR-01
		// §6]. Nanolathe deliberately refuses both cases at entry with a
		// diagnostic instead of reproducing an empty pool or an overrun.
		return nil, fmt.Errorf("nanolathe: unit pool sizing failed: logical path <battle entry>, providers searched [session unit limit], expected a per-player unit limit of at least 1, got %d", perPlayerRecords)
	}
	order := pool.PlayerPermutationForMode(mode, sortKeys)
	w, err := units.NewSlicedWithOrder(perPlayerRecords, cat, order)
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
		// The one-directional row read the guard's combat join needs
		// [04 R-UNIT-06 §1][05 R-SHARE-01 §1]; `Hostile` below stays the
		// symmetric predicate the command resolver asks for [04 R-ORD-02 §1].
		DeclaresAlliance: func(from, toward uint8) bool {
			return s.Econ.DeclaresAlliance(from, toward)
		},
		// The per-player mapping word grid [03 R-LAYER §1]. The visibility
		// service owns it — its width is the map's cell width halved, one word
		// per 2x2-cell tile — and the sole simulation reader reaching it
		// through the order binding is the aircraft landing test's coarse
		// early accept [04 R-AIR-01 §6a][04 R-AIR-01 §14.2]. The caller hands
		// tile coordinates that already carry its own index offset; the flat
		// index is formed here with the grid's own stride, so a tile column
		// past the stride wraps into the next row as the flat index does.
		// Only a genuine past-the-end index is refused, and refusing makes the
		// caller run its full test rather than invent a word.
		MappingWord: func(tileX, tileZ int32) (uint16, bool) {
			if s.Vis == nil || tileX < 0 || tileZ < 0 {
				return 0, false
			}
			w, _ := s.Vis.GridDimensions()
			if w <= 0 {
				return 0, false
			}
			grid := s.Vis.WordMask()
			idx := tileZ*w + tileX
			if idx < 0 || int(idx) >= len(grid) {
				return 0, false
			}
			return grid[idx], true
		},
		Hostile: func(actor, target *units.Unit) bool {
			if actor == nil || target == nil {
				return false
			}
			if actor.Owner == target.Owner {
				return false
			}
			if s.Econ == nil || int(actor.Owner) >= len(s.Econ.Players) || int(target.Owner) >= len(s.Econ.Players) {
				return true
			}
			return !s.Econ.Players[actor.Owner].Allies[target.Owner]
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
			if s.Features == nil || s.World == nil {
				return orders.FeatureView{}, false
			}
			// Resolve the sampled cell through the terrain's fringe-parent hop;
			// lattice scans may land on any stamped footprint cell, not only its
			// anchor [05 R-ECO-02 §2].
			_, anchorX, anchorZ, ok := features.FeatureAt(s.World, numeric.Fixed(int64(cx)<<20), numeric.Fixed(int64(cz)<<20))
			if !ok {
				return orders.FeatureView{}, false
			}
			inst := s.Features.InstanceAt(anchorX, anchorZ)
			if inst == nil {
				return orders.FeatureView{}, false
			}
			id := uint16(world.PlotFeatureNone)
			if cell := s.World.PlotAt(int32(anchorX), int32(anchorZ)); cell != nil {
				id = cell.Feature()
			}
			var key string
			if inst.Def != nil {
				key = inst.Def.CanonicalKey
			}
			var metal, energy int32
			var footprintX, footprintZ, height int32
			var reclaimable, autoreclaimable bool
			if inst.Def != nil {
				metal, energy = inst.Def.Metal, inst.Def.Energy
				footprintX, footprintZ, height = inst.Def.FootprintX, inst.Def.FootprintZ, inst.Def.Height
				reclaimable, autoreclaimable = inst.Def.Reclaimable, inst.Def.Autoreclaimable
			}
			return orders.FeatureView{ID: id, CX: int32(inst.CX), CZ: int32(inst.CZ), X: inst.X, Y: inst.Y, Z: inst.Z, FootprintX: footprintX, FootprintZ: footprintZ, Height: height, DefinitionKey: key, Metal: metal, Energy: energy, Reclaimable: reclaimable, Autoreclaimable: autoreclaimable}, true
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
				var metal, energy int32
				var footprintX, footprintZ, height int32
				var reclaimable, autoreclaimable bool
				if inst.Def != nil {
					metal, energy = inst.Def.Metal, inst.Def.Energy
					footprintX, footprintZ, height = inst.Def.FootprintX, inst.Def.FootprintZ, inst.Def.Height
					reclaimable, autoreclaimable = inst.Def.Reclaimable, inst.Def.Autoreclaimable
				}
				if visit(orders.FeatureView{ID: id, CX: int32(inst.CX), CZ: int32(inst.CZ), X: inst.X, Y: inst.Y, Z: inst.Z, FootprintX: footprintX, FootprintZ: footprintZ, Height: height, DefinitionKey: key, Metal: metal, Energy: energy, Reclaimable: reclaimable, Autoreclaimable: autoreclaimable}) {
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
		movementGoals.RunAir = s.Movement.AirLegRunner()
		// The target registry's third list, held and rebuilt by the movement
		// system [06 §3.1][04 R-AIR-01 §11].
		movementGoals.AirBases = s.Movement.AirBaseList
		movementGoals.InstallPoint = func(req orders.PointGoalRequest) bool {
			if req.Node == nil {
				return false
			}
			return s.Movement.InstallPointGoal(req)
		}
		// The record-level payload release of [04 R-ORD-01 §1], in the form
		// RWU-19-18 spells out: mover-less no-op, a NULL goal handed to the
		// controller (cancel the in-flight search, `0x80` on the previous
		// payload's record, clear has-waypoint and wants-repath), virtual
		// delete, clear the field. The arrival release of [04 R-MOV-03 §2] and
		// the queue teardown of [04 R-MOV-03 §9] reach the same helper through
		// the record, so every caller of this port gets it.
		movementGoals.Release = func(node *orders.Node) bool {
			if node == nil {
				return false
			}
			return s.Movement.ReleaseGoalPayload(node)
		}
		movementGoals.InstallAnnulus = func(req orders.AnnulusGoalRequest) bool {
			return s.Movement.InstallAnnulusGoal(req)
		}
		movementGoals.InstallRectangle = func(req orders.RectangleGoalRequest) bool {
			return s.Movement.InstallRectangleGoal(req)
		}
		movementGoals.InstallAir = func(req orders.AirGoalRequest) bool {
			return s.Movement.InstallAirGoal(req)
		}
		// The direct position commit of [04 R-COLL-01 §4] — the setter that
		// clears the old footprint, writes XYZ and the cell pair, and stamps
		// the new one under the overlap protocol without asking the placement
		// validator. internal/movement owns the occupancy planes and the
		// class-layer restamp family [04 R-MOV-03 §3], so it owns this too; the
		// `Teleport` row is its order-facing caller [04 R-ORD-01 §2].
		movementGoals.PlaceUnit = func(req orders.PlaceRequest) bool {
			return s.Movement.PlaceUnit(req)
		}
	}
	return &orders.QueueBinding{
		Economy:   s.Econ,
		Lookup:    worldQueries.LookupUnit,
		Hostility: worldQueries.Hostile,
		SimRNG:    s.SimRNG(),
		CurrentTick: func() uint32 {
			if s.Clock == nil {
				return 0
			}
			return s.Clock.GlobalTick
		},
		Movement: movementGoals,
		World:    worldQueries,
		// The reclaim payout's cell entry [05 R-WORK-01 §5]. Reading it at CALL
		// time keeps the binding independent of whether the feature service has
		// been composed yet.
		ReclaimFeature: func(cx, cz int) (float32, float32, bool) {
			if s.Features == nil {
				return 0, 0, false
			}
			return s.Features.ReclaimAt(cx, cz)
		},
		// Command code 14's gate: the definition's compiled build list is
		// non-empty [04 R-ORD-02 §1]. The pages are the `CANBUILD` sections of
		// gamedata/sidedata.tdf, keyed by the builder's canonical unit name
		// [02 "Build-menu catalog keys"]; a definition with no page has no
		// build list, which is a reject, not an error.
		BuildList: func(def *content.UnitDef) bool {
			if def == nil || s.Catalog == nil || s.Catalog.BuildMenus == nil {
				return false
			}
			page := s.Catalog.BuildMenus[content.CanonicalKey(def.CanonicalKey)]
			return page != nil && len(page.Buttons) > 0
		},
		// The carriable test of codes 1, 2 and 6 is §10.2's nine-reject
		// admission, which internal/movement owns [04 §10.2][04 R-ORD-02 §1].
		TransportAdmission: func(carrier, candidate *units.Unit) bool {
			if s.Movement == nil || s.Units == nil || carrier == nil || candidate == nil {
				return false
			}
			return s.Movement.CanTransport(carrier.Handle, candidate.Handle, s.Units).Allowed
		},
		Resources: func(owner uint8) (orders.ResourceView, bool) {
			if s.Econ == nil || int(owner) >= len(s.Econ.Players) {
				return orders.ResourceView{}, false
			}
			p := s.Econ.Players[owner]
			return orders.ResourceView{Stock: p.Stock, Capacity: p.Capacity}, true
		},
		Work: &orders.WorkAdapter{
			Ready: func() bool { return s.Build != nil && s.Econ != nil },
			Assist: func(builder *units.Unit, n *orders.Node, tick uint32) bool {
				if s.Build == nil || n == nil || worldQueries.LookupUnit == nil {
					return false
				}
				target := worldQueries.LookupUnit(n.Target)
				var before float32
				if target != nil {
					before = target.Remaining
				}
				ok := s.Build.Assist(builder, target, tick)
				// [05 R-WORK-01 §1] runs the completion transition from inside
				// the shared step, so the builder that stores the zero is the
				// builder that completes the frame — a HelpBuild helper, or a
				// guard assisting, just as much as the frame's own builder.
				//
				// This build defers a product's mover state and its visibility
				// publication to CompleteUnit, and the only caller of that hook
				// on the construction path was the WorkResult a builder's own
				// StepUnit window returns. A frame finished by a helper
				// therefore got the whole completion posture — remaining zero,
				// full health, the completed and classifier bits, the
				// activatewhenbuilt edge, its placement retired — and no mover
				// record and no publish, so it stood finished on the map unable
				// to answer a Move and covering nothing. Its own builder
				// recovered it one visit later, but only while that builder was
				// still alive and still holding the record; a frame whose
				// builder had walked away or died never completed at all.
				// Resurrection's seam below is the same call for the same
				// reason [01 §6.1][03 §3].
				//
				// The test is the transition edge, not the flag: it fires on
				// the one step that stored the zero, so the hook runs once even
				// when a second helper is working the same frame.
				if target != nil && before != 0 && target.Remaining == 0 {
					s.CompleteUnit(target.Handle)
				}
				return ok
			},
			Repair: func(builder, patient *units.Unit, n *orders.Node, _ uint32) bool {
				if s.Build == nil || n == nil || worldQueries.LookupUnit == nil {
					return false
				}
				if builder == nil || builder.Def == nil {
					return false
				}
				return s.Build.Repair(builder, patient, construction.WorkerQuantum(builder.Def.WorkerTime))
			},
			Resurrect: func(builder *units.Unit, n *orders.Node, _ uint32) bool {
				return s.resurrectStep(builder, n, worldQueries.LookupFeature)
			},
			// Interrupt mask 2's producer. The record-removal cleanup delivers
			// the cancel-current notification to the operation handler when the
			// removed record's dynamic gate still holds bit 1; for the three
			// construction rows that handler is the factory production machine,
			// which this package does not hold
			// [04 R-ORDER-02 §2][05 "Build request and factory queue behavior"].
			CancelNotice: func(owner *units.Unit, n *orders.Node, tick uint32) bool {
				if s.Build == nil {
					return false
				}
				return s.Build.DeliverCancelNotice(owner, n, tick)
			},
		},
		Weapons: &orders.WeaponAdapter{
			Ready: func() bool { return s.Combat != nil },
			ReleaseSlot: func(u *units.Unit, idx int) bool {
				return combat.ReleaseWeaponSlot(u, idx)
			},
			InhibitSlot: func(u *units.Unit, idx int) bool {
				return combat.InhibitWeaponSlot(u, idx)
			},
			SetManualTarget: func(u *units.Unit, idx int, target pool.Handle) bool {
				return combat.SetManualWeaponTarget(u, idx, target)
			},
			FireTarget: func(u *units.Unit, idx int, target pool.Handle, tick uint32) bool {
				return combat.FireWeaponTarget(u, idx, target, tick)
			},
			FirePoint: func(u *units.Unit, idx int, x, z numeric.Fixed, tick uint32) bool {
				return combat.FireWeaponPoint(u, idx, x, z, tick)
			},
			StopFiring: func(u *units.Unit, idx int) bool {
				return combat.StopWeaponFiring(u, idx)
			},
			Acquire: func(u *units.Unit, idx int, limit uint32) (pool.Handle, bool) {
				return s.Combat.AcquireWeaponTarget(u, idx, limit, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG())
			},
			CanEngage: func(u *units.Unit, target pool.Handle, idx int) bool {
				if s.Combat == nil || s.Units == nil {
					return false
				}
				return s.Combat.CanEngageSlotTarget(u, s.Units.Unit(target), idx, s.World)
			},
			Engaged: func(u *units.Unit, idx int) bool {
				if u == nil || u.SlotAt(idx) == nil {
					return false
				}
				target := u.SlotAt(idx).Target
				if target.Kind != units.TargetUnit || s.Units == nil {
					return false
				}
				return combat.WeaponCanEngage(u, idx, s.Units.Unit(target.Unit))
			},
		},
		Presentation: &orders.PresentationAdapter{
			Ready: func() bool {
				return s.publication != nil && s.publication.events != nil
			},
			Status: func(u *units.Unit, kind uint8, text string) bool {
				if s.publication == nil || s.publication.events == nil || u == nil {
					return false
				}
				// The status request is presentation-only, but its admission gate is
				// authoritative unit ownership/state: dead or dying units and other
				// players never reach the local message line [04 R-ORD-01 §1][07
				// R-HUD-03 §14]. Unit.Alive/Dying are this model's live/death state.
				if int(u.Owner) != localPlayerForSession(s) || !u.Alive || u.Dying {
					return false
				}
				tick := uint32(0)
				if s.Clock != nil {
					tick = s.Clock.GlobalTick
				}
				// Store the semantic request at the committed boundary. The
				// presentation edge then submits it to the existing audio queue,
				// whose resolver supplies the definition display name, slot default
				// caption/localization, UNITCHAT priority gate and cooldown [03
				// §8.3][07 R-HUD-03 §14].
				return s.publication.events.EmitStatus(frame.Event{
					Tick: tick, Source: u.Handle, StatusKind: kind,
					StatusText: text, StatusClass: 1,
				})
			},
			Nanolathe: func(builder *units.Unit, n *orders.Node, tick uint32) bool {
				if s.publication == nil || s.publication.events == nil || s.Build == nil || builder == nil || n == nil {
					return false
				}
				_, source, ok := s.Build.QueryNanoPiece(builder)
				if !ok {
					// QueryNanoPiece is the authored model/piece geometry gate. Do
					// not synthesize a muzzle point when that lookup is unresolved
					// [03 §5.5][I9].
					return false
				}
				target := worldQueries.LookupUnit(n.Target)
				if target == nil || !target.Alive || target.Dying {
					return false
				}
				activeUntil := uint32(0)
				name := orders.DescriptorFor(n.ID).Name
				switch name {
				case "RepairUnit", "RepairUnitNoMove", "SelfRepair":
					activeUntil = tick + 150
				case "HelpBuild", "MobileBuild", "BuildingBuild", "Reclaim", "Resurrect", "VTOL_Reclaim":
					activeUntil = tick + 300
				case "Capture", "ReclaimUnit", "VTOL_ReclaimUnit":
					activeUntil = tick + 900
				case "VTOL_HelpBuild", "VTOL_RepairUnit":
					// These air rows spray but have no nanolathe-active stamp
					// [04 R-ORD-01 §7].
				default:
					return false
				}
				nanoPiece := [3]numeric.Fixed{source.X(), source.Y(), source.Z()}
				reversed := name == "Capture" || name == "ReclaimUnit" || name == "VTOL_ReclaimUnit"
				e := frame.Event{
					Tick: tick, Source: builder.Handle, Target: target.Handle,
					X: nanoPiece[0], Y: nanoPiece[1], Z: nanoPiece[2],
					TargetX: target.X, TargetY: target.Y, TargetZ: target.Z,
					EffectID: 6, Mode: 1, Team: builder.Owner,
					Producer: frame.ProducerBeam, PaletteRow: 6,
					NanolatheGeometryKnown: true, NanolatheActiveUntil: activeUntil,
				}
				var boxMin, boxMax [3]numeric.Fixed
				if reversed {
					// The reversed direction [05 R-WORK-01 §8]: the six-word
					// box is the TARGET UNIT's — its world position plus its
					// definition's six signed extents [02 R-CAT-01 §7] — and it
					// sits at the SOURCE end, with the builder's nano piece as
					// the degenerate destination.
					//
					// Both halves of that used to be missing. The point pair
					// was swapped, but the box was never published, so the
					// client re-derived it from the model and — with no
					// NanolatheBoxAtSource flag to say which end carried the
					// extent — read it as the destination. Source and
					// destination then both lay inside the target unit and
					// every particle of a capture or a unit reclaim was born
					// and died inside the victim, exactly the collapse the
					// feature rows had before WU-19-134.
					boxMin, boxMax = target.NanolatheBox()
					e.X, e.Y, e.Z = boxMin[0], boxMin[1], boxMin[2]
					e.TargetX, e.TargetY, e.TargetZ = nanoPiece[0], nanoPiece[1], nanoPiece[2]
					e.NanolatheTargetBoxKnown = true
					e.NanolatheTargetMin, e.NanolatheTargetMax = boxMin, boxMax
					e.NanolatheBoxAtSource = true
				} else {
					// The ordinary work direction — repair, help-build/assist,
					// mobile/building construction, and a resurrection whose
					// target has already resolved into a unit — sprays FROM
					// the builder's nano piece INTO the target unit, so the
					// box sits at the DESTINATION end (NanolatheBoxAtSource
					// stays false) [05 R-WORK-01 §8]. Publishing the same
					// derivation the reversed producers use [02 R-CAT-01 §7]
					// lets the client stop re-deriving the destination from
					// real model geometry, which is the wrong shape: the
					// record is footprint-derived in X/Z.
					boxMin, boxMax = target.NanolatheBox()
					e.NanolatheTargetBoxKnown = true
					e.NanolatheTargetMin, e.NanolatheTargetMax = boxMin, boxMax
				}
				if !s.publication.events.EmitNanolathe(e) {
					return false
				}
				if s.strips != nil {
					if reversed {
						s.appendStripNanoEmitterFromBox(boxMin, boxMax, nanoPiece)
					} else {
						s.appendStripNanoEmitter(
							[3]numeric.Fixed{e.X, e.Y, e.Z},
							[3]numeric.Fixed{e.TargetX, e.TargetY, e.TargetZ})
					}
				}
				return true
			},
			NanolatheFeature: func(builder *units.Unit, n *orders.Node, feature orders.FeatureView, tick uint32) bool {
				if s.publication == nil || s.publication.events == nil || s.Build == nil || builder == nil || n == nil {
					return false
				}
				_, source, ok := s.Build.QueryNanoPiece(builder)
				if !ok {
					return false
				}
				footX, footZ := feature.FootprintX, feature.FootprintZ
				if footX <= 0 {
					footX = 1
				}
				if footZ <= 0 {
					footZ = 1
				}
				minX := world.CellToWorld(feature.CX)
				minZ := world.CellToWorld(feature.CZ)
				maxX := minX.Add(numeric.Fixed(int64(footX) * 1048576))
				maxZ := minZ.Add(numeric.Fixed(int64(footZ) * 1048576))
				minY, ok := worldQueries.TerrainHeight(minX, minZ)
				if !ok {
					return false
				}
				maxY := minY.Add(numeric.Fixed(int64(feature.Height) * 65536))
				boxMin := [3]numeric.Fixed{minX, minY, minZ}
				boxMax := [3]numeric.Fixed{maxX, maxY, maxZ}
				nanoPiece := [3]numeric.Fixed{source.X(), source.Y(), source.Z()}
				e := frame.Event{
					Tick: tick, Source: builder.Handle, Target: 0,
					X: minX, Y: minY, Z: minZ,
					TargetX: source.X(), TargetY: source.Y(), TargetZ: source.Z(),
					EffectID: 6, Mode: 0, Team: builder.Owner,
					Producer: frame.ProducerBeam, PaletteRow: 6,
					NanolatheActiveUntil:    tick + 300,
					NanolatheTargetBoxKnown: true,
					NanolatheTargetMin:      boxMin,
					NanolatheTargetMax:      boxMax,
				}
				name := orders.DescriptorFor(n.ID).Name
				if name == "Reclaim" && n.Param1 > 15 || name == "VTOL_Reclaim" && n.Param1 > 30 {
					// Feature reclaim sprays FROM the feature box INTO the
					// builder's nano piece [05 R-WORK-01 §8]: the box is the
					// source end and the nano piece is the degenerate one. The
					// published point pair already reads that way (the event's
					// own position is the box corner and its target is the nano
					// piece); the flag is what tells the presentation which end
					// carries the extent. Without it the box was read as the
					// destination in both directions, so every particle of a
					// tree reclaim was born and died inside the tree's own cell
					// and no spray ever reached the commander.
					e.Mode = uint8(frame.NanolatheBuild)
					e.NanolatheGeometryKnown = true
					e.NanolatheBoxAtSource = true
					segments := frame.BuildNanolatheSegments(e, tick)
					if len(segments) != 2 {
						return false
					}
					if s.publication.events.EmitNanolatheSegments(e, tick) != len(segments) {
						return false
					}
					for range segments {
						s.appendStripNanoEmitterFromBox(boxMin, boxMax, nanoPiece)
					}
					return true
				}
				if name == "Resurrect" {
					// The resurrection wait sprays the ordinary way round —
					// builder nano piece into the feature box [05 R-WORK-01 §8]
					// — so the emitting end is the nano piece and the box is the
					// destination.
					e.Mode = uint8(frame.NanolatheReclaim)
					e.NanolatheGeometryKnown = true
					e.X, e.Y, e.Z = nanoPiece[0], nanoPiece[1], nanoPiece[2]
					e.TargetX, e.TargetY, e.TargetZ = minX, minY, minZ
					if !s.publication.events.EmitNanolathe(e) {
						return false
					}
					s.appendStripNanoEmitterBox(nanoPiece, boxMin, boxMax)
					return true
				}
				return s.publication.events.EmitNanolathe(e)
			},
			// TODO(T25): PresentationAdapter.Teleport stays nil. The row's
			// effect is the strip-5 flame-stream container of [03 R-LAYER §4] —
			// a 30-tick object that lays one 52-byte animated flame segment
			// every 10 ticks between a moved unit's old and new position, with
			// one CRT draw per segment for its start frame — and internal/frame
			// has no strip-5 producer: frame.Strip carries no constant for it
			// and frame.RouteForProducer would return StripUnknown, which is
			// the sentinel that refuses a guessed route [03 §1][I9]. Adding the
			// producer means an event kind in internal/frame, a container in
			// internal/render and a painter in internal/client, none of which
			// WU-19-142 owns. Placeholder: the simulation half of the row runs
			// in full (the units move and their footprints restamp) and the
			// flame stream is not drawn. The handler already treats a nil
			// callback as "no effect" and still commits the position, so this
			// binding is the only thing that has to change when the producer
			// lands.
		},
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
	s.Build.Combat = s.Combat
	s.Build.World = s.Units
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
	// The extraction rate is sampled by the unit CREATOR, once per created unit
	// and never recomputed [05 R-PROD-01 §6], so the plot it reads is bound
	// here — before the first mission, commander, factory or restore allocation
	// — rather than at each placement call site.
	s.Units.SetExtractionSampler(s.World)
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
	// The cloak gate, all three terms [05 R-ECO-01 §9][03 R-VIS-01 §6].
	//
	// This gate is the REQUEST side. It decides whether the unit is charged
	// this pass; whether the unit ends the pass hidden is the settlement's
	// transition on units.Unit.Hidden, which nothing here reads
	// [05 R-ECO-01 §9].
	//
	// Term 1 is the cloak-REQUESTED status bit, units.Unit.IsCloaked (I13):
	// seeded from the definition's `init_cloaked` by the constructor, set by
	// the `Cloak_On` order handler and cleared by `Cloak_Off` behind the
	// definition's cloak capability, and rebuilt by save restore. Those are
	// retail's three writers and there is no fourth; the earlier text here
	// denied the `init_cloaked` seed and sent it to an "initial-posture path"
	// that does not exist — both corrected in place (RWU-19-26).
	//
	// Term 2 is the decloak-forced status bit, bit 12, which the sensor
	// phase's proximity breach sets and the top of the next first pass clears
	// [03 R-VIS-01 §4 pass 4][03 R-VIS-01 §6]. This build keeps that sensor
	// status word beside the unit rather than in it, so the term is read from
	// there. It cannot be observed apart from term 3 — the breach writes the
	// `tick + 90` deadline on the same visit — so an implementation that
	// carried the breach as the deadline alone would diverge by nothing; the
	// term is still real and is kept.
	//
	// Correction (WU-19-99): this said the term was read from the sensor status
	// word "which is why [05 R-ECO-01 §9] records the same bit as inert with its
	// meaning Unknown". §9 no longer records it that way and has been corrected
	// in place: the bounded-negative search that found no writer predated the
	// sensor-phase trace. The bit is the decloak-forced latch — the sensor
	// phase's first pass clears it on every live unit every tick and the
	// proximity-breach pass sets it again on the same visit that stamps the
	// deadline — and its meaning is Established, not Unknown
	// [05 R-ECO-01 §9][03 R-VIS-01 §4 pass 4][03 R-VIS-01 §6].
	//
	// Term 3 is the per-unit deadline: `currentTick >= unit.RevealDeadline`,
	// inclusive [03 R-VIS-01 §6]. That field is shared with the sensor phase's
	// proximity breach and the work handlers' nanolathe stamps — one field, a
	// later write always winning outright, retail taking no maximum
	// [03 R-VIS-01 §6][04 R-ORD-01 §1] (I13). A unit no handler has stamped carries
	// zero and is therefore due from its first pass, which is exactly the idle
	// cloaked unit's behavior.
	s.Econ.CloakDue = func(u *units.Unit) bool {
		if u == nil || !u.IsCloaked {
			return false
		}
		if s.visStatus[int(u.Handle)]&visibility.DecloakBit != 0 {
			return false
		}
		tick := uint32(0)
		if s.Clock != nil {
			tick = s.Clock.GlobalTick
		}
		return tick >= u.RevealDeadline
	}
	// The ledger's production discount for a computer player selects on the
	// battle's difficulty word [05 R-ECO-01 §3]; it is the same word the AI
	// plan gate reads, through the same accessor. A word outside 0..2 leaves
	// the selector unset, which the ledger already treats as the undiscounted
	// (hard) path rather than guessing a value.
	if word, ok := sessionDifficultyWord(s); ok {
		s.Econ.SetEconomySelector(word)
	}
	// Features [05] with terrain, sim, crt, wind — DET-01 injected from session.
	if s.Features == nil {
		sim := s.SimRNG()
		crt := s.CrtRNG()
		s.Features = features.NewService(s.World, sim, crt, s.Wind)
		// The steam-strip producer of [05 R-ECO-02 §3] is bound before the
		// terrain's own features are populated, because the vents are placed by
		// that populate call and the producer runs from the stamp itself.
		s.Features.GeothermalSteam = func(x, y, z numeric.Fixed) {
			s.appendStripGeothermalSteam([3]numeric.Fixed{x, y, z})
		}
		s.bindFeatureStripProducers()
		s.Features.PopulateFromTerrain()
	} else if s.Features.Terrain != s.World {
		return fmt.Errorf("session: Features.Terrain mismatch")
	} else {
		// Existing service but world may have been swapped (e.g. load); ensure terrain features are present
		if s.Features.GeothermalSteam == nil {
			s.Features.GeothermalSteam = func(x, y, z numeric.Fixed) {
				s.appendStripGeothermalSteam([3]numeric.Fixed{x, y, z})
			}
		}
		s.bindFeatureStripProducers()
		s.Features.PopulateFromTerrain()
	}
	// The two remaining feature seams — BurnFrameGeometry and AnimationTicks —
	// stay unbound here; bindFeatureStripProducers says why.
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
	// The air build approach's product-footprint resolver
	// [04 R-ORD-02 §2][04 R-PATH-01 §13]: internal/movement holds no catalog
	// handle of its own, so it asks this session-bound closure for the
	// MobileBuild product's footprint pair from the stable catalog index the
	// order record carries in Param1 — the same catalog lookup
	// construction.Service.siteAnchorCell/siteCentre already perform for the
	// ground twin.
	s.Movement.ProductFootprint = func(catalogIndex uint32) (fx, fz int32, ok bool) {
		if s.Catalog == nil {
			return 0, 0, false
		}
		def, ok := s.Catalog.UnitDefByIndex(catalogIndex)
		if !ok || def == nil {
			return 0, 0, false
		}
		fx, fz = world.FootprintForUnit(s.Catalog, def)
		return fx, fz, true
	}
	// The occupancy overlap protocol arbitrates a contested cell from the
	// OCCUPANT'S OWNER player-row state byte and records the outcome on both
	// units' flag words [04 R-COLL-01 §4]. The grid carries neither fact, so
	// the composer hands it the same player table the damage funnel and the
	// sweep gate read — the row's ControllerState, never the unit's own owner
	// byte, which is the slot number [06 R-DMG-01 §8]. An unoccupied row, or
	// an index past the ten records, reads as ControlByteAbsent, which is not
	// the displacing state, so its units are never displaced.
	s.Movement.AttachOverlapBinding(func(owner uint8) uint8 {
		if s.Econ == nil || int(owner) >= len(s.Econ.Players) {
			return combat.ControlByteAbsent
		}
		p := &s.Econ.Players[owner]
		if !p.Exists {
			return combat.ControlByteAbsent
		}
		return p.ControllerState
	})
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
	s.Build.Combat = s.Combat
	// The area walk of [06 §9.3] offers a feature candidate in every covered
	// cell, and the entry it reaches is the feature damage of [06 §13.1]. The
	// combat service holds no feature runtime of its own, so the composer hands
	// it the one built above. This sits with the combat construction rather
	// than in the feature block, because the feature service is composed first
	// and Combat does not exist yet there; with none bound a blast reaches
	// units only.
	s.Combat.Features = s.Features
	s.Build.World = s.Units
	// The damage funnel's three control-byte gates read the player slot's
	// control byte, never the unit's own owner byte [06 R-DMG-01 §8]. The byte
	// is the economy player record's ControllerState the session already writes
	// (1 for the local human seat, 2 for a computer seat) [05 R-SHARE-01 §1];
	// an unoccupied row, and any index past the ten records — the eleventh row
	// a null-shooter record's neutral side byte selects — reads as
	// ControlByteAbsent, which PASSES gate 1 and rejects gate 2
	// [06 R-DMG-01 §9].
	s.Combat.ControlByte = func(owner uint8) uint8 {
		if s.Econ == nil || int(owner) >= len(s.Econ.Players) {
			return combat.ControlByteAbsent
		}
		p := &s.Econ.Players[owner]
		if !p.Exists {
			return combat.ControlByteAbsent
		}
		return p.ControllerState
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
	if err := queueBinding.Validate(); err != nil {
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
			// Interrupt mask 8's producer, and the construction-side arm of the
			// target-removed walk: "when a unit is destroyed, the removal path
			// walks every reference registered on that unit and calls the method
			// with 0x8, then unlinks the reference" [04 R-ORD-01 §6]. The one
			// reference construction registers is the factory record's product
			// binding, so a product destroyed mid-build wakes its factory with
			// the construction-stopped interrupt
			// [05 "Build request and factory queue behavior"]. It runs before
			// the placement release below only because the release does not read
			// the link; neither ordering is contractual.
			s.Build.NotifyProductRemoved(h)
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
			Graphic: ev.Graphic, AssetID: ev.Bank, Magnitude: ev.Magnitude,
			HasCalculatedFlash: ev.HasCalculatedFlash, CalculatedTable: ev.CalculatedTable,

			DurationsB: render.FlashFrameDurations(int(ev.CalculatedTable)),
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
			// `startsmoke` at the muzzle point: four frames at hold 30, a slow
			// puff that hangs where the shot left [06 R-WFX-01 §5].
			s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, SmokePuffStart)
		case combat.EventEndSmoke:
			// Strip-9 smoke [R-STRIP-01 §1 strip 9, the authoritative
			// impact dispatcher under a weapon-definition flag]: the end
			// puff is land-branch-only in the central impact and replaces
			// the explosion art [06 §13.2]; the dispatcher's smoke producer
			// sits on that same weapon-flag branch.
			// `endsmoke` at impact takes the trail puff's parameters
			// [06 R-WFX-01 §5].
			s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, SmokePuffTrail)
			s.publication.events.EmitSmokeEnd(pe)
		case combat.EventTrailSmoke:
			// Strip-9 smoke [R-STRIP-01 §1 strip 9, the projectile phase's
			// trail-window and expiry branches]: trail puffs and the
			// non-burn-blow expiry puff are the same trail-style smoke at
			// the projectile's position [06 §13.2].
			pe.EffectID = uint32(ev.Target)
			s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, SmokePuffTrail)
			s.publication.events.EmitSmokeStart(pe)
		case combat.EventExplosion, combat.EventWaterExplosion:
			// Strip-9 smoke [R-STRIP-01 §1 strip 9, the land/water/lava
			// impact effect variants under a second weapon flag]: the
			// explosion GAF variant functions each carry a strip-9 smoke
			// producer gated on the weapon's start-smoke flag, which the
			// event carries as Smoke.
			if ev.Smoke {
				// The land dust of an above-sea explosion: three particles,
				// seven ticks apart [06 R-WFX-01 §5][06 R-WFX-01 §2 step 4].
				s.appendStripSmokePuffer(9, [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}, SmokePuffLandDust)
			}
			if ev.Kind == combat.EventExplosion {
				s.publication.events.EmitExplosion(pe)
			} else {
				s.publication.events.EmitWaterImpact(pe)
			}
		case combat.EventDamageFlash:
			// The flash's only reader is the minimap unit-dot pass, which doc
			// 07 owns [06 R-WPN-04 §2]. No frame emitter carries it yet, so the
			// event stops here rather than being published as some other kind.
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
	s.bindDamageReaction()
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

// sessionUnitLimit is the session's per-player unit limit: the `Max Units`
// lobby option for a skirmish, or the mission's unit-count key for a campaign,
// which retail copies once into a session word when the world is built
// [08 R-AI-01 §13][02 "unit limit"]. Both the path scheduler's service tiering
// and the computer player's half-capacity scoring term read that one word.
// campaignUnitLimit is the session unit-limit word a campaign battle entry
// installs: the OTA loader writes it from the map's `maxunits` key whenever an
// OTA is parsed, with a missing-key default of 200, and campaign is the one
// mode whose OTA value survives battle entry [08 R-SKIR-01 §6][05 R-SHARE-01
// §7]. A mission with no parsed OTA never reached that writer, so it takes the
// same missing-value default.
func campaignUnitLimit(m *mission.Mission) int32 {
	if m == nil || m.OTA == nil {
		// Decoding a nil section yields the accessor defaults, so the missing
		// `maxunits` default is read from the one decoder rather than restated
		// as a second constant here.
		return mission.DecodeMissionGlobals(nil).MaxUnits
	}
	return mission.DecodeMissionGlobals(m.OTA.Global).MaxUnits
}

func sessionUnitLimit(s *Session) int32 {
	if s != nil && s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
		return campaignUnitLimit(s.Mission)
	}
	// Skirmish battle entry copies the clamped configured `[Preferences]
	// UnitLimit` over the session word [08 R-SKIR-01 §6]. It reaches the
	// session on the setup record, whose Normalize applied the clamp; a
	// session composed without one (a fixture) reads the missing-value
	// default through the same clamp.
	//
	// TODO(T25): where the carried-over word lives IS traced — the marker that
	// stood here said it was not. [08 R-ENTRY-01 §6] names the destination
	// exactly: the battle-restoration dispatcher's first step is
	// `Summary.maxunits` → the **lobby unit-limit copy**, which is the same
	// start-up word `[Preferences] UnitLimit` seeds and battle entry clamps
	// 20..500 [08 R-SKIR-01 §6]. The pool for the battle being loaded was
	// already sized from that copy as it stood *before* the restore, so the
	// restored value only reaches the next battle — and a second load in one
	// process must therefore read it here. This engine still drops it because
	// the write belongs at the restore site (`SkirmishConfig.UnitLimit`, set
	// while staging a retail battle), in files this unit does not own.
	if s == nil {
		return int32(ClampUnitLimit(0))
	}
	return int32(ClampUnitLimit(s.Skirmish.UnitLimit))
}

func sessionPathUnitLimit(s *Session) int32 {
	return sessionUnitLimit(s)
}

// sessionAIDifficulty is the difficulty word the AI profile's plan gate
// compares each directive's arguments against: 0 easy, 1 medium, 2 hard,
// written from the registry/lobby setting and from the campaign difficulty
// control [08 R-AI-01 §12]. A campaign takes the difficulty the mission was
// loaded with (the same word that selected its schema); a skirmish takes the
// lobby value, whose established missing-value default is 1, Medium
// [08 "Skirmish configuration"]. A word outside the vocabulary is reported
// absent rather than guessed.
func sessionAIDifficulty(s *Session) (ai.Difficulty, bool) {
	word, ok := sessionDifficultyWord(s)
	if !ok {
		return "", false
	}
	switch word {
	case 0:
		return ai.DifficultyEasy, true
	case 1:
		return ai.DifficultyMedium, true
	default:
		return ai.DifficultyHard, true
	}
}

// sessionDifficultyWord is the battle's difficulty word itself — 0 easy, 1
// medium, 2 hard. It has two consumers, and they must read the same word: the
// AI profile's plan gate [08 R-AI-01 §12] and the ledger's production discount
// for a computer player, whose "global mode selector" is established as this
// same difficulty word [05 R-ECO-01 §3]. One reader, so there is one copy.
// A word outside the vocabulary is reported absent rather than guessed.
func sessionDifficultyWord(s *Session) (int, bool) {
	if s == nil {
		return 0, false
	}
	word := s.Skirmish.Difficulty
	if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
		// Difficulty is -1 on a mission that is not a campaign load.
		word = s.Mission.Difficulty
	}
	if word < 0 || word > 2 {
		return 0, false
	}
	return word, true
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

// resurrectStep is the construction-owned half of the `Resurrect` order row,
// bound onto the queue binding's Work adapter [04 R-ORD-01 §5]
// [05 R-WORK-01 §7]. internal/orders owns the row — its phases, captions,
// deadlines and result codes — but two of its steps need things an order record
// cannot see: the unit CATALOGUE (phase 3 resolves the corpse's name to a unit
// definition) and the unit ALLOCATOR together with the feature grid (phase 5).
// internal/construction owns both and imports internal/orders, so the call
// cannot go the other way; this is the composition seam that closes the loop,
// exactly as `Assist` and `Repair` above do for their own steps.
//
// The record's phase byte selects the step, which is the same discriminator
// internal/construction's unit-reclaim step reads:
//
//   - phase 3: "copy the feature record's name, truncate it at the first '_',
//     look the result up in the unit catalogue"; store the definition index in
//     p1 and the delay in p2. A name that resolves to nothing reports false,
//     which is the row's `Ressurection failed` arm.
//   - phase 5: allocate the unit of that definition at the feature's position
//     and the builder's owner byte, remove the feature, set the new unit's
//     remaining fraction to 0 and its health to 1, and bind it as the record's
//     target. A refused allocation reports false, which is the row's
//     `Unable to create any more units` arm.
//
// The delay arithmetic stays in internal/orders (orders.ResurrectionDelay):
// the 0.3 and its truncation belong to this state of the row, not to the
// catalogue lookup.
func (s *Session) resurrectStep(builder *units.Unit, n *orders.Node, lookupFeature func(int32, int32) (orders.FeatureView, bool)) bool {
	if s == nil || s.Build == nil || s.Catalog == nil || s.World == nil {
		return false
	}
	if builder == nil || builder.Def == nil || n == nil || lookupFeature == nil {
		return false
	}
	view, ok := lookupFeature(world.WorldToCell(n.GoalX), world.WorldToCell(n.GoalZ))
	if !ok {
		return false
	}
	// The corpse's own definition key, truncated at its first underscore, is
	// the unit name [05 R-WORK-01 §7]. `armaap_dead` resurrects an `armaap`.
	def, found := s.Catalog.Unit(construction.FeatureNameToDefName(view.DefinitionKey))
	if !found || def == nil {
		return false
	}
	switch n.Phase {
	case 3:
		n.BuildDefKey = def.CanonicalKey
		if idx, ok := s.Catalog.UnitDefIndex(def.CanonicalKey); ok {
			n.Param1 = idx
		}
		n.Param2 = uint32(orders.ResurrectionDelay(def.BuildTime, builder.Def.WorkerTime))
		return true
	case 5:
		cell := s.World.PlotAt(view.CX, view.CZ)
		if cell == nil {
			return false
		}
		// The executor's ONLY simulation draw is phase 1's approach-point draw,
		// which internal/orders takes [05 R-WORK-01 §7]; the construction
		// service's own optional jitter draw is therefore passed no stream, so
		// the seed advances exactly once per resurrection (I4).
		product, err := s.Build.Resurrect(builder, cell, def, view.X, view.Y, view.Z, nil)
		if err != nil || product == nil {
			return false
		}
		// The feature's blocking footprint has just left the grid, so the
		// pathfinder's cached static obstacles are stale until the revision
		// moves; the reclaim transition bumps it for the same reason
		// [05 "Removal and successor replacement"].
		// The feature service reconciles its own animation instances against
		// the grid in the next feature phase, the same route the reclaim
		// transition's grid-only removal already takes.
		s.World.BumpStaticObstacleRevision()
		n.Target = product.Handle
		// A resurrected unit is finished (remaining 0), so it joins the world
		// the way any completed product does: movement state and a visibility
		// publish [01 §6.1][03 §3].
		s.CompleteUnit(product.Handle)
		// Settled (2026-09-02): the two fields phase 5 copies are the live
		// feature record's ORIENTATION triple — bank and heading as one 32-bit
		// copy, pitch as a 16-bit copy — written into the new unit's bank,
		// heading and pitch words; no position is copied. The corpse placement
		// stores that triple from the dying unit, and every other placement
		// stores zeros [05 R-WORK-01 §7 "the two copied fields"][05 "Feature
		// instance and terrain cell"][04 R-ORD-01 §5].
		//
		// TODO: not yet implemented here — features.Instance carries no
		// orientation, so PlaceCorpse (internal/features) has nowhere to keep
		// the dying unit's Bank/Heading/Pitch and this transplant has nothing to
		// copy; the resurrected unit keeps the allocator's own facing. The
		// follow-up is: add the triple to features.Instance (zero for every
		// non-corpse placement), have the death path hand PlaceCorpse the
		// unit's triple, and copy it onto `product` here after CompleteUnit.
		return true
	}
	return false
}

// bindDamageReaction installs the damage-intake reaction routine's seams
// [06 §9.1] step 4, closed at [06 R-WPN-04 §2]. The routine itself lives in
// internal/combat; everything bound here is a mechanism that package cannot
// reach — internal/orders (which imports it), the computer player's manager,
// the alliance rows, and the interface message queue.
func (s *Session) bindDamageReaction() {
	if s == nil || s.Combat == nil {
		return
	}
	s.Combat.Reaction = &combat.ReactionSeams{
		// Part 1: pending bit 0x10 on every order record observing the victim
		// [06 R-WPN-04 §2 part 1][04 R-MOV-03 §7].
		ObserverNotice: func(victim *units.Unit) {
			orders.ObserverNotice(s.Units, victim)
		},
		// "the attacker is not allied" [08 R-AI-01 §11]. Same-owner counts as
		// allied; the two directed rows are read the way the combat scan reads
		// them [05 R-SHARE-01 §1].
		Allied: func(a, b uint8) bool {
			if a == b {
				return true
			}
			if s.Econ == nil || int(a) >= len(s.Econ.Players) || int(b) >= len(s.Econ.Players) {
				return false
			}
			return s.Econ.Players[a].Allies[b] || s.Econ.Players[b].Allies[a]
		},
		// The construction throttle's draw and deadline are the manager's own
		// [08 R-AI-01 §11]; the combat side has already tested `cancapture` and
		// the control byte, which is where retail's own test lives.
		ArmConstructionThrottle: func(owner uint8, tick uint32) {
			if int(owner) >= len(s.AI) {
				return
			}
			if mgr := s.AI[owner]; mgr != nil {
				mgr.RecordUnitLoss(tick)
			}
		},
		StopCurrentOrder: func(victim *units.Unit, tick uint32) {
			s.bindOrderQueue(victim)
			orders.StopCurrentOrder(victim, tick)
		},
		RetaliationOrder: func(victim, attacker *units.Unit) bool {
			s.bindOrderQueue(victim)
			return orders.RetaliationOrder(victim, attacker)
		},
		// The §3.1 acquisition physical gate, bound to the operands the damage
		// path does not carry [06 §3.1][06 R-WPN-04 §2 part 3].
		SlotAcquisitionAdmits: func(victim *units.Unit, idx int, cand *units.Unit) bool {
			return combat.SlotAcquisitionAdmits(victim, idx, cand, s.Units, s.Vis, s.World, s.Econ, s.Catalog)
		},
		UnderAttackSilenced: orders.UnderAttackSilenced,
		// The message helper posts the kind-2 message only when the victim is
		// NOT in the current selection, is owned by the local player, is alive
		// and is not death-latched [06 R-WPN-04 §2 part 4]. Bit 4 of the status
		// word is the selection bit the local selection commands write.
		UnderAttackNotice: func(victim *units.Unit) {
			if victim == nil || victim.Owner != s.LocalOwner {
				return
			}
			if !victim.Alive || victim.Dying {
				return
			}
			if victim.Flags&0x10 != 0 {
				return
			}
			s.EmitUnderAttack(victim.Handle)
		},
	}
}

// bindFeatureStripProducers connects the feature service's strip producers to
// this session's strip table. It sits beside the geothermal binding above and
// owns the same kind of seam: the strip table is session state, so the feature
// service can only reach it through a function the composer fills.
//
// The burning-feature smoke of [05 R-FEAT-01 §10] pass 3a is a strip-5
// container [R-STRIP-01 §1 strip 5]. Pass 3a emits ONE smoke particle per
// gated visit — the every-third-tick cadence is the phase-6 gate's, not the
// container's — so the container is built with the family's one-puff shape:
// the constructor spawns the only puff and the closed window retires it.
//
// The parameters are no longer open. [05 R-FEAT-01 §16] gives this site's init
// row as `(frameCap 0, spawnInterval 1, frameHold 0, lifetime 0, selector 0)`
// — the same row the trail puff, the timer-expiry puff and the impact
// `endsmoke` take, which is SmokePuffTrail below: `smoke 1`, every frame of
// the entry, the family's default hold of 7, and the lifetime of 0 that makes
// the container a one-shot. appendStripSmokePuffer spawns that single puff at
// the producer, so its last-frame draw is the emission's THIRD CRT draw,
// taken after the call site's two jitter draws and in the same phase
// [05 R-FEAT-01 §16][03 R-STRIP-01 §2][01 §7.5].
//
// Two feature seams are deliberately left nil, because filling them needs
// files outside this unit:
//
//   - BurnFrameGeometry wants the burn sequence's current GAF frame. The
//     session resolves no feature GAF at all — content.Catalog carries the
//     definition's sequence NAMES, and the only GAF cache in the build is the
//     client's. Filling it needs a resolver field and setter on Session (the
//     shape of SetEffectEntryFrameCount), an accessor over the client's
//     feature-GAF cache, and one line in the composer that binds them, exactly
//     as the effect frame count is bound today. Until then the puff is emitted
//     unjittered at the footprint centre, which is pass 3a's own base point.
//   - AnimationTicks wants the death/reclaim sequence LENGTH from the same
//     GAF entries, through the same chain. Until it is bound, the transition
//     of [05 R-FEAT-01 §5] takes step 3's immediate replacement, which is what
//     every removal did before the animation records existed — so a missing
//     seam changes nothing rather than freezing a cell.
func (s *Session) bindFeatureStripProducers() {
	if s == nil || s.Features == nil {
		return
	}
	if s.Features.BurnSmoke == nil {
		s.Features.BurnSmoke = func(pos [3]numeric.Fixed) {
			s.appendStripSmokePuffer(stripBurningFeatureSmoke, pos, SmokePuffTrail)
		}
	}
	// The two art-backed seams read the session's feature-sequence resolver at
	// CALL time, not at bind time: the shell installs the resolver when it
	// attaches the client, which can happen either side of this composition.
	// A session with no resolver answers "unknown" — zero geometry, which
	// makes pass 3a's addends zero, and zero visits, which keeps the
	// transition of [05 R-FEAT-01 §5] on step 3's immediate replacement.
	if s.Features.BurnFrameGeometry == nil {
		s.Features.BurnFrameGeometry = func(def *content.FeatureDef, visit int32) (w, h, xoff, yoff int32) {
			if s.featureSequence == nil || def == nil || def.SeqNameBurn == "" {
				return 0, 0, 0, 0
			}
			w, h, xoff, yoff, _, ok := s.featureSequence(def.Filename, def.SeqNameBurn, visit)
			if !ok {
				return 0, 0, 0, 0
			}
			return w, h, xoff, yoff
		}
	}
	if s.Features.AnimationTicks == nil {
		s.Features.AnimationTicks = func(def *content.FeatureDef, selector uint8) int32 {
			if s.featureSequence == nil || def == nil {
				return 0
			}
			sequence := def.SeqNameDie
			if selector == featureAnimSelectorReclaim {
				sequence = def.SeqNameReclamate
			}
			if sequence == "" {
				return 0
			}
			_, _, _, _, visits, ok := s.featureSequence(def.Filename, sequence, 0)
			if !ok {
				return 0
			}
			return visits
		}
	}
}

// featureAnimSelectorReclaim is the animating save selector of the reclaim
// sequence: 0 burn, 1 death, 2 reclaim [R-SAVE-FEATURE-01]. The features
// package keeps the same vocabulary unexported; this is the one value the
// composer needs to pick a definition's sequence name.
const featureAnimSelectorReclaim uint8 = 2

// stripBurningFeatureSmoke is the literal strip index the burning-feature
// producer passes [R-STRIP-01 §1 strip 5].
const stripBurningFeatureSmoke = 5
