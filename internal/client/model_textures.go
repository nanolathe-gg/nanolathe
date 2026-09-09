package client

// Model texture resolution and the phase-7 sequence cursors: which GAF entry
// a primitive's texture name selects, and which frame of it the committed
// tick is on [03 R-CRD-005 §1].

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// texKind classifies a resolved texture entry [03 §2.4.1]:
// one frame is static, exactly ten is a LOGOS team texture (frame = owner,
// never animated), anything else is an animated sequence ticked by the
// simulation frame with per-frame delays from the GAF table.
type texKind uint8

const (
	texStatic texKind = iota
	texTeam
	texAnimated
)

// texRef is one texture-name resolution.
type texRef struct {
	kind  texKind
	key   string            // immutable sequence identity used by loaded primitive cursors
	frame *formats.GAFFrame // static frame (kind static)
	entry *formats.GAFEntry // team/animated: full frame list
	cum   []int             // animated: cumulative delay ticks per frame
	total int               // animated: full-cycle length in ticks
}

// modelTextureCursor binds the generic presentation cursor to decoded GAF
// frames. The cursor owns only an AssetID sequence; this adapter keeps the
// already-resolved frame pointers alongside it, so draw loops never perform a
// VFS lookup [03 §2.4.1][03 §4.4].
type modelTextureCursor struct {
	player *presentationrender.TexturePlayer
	frames []*formats.GAFFrame
}

// phase7Stepper keeps the registry traversal independent of a concrete
// cursor. It is private because the session seam exposes only Client; the
// interface also makes the count-snapshot mutation rule directly testable.
type phase7Stepper interface {
	stepPhase7()
}

func (p *modelTextureCursor) stepPhase7() {
	if p != nil && p.player != nil {
		p.player.Step()
	}
}

type modelTextureKey struct {
	kind      uint8
	id        uint64
	tex       string
	piece     int
	primitive int
}

// ModelTextureRegistry is the battle-owned phase-7 service for animated
// textures embedded in loaded 3DO primitives. It is deliberately independent
// of Client: a renderer replacement, an off-screen unit, or no renderer at
// all cannot change which players phase 7 advances [03 R-CRD-005 §1].
type ModelTextureRegistry struct {
	fs              *vfs.FS
	primary         map[string]texRef
	logos           map[string]texRef
	loads           map[modelTextureLoadKey]*unitModel
	byCompiled      map[*compiledmodel.Model]modelTextureLoadKey
	unitByName      map[string]modelTextureLoadKey
	unitByID        map[uint16]modelTextureLoadKey
	hullByName      map[string]*compiledmodel.Model
	projectileByID  map[int32]modelTextureLoadKey
	featurePrepared map[string]*unitModel
	featureDefs     map[string]*content.FeatureDef
	featureOrder    []string
	players         []phase7Stepper
	bindings        map[modelTexturePrimitiveKey]*modelTextureCursor
	standalone      bool
}

type modelTextureLoadKind uint8

const (
	modelLoadUnit modelTextureLoadKind = iota
	modelLoadFeature
	modelLoadProjectile
	modelLoadStandalone
)

type modelTextureLoadKey struct {
	kind modelTextureLoadKind
	id   string
}

type modelTexturePrimitiveKey struct {
	load      modelTextureLoadKey
	piece     int
	primitive int
}

const (
	modelCursorUnit       uint8 = 0 // [03 §2.4.1] unit model texture cursor family
	modelCursorFeature    uint8 = 1 // [03 §5.1] feature model texture cursor family
	modelCursorProjectile uint8 = 2 // [03 §5.2] projectile model texture cursor family
	modelCursorDebris     uint8 = 3 // [04 R-COB-04 §2] detached whole-piece cursor family
)

// resolveTextureRef makes side-before-default precedence explicit. Callers
// pass the already compiled side set and default set; enumeration order is
// never allowed to decide which authored entry wins [03 §2.4.1].
func resolveTextureRef(side, defaults map[string]texRef, name string) (texRef, bool) {
	key := strings.ToLower(name)
	if ref, ok := side[key]; ok {
		return ref, true
	}
	ref, ok := defaults[key]
	return ref, ok
}

// NewModelTextureRegistry binds the battle's loaded model primitives before
// simulation begins. restoreStart divides the terrain table at the first
// restore reset: ordinary terrain entries and links bind before the saved
// suffix and its own link pass. Future Service admissions use precompiled
// geometry and never read the VFS from a simulation tick [03 R-CRD-005 §1].
func NewModelTextureRegistry(fs *vfs.FS, cat *content.Catalog, terrain *world.Terrain, restoreStart int) (*ModelTextureRegistry, error) {
	r := newModelTextureRegistry(fs, false)
	if cat == nil {
		return r, nil
	}
	if err := r.bindProjectileModels(cat); err != nil {
		return nil, fmt.Errorf("bind projectile models: %w", err)
	}
	if err := r.prepareFeatureModels(cat); err != nil {
		return nil, fmt.Errorf("prepare feature models: %w", err)
	}
	preRestore, postRestore := terrainDefinitionBoundary(terrain, restoreStart)
	r.admitTerrainFeatures(terrain, 0, preRestore)
	if err := r.bindUnitModels(cat); err != nil {
		return nil, fmt.Errorf("bind unit models: %w", err)
	}
	r.linkAdmittedFeatures(cat)
	r.admitTerrainFeatures(terrain, preRestore, postRestore)
	r.linkAdmittedFeatures(cat)
	return r, nil
}

func (r *ModelTextureRegistry) bindUnitModels(cat *content.Catalog) error {
	for _, key := range cat.SortedUnitKeys() {
		def := cat.Units[key]
		if def == nil {
			continue
		}
		// Unit parsing admits its corpse feature before loading that unit model.
		r.admitFeatureName(cat, def.Corpse)
		if def.ObjectName == "" {
			continue
		}
		load := modelTextureLoadKey{kind: modelLoadUnit, id: strconv.FormatUint(uint64(def.UnitDefID), 10)}
		m, err := r.bindLoad(load, def.ObjectName)
		if err != nil {
			return fmt.Errorf("unit %q: %w", key, err)
		}
		// Picking needs only immutable authored geometry. Keep its name lookup
		// separate from the per-definition animation identity [07 R-REV-01 §1].
		name := ckey(def.ObjectName)
		if m != nil && r.hullByName[name] == nil {
			r.hullByName[name] = m.compiled
		}
		r.unitByName[ckey(key)] = load
		r.unitByID[uint16(def.UnitDefID)] = load
	}
	return nil
}

func (r *ModelTextureRegistry) bindProjectileModels(cat *content.Catalog) error {
	projectileNames := map[string]modelTextureLoadKey{}
	for _, def := range cat.WeaponRecordsByID() {
		if def == nil || def.Model == "" {
			continue
		}
		name := ckey(def.Model)
		load, ok := projectileNames[name]
		if !ok {
			load = modelTextureLoadKey{kind: modelLoadProjectile, id: strconv.FormatInt(int64(def.ID), 10)}
			if _, err := r.bindLoad(load, def.Model); err != nil {
				return fmt.Errorf("weapon %d: %w", def.ID, err)
			}
			projectileNames[name] = load
		}
		r.projectileByID[def.ID] = load
	}
	return nil
}

func newModelTextureRegistry(fs *vfs.FS, standalone bool) *ModelTextureRegistry {
	r := &ModelTextureRegistry{
		fs: fs, primary: map[string]texRef{}, logos: map[string]texRef{},
		loads: map[modelTextureLoadKey]*unitModel{}, byCompiled: map[*compiledmodel.Model]modelTextureLoadKey{},
		unitByName: map[string]modelTextureLoadKey{}, unitByID: map[uint16]modelTextureLoadKey{}, projectileByID: map[int32]modelTextureLoadKey{},
		featurePrepared: map[string]*unitModel{}, featureDefs: map[string]*content.FeatureDef{},
		hullByName: map[string]*compiledmodel.Model{},
		bindings:   map[modelTexturePrimitiveKey]*modelTextureCursor{}, standalone: standalone,
	}
	r.buildTextureIndex()
	return r
}

func (r *ModelTextureRegistry) AdmitFeatureDefinition(def *content.FeatureDef) {
	if r == nil || def == nil {
		return
	}
	id := ckey(def.CanonicalKey)
	if id == "" {
		return
	}
	if _, ok := r.featureDefs[id]; ok {
		return
	}
	r.featureDefs[id] = def
	r.featureOrder = append(r.featureOrder, id)
	if def.Object == "" {
		return
	}
	key := modelTextureLoadKey{kind: modelLoadFeature, id: id}
	m := r.featurePrepared[key.id]
	r.loads[key] = m
	if m == nil || m.compiled == nil {
		return
	}
	r.byCompiled[m.compiled] = key
	for i := len(m.compiled.Pieces) - 1; i >= 0; i-- {
		if m.compiled.Pieces[i].Parent < 0 {
			r.bindPiece(m.compiled, i, key)
		}
	}
}

func (r *ModelTextureRegistry) prepareFeatureModels(cat *content.Catalog) error {
	type featureKey struct {
		key       string
		canonical string
	}
	keys := make([]featureKey, 0, len(cat.Features))
	for key, def := range cat.Features {
		canonical := ckey(key)
		if def != nil && ckey(def.CanonicalKey) != "" {
			canonical = ckey(def.CanonicalKey)
		}
		keys = append(keys, featureKey{key: key, canonical: canonical})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].canonical != keys[j].canonical {
			return keys[i].canonical < keys[j].canonical
		}
		return keys[i].key < keys[j].key
	})
	for _, key := range keys {
		def := cat.Features[key.key]
		if def == nil || def.Object == "" {
			continue
		}
		m, err := expandModelFromFSStrict(r.fs, def.Object)
		if err != nil {
			return fmt.Errorf("feature %q: %w", key.key, err)
		}
		r.featurePrepared[key.canonical] = m
	}
	return nil
}

func terrainDefinitionBoundary(terrain *world.Terrain, restoreStart int) (int, int) {
	if terrain == nil {
		return 0, 0
	}
	if restoreStart < 0 {
		restoreStart = 0
	}
	if restoreStart > len(terrain.FeatureDefs) {
		restoreStart = len(terrain.FeatureDefs)
	}
	return restoreStart, len(terrain.FeatureDefs)
}

func (r *ModelTextureRegistry) admitTerrainFeatures(terrain *world.Terrain, start, end int) {
	if r == nil || terrain == nil {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > len(terrain.FeatureDefs) {
		end = len(terrain.FeatureDefs)
	}
	if start > end {
		start = end
	}
	for i := start; i < end; i++ {
		r.AdmitFeatureDefinition(terrain.FeatureDefs[i])
	}
}

func (r *ModelTextureRegistry) admitFeatureName(cat *content.Catalog, name string) {
	if cat == nil || name == "" {
		return
	}
	r.AdmitFeatureDefinition(cat.Features[content.CanonicalKey(name)])
}

func (r *ModelTextureRegistry) linkAdmittedFeatures(cat *content.Catalog) {
	if r == nil || cat == nil {
		return
	}
	// The count is live: an admitted transition record is visited later in this
	// same pass. Links are resolved in dead, reclaim, burnt order [03
	// R-CRD-005 §1].
	for i := 0; i < len(r.featureOrder); i++ {
		def := r.featureDefs[r.featureOrder[i]]
		if def == nil {
			continue
		}
		r.admitFeatureLink(cat, def.FeatureDeadDef, def.FeatureDead)
		r.admitFeatureLink(cat, def.FeatureReclamateDef, def.FeatureReclamate)
		r.admitFeatureLink(cat, def.FeatureBurntDef, def.FeatureBurnt)
	}
}

func (r *ModelTextureRegistry) admitFeatureLink(cat *content.Catalog, linked *content.FeatureDef, name string) {
	if linked != nil {
		r.AdmitFeatureDefinition(linked)
		return
	}
	r.admitFeatureName(cat, name)
}

func (r *ModelTextureRegistry) unitModel(defName string, defID uint16, name string) *unitModel {
	if r == nil {
		return nil
	}
	if defName != "" {
		if load, ok := r.unitByName[ckey(defName)]; ok {
			return r.loads[load]
		}
	}
	if defID != 0 {
		if load, ok := r.unitByID[defID]; ok {
			return r.loads[load]
		}
	}
	return r.modelFor(modelLoadUnit, name, name)
}

func (r *ModelTextureRegistry) projectileModel(id int32, name string) *unitModel {
	if r == nil {
		return nil
	}
	if load, ok := r.projectileByID[id]; ok {
		return r.loads[load]
	}
	return r.modelFor(modelLoadProjectile, strconv.FormatInt(int64(id), 10), name)
}

func ckey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func (r *ModelTextureRegistry) modelFor(kind modelTextureLoadKind, id, name string) *unitModel {
	if r == nil {
		return nil
	}
	key := modelTextureLoadKey{kind: kind, id: ckey(id)}
	if m, ok := r.loads[key]; ok {
		return m
	}
	if !r.standalone {
		return nil
	}
	if key.id == "" {
		key.id = ckey(name)
	}
	m, _ := r.bindLoad(key, name)
	return m
}

func (r *ModelTextureRegistry) bindLoad(key modelTextureLoadKey, name string) (*unitModel, error) {
	if r == nil || name == "" {
		return nil, nil
	}
	if m, ok := r.loads[key]; ok {
		return m, nil
	}
	m, err := expandModelFromFSStrict(r.fs, name)
	if err != nil {
		return nil, err
	}
	r.loads[key] = m
	if m == nil || m.compiled == nil {
		return nil, fmt.Errorf("model %q: empty compiled geometry", name)
	}
	r.byCompiled[m.compiled] = key
	var roots []int
	for i := range m.compiled.Pieces {
		if m.compiled.Pieces[i].Parent < 0 {
			roots = append(roots, i)
		}
	}
	for i := len(roots) - 1; i >= 0; i-- {
		r.bindPiece(m.compiled, roots[i], key)
	}
	return m, nil
}

// bindPiece mirrors the loader's linked walk: sibling subtree, child subtree,
// then this piece's primitives ascending. Children retain source sibling order,
// so walking the list backwards reproduces the sibling-first recursion
// [03 R-CRD-005 §1].
func (r *ModelTextureRegistry) bindPiece(m *compiledmodel.Model, index int, load modelTextureLoadKey) {
	if r == nil || m == nil || index < 0 || index >= len(m.Pieces) {
		return
	}
	piece := m.Pieces[index]
	for i := len(piece.Children) - 1; i >= 0; i-- {
		r.bindPiece(m, piece.Children[i], load)
	}
	for primitive, pr := range piece.Primitives {
		ref, ok := r.resolve(pr.TextureName)
		if !ok || ref.kind != texAnimated || ref.entry == nil {
			continue
		}
		key := modelTexturePrimitiveKey{load: load, piece: index, primitive: primitive}
		r.bindings[key] = newModelTextureCursor(ref)
		r.players = append(r.players, r.bindings[key])
	}
}

func (r *ModelTextureRegistry) resolve(name string) (texRef, bool) {
	if r == nil {
		return texRef{}, false
	}
	return resolveTextureRef(r.primary, r.logos, name)
}

func newModelTextureCursor(ref texRef) *modelTextureCursor {
	frames := make([]*formats.GAFFrame, len(ref.entry.Frames))
	ids := make([]content.AssetID, len(frames))
	durations := make([]uint32, len(frames))
	for i, frame := range ref.entry.Frames {
		frames[i] = frame.Frame
		ids[i] = content.AssetID(ref.key + "#" + fmt.Sprint(i))
		durations[i] = uint32(frame.Value)
	}
	return &modelTextureCursor{player: presentationrender.NewTexturePlayer(content.AssetSequence{
		ID: content.AssetID(ref.key), Frames: ids, Durations: durations, Loop: ref.entry.Unknown1 != 0,
	}), frames: frames}
}

// StepPhase7 is the session callback. It snapshots the count and visits newest
// to oldest exactly once, without visibility or renderer involvement [03
// R-CRD-005 §1].
func (r *ModelTextureRegistry) StepPhase7() {
	if r == nil {
		return
	}
	n := len(r.players)
	for i := n - 1; i >= 0; i-- {
		if p := r.players[i]; p != nil {
			p.stepPhase7()
		}
	}
}

func (r *ModelTextureRegistry) animatedFrame(model *compiledmodel.Model, piece, primitive int, ref texRef) *formats.GAFFrame {
	if r == nil || model == nil {
		return nil
	}
	load, ok := r.byCompiled[model]
	if !ok {
		return nil
	}
	p := r.bindings[modelTexturePrimitiveKey{load: load, piece: piece, primitive: primitive}]
	if p == nil || p.player == nil {
		return ref.frame
	}
	index, ok := p.player.FrameIndex()
	if !ok || index >= len(p.frames) {
		return nil
	}
	return p.frames[index]
}

// modelCursor is retained for explicit standalone preview/test setup. Battle
// clients use ModelTextureRegistry and never bind a player while drawing.
func (c *Client) modelCursor(key modelTextureKey, ref texRef) *modelTextureCursor {
	if c == nil || ref.kind != texAnimated || ref.entry == nil {
		return nil
	}
	if c.modelPresentation == nil {
		c.modelPresentation = make(map[modelTextureKey]*modelTextureCursor)
	}
	if p := c.modelPresentation[key]; p != nil {
		return p
	}
	p := newModelTextureCursor(ref)
	c.modelPresentation[key] = p
	// This explicit standalone preview path retains its historical unit preview
	// cadence. Battle-loaded unit, feature, and projectile primitives are all
	// registered by ModelTextureRegistry instead; feature sprite events and
	// sprite projectile art use their own cursor owners [R-CRD-005 §1].
	if key.kind == modelCursorUnit {
		c.modelPlayers = append(c.modelPlayers, p)
	}
	return p
}

func (c *Client) modelAnimatedFrame(ref texRef, kind uint8, id uint64) *formats.GAFFrame {
	return c.modelAnimatedFrameAt(ref, kind, id, 0, 0)
}

// modelAnimatedFrameAt resolves the cursor for one concrete model primitive.
// In explicit standalone preview setup, a repeated texture on two primitives
// still receives two playback players. Battle composition uses the loaded
// primitive registry instead [R-CRD-005 §1].
func (c *Client) modelAnimatedFrameAt(ref texRef, kind uint8, id uint64, piece, primitive int) *formats.GAFFrame {
	p := c.modelCursor(modelTextureKey{kind: kind, id: id, tex: ref.key, piece: piece, primitive: primitive}, ref)
	if p == nil {
		return ref.frame
	}
	index, ok := p.player.FrameIndex()
	if !ok || index >= len(p.frames) {
		return nil
	}
	return p.frames[index]
}

func unitPresentationID(v frame.UnitView) uint64 {
	if v.InstanceID != 0 {
		return v.InstanceID
	}
	return uint64(v.Slot)
}

func featurePresentationID(v frame.FeatureView) uint64 {
	if v.InstanceID == 0 {
		return 0
	}
	return v.InstanceID
}

func projectilePresentationID(v frame.ProjectileView) uint64 {
	if v.PresentationID != 0 {
		return v.PresentationID
	}
	return uint64(v.Handle)
}

// animatedGAFFrame is the separate feature sprite/event cursor adapter; it is
// not part of the loaded-3DO phase-7 registry.
func (c *Client) animatedGAFFrame(key string, id uint64, entry *formats.GAFEntry) *formats.GAFFrame {
	if entry == nil || len(entry.Frames) <= 1 {
		if entry != nil && len(entry.Frames) == 1 {
			return entry.Frames[0].Frame
		}
		return nil
	}
	if id == 0 {
		// Not a retail question: FeatureView carries no published stable
		// identity for this animated sequence, so every feature would share
		// cursor zero and step in lockstep. Suppressing is the honest answer
		// until the publication boundary carries an identity.
		return nil
	}
	ref := texRef{kind: texAnimated, key: key, entry: entry, frame: entry.Frames[0].Frame}
	return c.modelAnimatedFrame(ref, modelCursorFeature, id)
}

// String names the texture kind for diagnostics.
func (k texKind) String() string {
	switch k {
	case texTeam:
		return "team"
	case texAnimated:
		return "animated"
	}
	return "static"
}

// StepPhase7 advances explicit standalone-preview model players once at the
// phase-7 boundary. Battle sessions install ModelTextureRegistry directly.
// The entry count is captured before traversal and entries are visited from
// newest to oldest; an append during traversal waits for the next invocation
// [R-CRD-005 §1]. Presentation metadata only is touched and no RNG stream is
// consulted [I4][I6].
func (c *Client) StepPhase7() {
	if c == nil {
		return
	}
	if c.modelTextures != nil {
		c.modelTextures.StepPhase7()
		return
	}
	n := len(c.modelPlayers)
	for i := n - 1; i >= 0; i-- {
		if p := c.modelPlayers[i]; p != nil {
			p.stepPhase7()
		}
	}
}

// buildTextureIndex enumerates textures/*.gaf and indexes entries by name.
// Team textures are the 10-frame entries (LOGOS.GAF): frame n is player n's
// colored copy [fmt 3do].
func (c *Client) buildTextureIndex() {
	if c == nil {
		return
	}
	r := newModelTextureRegistry(c.modelFS, true)
	c.texIndex, c.logoIndex = r.primary, r.logos
}

func (r *ModelTextureRegistry) buildTextureIndex() {
	fs := r.fs
	if fs == nil {
		return
	}
	seen := map[string]bool{}
	for _, e := range fs.Entries() {
		p := strings.ToLower(e.Path)
		if !strings.HasPrefix(p, "textures/") || !strings.HasSuffix(p, ".gaf") {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		g, err := formats.LoadGAFFile(fs, e.Path)
		if err != nil {
			continue
		}
		isLogos := p == "textures/logos.gaf"
		for i := range g.Entries {
			entry := &g.Entries[i]
			if len(entry.Frames) == 0 || entry.Frames[0].Frame == nil {
				continue
			}
			ref := texRef{frame: entry.Frames[0].Frame, entry: entry, key: p + "|" + strings.ToLower(entry.Name)}
			switch {
			case isLogos && len(entry.Frames) == 10:
				ref.kind = texTeam // frame n = player n, never animated
			case len(entry.Frames) > 1:
				ref.kind = texAnimated
				total := 0
				for _, fr := range entry.Frames {
					d := int(fr.Value)
					total += d
					ref.cum = append(ref.cum, total)
				}
				ref.total = total
			}
			key := strings.ToLower(entry.Name)
			if isLogos {
				r.logos[key] = ref
			} else {
				r.primary[key] = ref
			}
		}
	}
}
