package client

// Software 3DO model presentation [fmt 3do] [03 §2.5].
//
// Presentation only [I6]: nothing here touches simulation state. The client
// expands a compiled 3DO into its per-unit presentation form and adapts the
// canonical model traversal to indexed pixels; the hierarchy transforms and
// the per-corner shade rows are owned by internal/render.
//
// Which span writer a primitive takes is the shaded/unshaded split, not the
// flat/textured one: the unshaded pair write the sampled texel and the colour
// byte raw, the shaded pair write them through the primitive's SHD row, so a
// flat polygon under the shaded renderer goes through SHD exactly as a
// textured quad does [03 §4.3] [03 R-REN-03A §5].

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type modelCorner struct {
	x, y, z float64
	u, v    float64
}

type unitModel struct {
	compiled    *compiledmodel.Model
	pieceByName map[string]int // lower-case name → piece index
}

func (c *Client) orientationCache(id uint64) *presentationrender.OrientationCache {
	if c == nil {
		return nil
	}
	if c.modelOrientation == nil {
		c.modelOrientation = make(map[uint64]*presentationrender.OrientationCache)
	}
	cache := c.modelOrientation[id]
	if cache == nil {
		cache = &presentationrender.OrientationCache{}
		c.modelOrientation[id] = cache
	}
	return cache
}

// unitModelFor expands and caches a model; nil when the authored 3DO is unavailable.
func (c *Client) unitModelFor(name string) *unitModel {
	if name == "" {
		return nil
	}
	if c != nil && c.modelTextures != nil {
		return c.modelTextures.modelFor(modelLoadUnit, name, name)
	}
	if m, ok := c.models[name]; ok {
		return m
	}
	if c.modelFS == nil {
		return nil
	}
	m := c.expandModel(name)
	c.models[name] = m // nil caches too: unresolved authored models draw no pixels
	return m
}

// expandModel loads a model and applies retail's load-time contract
// [03 §2.4]:
//   - the selection primitive swaps to index 0;
//   - primitives from index one upward sort ascending by the integer mean of
//     their vertices' second coordinate (Y) — draw order is fixed here, and
//     no per-frame sort reproduces retail tie order;
//   - a recursive pass negates the first and third vertex coordinates and the
//     first and third parent translations of every object (a half-turn about
//     the vertical axis applied to the whole model).
//
// Pieces walk depth-first (root → child → sibling); colored primitives retain
// their authored polygon rings and textured faces draw only as quads
// [03 §2.4.1]. A face is dropped when its projected corner
// ring runs counter-clockwise, which is what retail's two-chain edge walk does
// to a back face [R-RAST-01 §1] step 7 — see modelFacePaints. (This comment
// previously said faces draw double-sided with no backface cull, citing
// [03 §2.4.1]; that sentence predates the scan converter's closure and is
// wrong — the cull is not a separate test, it is the span comparison.)
// This version stores the hierarchy for dynamic piece transforms [03 §2.4] C21–C22.
func (c *Client) expandModel(name string) *unitModel {
	return expandModelFromFS(c.modelFS, name)
}

// expandModelFromFS is the explicit standalone-preview recovery wrapper. A
// battle registry uses expandModelFromFSStrict during composition and reports
// a named-model failure before any tick can run [02 R-MALF-01 §5].
func expandModelFromFS(fs *vfs.FS, name string) *unitModel {
	m, _ := expandModelFromFSStrict(fs, name)
	return m
}

func expandModelFromFSStrict(fs *vfs.FS, name string) (*unitModel, error) {
	if fs == nil {
		return nil, fmt.Errorf("model %q: no virtual filesystem", name)
	}
	path := name
	if !strings.Contains(strings.ToLower(path), ".3do") {
		path = "objects3d/" + path + ".3do"
	}
	compiled, err := compiledmodel.Load(fs, path)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", path, err)
	}
	if compiled == nil || len(compiled.Pieces) == 0 {
		return nil, fmt.Errorf("model %q: empty compiled geometry", path)
	}
	byName := make(map[string]int, len(compiled.Pieces))
	for i, piece := range compiled.Pieces {
		byName[strings.ToLower(piece.Name)] = i
	}
	// Loading, selection-primitive placement, primitive ordering, and the
	// authored half-turn are complete in internal/model.Load [03 §2.4].
	return &unitModel{compiled: compiled, pieceByName: byName}, nil
}

func (c *Client) modelForUnit(v frame.UnitView) *unitModel {
	if c != nil && c.modelTextures != nil {
		// DefName/DefID choose the catalog's already-loaded model identity;
		// Model is only the no-definition fallback used by standalone previews.
		return c.modelTextures.unitModel(v.DefName, v.DefID, v.Model)
	}
	return c.unitModelFor(v.Model)
}

func (c *Client) modelForFeature(v frame.FeatureView) *unitModel {
	if c != nil && c.modelTextures != nil {
		return c.modelTextures.modelFor(modelLoadFeature, v.DefName, v.Model)
	}
	return c.unitModelFor(v.Model)
}

func (c *Client) modelForProjectile(v frame.ProjectileView) *unitModel {
	if c != nil && c.modelTextures != nil {
		return c.modelTextures.projectileModel(v.WeaponID, v.Model)
	}
	return c.unitModelFor(v.Model)
}

func (c *Client) modelForDebris(v frame.DebrisView) *unitModel {
	if c != nil && c.modelTextures != nil {
		return c.modelTextures.unitModel(v.DefName, v.DefID, v.Model)
	}
	return c.unitModelFor(v.Model)
}

// modelStates copies committed piece lanes into the canonical presentation state.
// All model callers use this representation, so hierarchy traversal and angle
// composition have one implementation in internal/render [03 §2.4][03 §5.2].
func (c *Client) modelStates(m *unitModel, pieces []frame.PieceView) []compiledmodel.PieceState {
	if m == nil || m.compiled == nil {
		return nil
	}
	states := c.borrowModelStates(len(m.compiled.Pieces))
	for _, pv := range pieces {
		idx := pv.Index
		if pv.Name != "" {
			found, ok := m.pieceByName[c.modelNameKey(pv.Name)]
			if !ok {
				continue
			}
			idx = found
		}
		if idx < 0 || idx >= len(states) {
			continue
		}
		states[idx] = compiledmodel.PieceState{
			RotX: pv.RotX, RotY: pv.RotY, RotZ: pv.RotZ,
			Trans:     [3]numeric.Fixed{pv.Tx, pv.Ty, pv.Tz},
			DontShade: pv.DontShade, Hidden: pv.Hidden, DontShadow: pv.DontShadow, DontCache: pv.DontCache,
		}
	}
	return states
}

// modelStatesForCompiled adapts the committed piece lanes for every model
// presentation consumer. Keeping name/index resolution here makes selection
// geometry and body drawing consume the same immutable hierarchy and pose
// [03 §2.4][03 §5.2].
func modelStatesForCompiled(m *compiledmodel.Model, pieces []frame.PieceView) []compiledmodel.PieceState {
	if m == nil {
		return nil
	}
	states := make([]compiledmodel.PieceState, len(m.Pieces))
	byName := make(map[string]int, len(m.Pieces))
	for i, piece := range m.Pieces {
		if piece.Name != "" {
			byName[strings.ToLower(piece.Name)] = i
		}
	}
	for _, pv := range pieces {
		idx := pv.Index
		if pv.Name != "" {
			found, ok := byName[strings.ToLower(pv.Name)]
			if !ok {
				continue
			}
			idx = found
		}
		if idx < 0 || idx >= len(states) {
			continue
		}
		states[idx] = compiledmodel.PieceState{
			RotX: pv.RotX, RotY: pv.RotY, RotZ: pv.RotZ,
			Trans:     [3]numeric.Fixed{pv.Tx, pv.Ty, pv.Tz},
			DontShade: pv.DontShade, Hidden: pv.Hidden, DontShadow: pv.DontShadow, DontCache: pv.DontCache,
		}
	}
	return states
}

// unitNanoframeReveal builds the reveal for an unfinished unit, or nil when
// the unit is complete. The pulse phase is offset by the unit's own
// identifier so neighbouring nanoframes do not pulse together [03 §5.2].
func (c *Client) unitNanoframeReveal(v frame.UnitView) (*presentationrender.NanoframeReveal, uint8) {
	if v.BuildRemaining <= 0 {
		return nil, 0
	}
	// Retail keys the pulse off the unit's own sixteen-bit identifier. The pool
	// slot index is that number in this build — a stable per-unit value in the
	// same range — so this is a naming difference, not an open question.
	band, outline := presentationrender.NanoframePulse(uint16(v.Slot), c.frameTick)
	reveal := presentationrender.BuildNanoframeReveal(v.BuildRemaining, band, outline)
	return &reveal, outline
}

// drawUnitModel uses the concrete hierarchy traversal for every unit kind.
func (c *Client) drawUnitModel(v frame.UnitView, sx, sy int32) bool {
	// The screen position is not an input: a unit's position enters the model
	// path once, at modelAnchor, from the draw record's own world position
	// [03 §5.2][R-REN-03A §1]. The parameters are the caller's bucket
	// coordinates and are retained only so the two present passes read alike.
	_, _ = sx, sy
	if c.geometryOnlyModels {
		g, live := c.unitGeometryPair(v, false)
		if g == nil {
			return false
		}
		c.list.RecordModel(drawlist.Model{Geometry: g})
		if live != nil {
			c.list.RecordModel(drawlist.Model{Geometry: live})
		}
		return true
	}
	m, ok := c.composeUnitModel(v)
	if !ok {
		return false
	}
	id := unitPresentationID(v)
	if m.direct {
		live, ok := c.composeDirectLiveModel(m.draw, unitTeamColor(v), id, modelCursorUnit, m.directLane)
		if !ok {
			return false
		}
		c.finishModel(live, nil)
		return true
	}
	if m.image == nil {
		return false
	}
	if m.image.height == nil {
		// The cached body commits first; every live piece then draws directly
		// in reverse piece order, without a key plane [03 R-REN-03A §4].
		c.finishModel(m, nil)
		live, liveOK := c.composeDirectLiveModel(m.draw, unitTeamColor(v), id, modelCursorUnit, presentationrender.PieceLaneLive)
		if liveOK {
			c.emitModel(pendingModelCommit{m: live, blit: live.image, body: true, trace: true})
		}
		return true
	}
	c.finishModel(m, nil)
	return true
}

// drawFeatureModel and drawProjectileModel share the same concrete traversal.
func (c *Client) drawFeatureModel(f frame.FeatureView) bool {
	m := c.modelForFeature(f)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	// The pseudo-unit is filled with "model pointer, position and the slot's
	// orientation words" [03 R-RAST-01 §6], so the committed record's triple
	// goes in exactly where drawUnitModel puts a unit's — heading, pitch, bank.
	// It is zero for every placement but a corpse, so map-authored 3DO features
	// draw as before and a wreck now lies the way its unit fell
	// [05 "Feature instance and terrain cell"].
	draw := presentationrender.BuildUnitDrawSimple(m.compiled, nil, f.Heading, f.Pitch, f.Bank, [3]numeric.Fixed{f.X, f.Y, f.Z})
	// The feature-backed pseudo-unit has no FBI to read and sets both the
	// structure class bit and the height-plane bit unconditionally at
	// construction, so 3DO wrecks anti-alias like buildings [R-REN-03A §2].
	draw.Structure, draw.KeyPlane = true, true
	// The nanoframe reveal is a construction-fraction contract; a sinking
	// feature is not an unfinished unit and takes the ordinary model path.
	// TODO(question): identify the feature pseudo-unit player-colour selector
	// used for LOGOS faces. The observed construction supplies model, position
	// and orientation but not a published selector, so a feature team face stays
	// absent rather than borrowing player colour zero [03 R-RAST-01 §3].
	return c.drawModel(draw, 0, teamColor{}, featurePresentationID(f), modelCursorFeature, nil, 0)
}

func (c *Client) drawProjectileModel(p frame.ProjectileView) bool {
	m := c.modelForProjectile(p)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	now := uint32(0)
	if c.buffer != nil {
		if committed := c.buffer.Current(); committed != nil {
			now = committed.Tick
		}
	}
	parentScratch, childScratch := c.borrowProjectileScratch(), c.borrowProjectileScratch()
	parent, child := presentationrender.BuildProjectileModelPiecesInto(m.compiled, p, now, parentScratch, childScratch)
	if parent == nil {
		return false
	}
	// A projectile is not a unit instance: retail draws each standalone model
	// piece through the unshaded effect entry. The parent is one call, and only
	// the header child can become the second one; grandchildren are not walked
	// [03 §5.4][03 R-COMP-02 §6]. That entry has no player-colour branch;
	// LOGOS faces use its ordinary, initial frame-zero cursor instead.
	parent.KeyPlane, parent.Structure = false, false
	if !c.drawModel(parent, 0, teamColor{}, projectilePresentationID(p), modelCursorProjectile, nil, 0) {
		return false
	}
	if child != nil {
		child.KeyPlane, child.Structure = false, false
		if !c.drawModel(child, 0, teamColor{}, projectilePresentationID(p), modelCursorProjectile, nil, 0) {
			return false
		}
	}
	return true
}

func (c *Client) drawDebrisModel(v frame.DebrisView) bool {
	m := c.modelForDebris(v)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	draw := presentationrender.BuildDebrisModelPieceInto(m.compiled, v, c.borrowProjectileScratch())
	if draw == nil {
		return false
	}
	// Whole debris enters the direct unkeyed, unshaded model path. Its owner
	// palette was captured at publication from the source raw slot [04 R-COB-04 §2].
	selector := teamColor{index: v.OwnerColor, known: v.OwnerColorKnown}
	if !c.directModelOriginVisible(draw) {
		return false
	}
	if c.geometryOnlyModels {
		g := c.directDebrisGeometry(draw, selector, uint64(v.Slot))
		if g == nil {
			return false
		}
		c.list.RecordModel(drawlist.Model{Geometry: g})
		return true
	}
	direct, ok := c.composeDirectDebrisModel(draw, selector, uint64(v.Slot))
	if !ok {
		return false
	}
	c.finishModel(direct, nil)
	// TODO(RT08): whole-debris smoke and flame are per-render-frame CRT trail
	// producers; presentation CRT ownership is not established at this seam.
	return true
}

// modelLocalVertex narrows one world-space piece vertex to the composition
// image's model-relative pixel offsets and returns the whole-unit height that
// feeds the key. Retail narrows the model-relative 16.16 value once, by
// extracting its high word — an arithmetic shift, so it floors — and applies
// the half-height shear with a second arithmetic shift [03 §2.5][R-REN-03A §1].
//
// The Z component carries the 3DO handedness flip: retail forms `Zn = hi16(-vz)`
// — the negation happens BEFORE the narrowing, so the result is `-ceil(vz)` and
// not `-floor(vz)`, and a vertex a fraction above zero lands one pixel higher on
// screen [R-RAST-01 §2]. A unit's own position enters the blit unnegated, so
// model space is mirrored in Z against world space; the selection quad of
// [03 R-WATER-01 §1] rule 3 already spells the same negation out. This flip was
// missing here, which rendered every model a half circle from its direction of
// travel while its turn sense still looked right — the "walks backwards" defect.
// It is corrected together with the root heading fold of [03 §2.4] C24, which
// had been negated to compensate; either one alone reverses the turn sense.
func modelLocalVertex(v, origin [3]numeric.Fixed) (lx, ly, ry int32) {
	rx := int32(v[0].Sub(origin[0]).Floor())
	ry = int32(v[1].Sub(origin[1]).Floor())
	zn := int32((-(v[2].Sub(origin[2]))).Floor()) // Zn = hi16(-vz) [R-RAST-01 §2]
	return rx, zn - (ry >> 1), ry
}

// scaleModelLocal applies the presentation view scale to a model-relative
// offset. Retail has no scale; at the retail scale of 1 this is the identity
// and the offsets stay exactly as [R-REN-03A §1] computes them. At the detail
// scale it is an exact integer multiply, so a doubled model lands on the pixel
// grid one-to-one (DESIGN_GPU_RENDERER §14.2).
func (c *Client) scaleModelLocal(lx, ly int32) (int32, int32) {
	s := c.modelScale()
	if s == 1 {
		return lx, ly
	}
	return lx * s, ly * s
}

// modelScale is the factor model geometry is projected by, and modelBlitScale
// the factor the finished image is blitted by; their product is the view scale.
// Enhanced rasterizes the scaled geometry (smooth edges, texels doubled) and
// blits one-to-one; Original rasterizes at native size and doubles the image
// on the blit, so its detail-scale frame is a pure nearest upscale of the
// native frame (DESIGN_GPU_RENDERER §14.2).
func (c *Client) modelScale() int32 {
	if c == nil || !c.enhanced {
		return 1
	}
	return c.viewScale()
}

func (c *Client) modelBlitScale() int32 {
	if c == nil || c.enhanced {
		return 1
	}
	return c.viewScale()
}

// modelHeightKey computes the per-vertex key shared by completed-model
// composition and nanoframe reveal: the whole world height above the unit
// origin, biased so geometry below the origin still keys non-negative
// [R-REN-03A §2].
//
// This previously halved the height. That was read off the anti-aliased vertex
// path, which doubles the vertex two steps earlier and divides by two only to
// undo it; the plain path adds the undivided height. Halving threw away half
// the depth resolution and roughly doubled how often two faces tie
// [03 R-REN-03A §2].
//
// The narrowing floors rather than truncating toward zero: retail extracts the
// high word of the model-relative 16.16 value with an arithmetic shift, not
// through __ftol, so I3's truncate-toward-zero rule does not apply here
// [R-REN-03A §2].
//
// A definition authoring the FBI Digger key raises every key by a further 75,
// and the finished image is then erased wherever the key is at or below 125
// [R-REN-03A §8]. The offset alone would only shift every key uniformly, so the
// two halves go together: drawModel runs the erase, this function supplies the
// raised keys it acts on. Three stock units author it — ARMAMB, CORTOAST,
// CORVIPE — and for those the erase removes exactly the geometry at or below
// the model origin, which is the buried half of a pop-up defence.
func modelHeightKey(relativeY numeric.Fixed, digger bool) int32 {
	key := int32(relativeY.Floor()) + presentationrender.NanoframeHeightBias
	if digger {
		key += diggerKeyBias
	}
	return key
}

// diggerKeyBias is the extra height-key offset a definition authoring the FBI
// Digger key carries, and diggerEraseThreshold is the key at or below which the
// finished image is erased. The threshold is the sum of the two bases, so it
// lands exactly at the model origin [R-REN-03A §8].
const (
	diggerKeyBias        int32 = 75
	diggerEraseThreshold       = diggerKeyBias + presentationrender.NanoframeHeightBias
)

// groundHeightUnder samples the terrain height beneath a unit, which is what
// the shadow shear uses [R-REN-03D §3].
func (c *Client) groundHeightUnder(x, z numeric.Fixed) numeric.Fixed {
	if c == nil || c.terrain == nil {
		return 0
	}
	return c.terrain.HeightAt(x, z)
}
