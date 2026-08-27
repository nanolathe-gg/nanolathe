package client

// Software 3DO model presentation [fmt 3do][03 §2.5].
//
// Presentation only (I6): this file never touches sim state. It expands a
// The client adapts the canonical model traversal to indexed pixels; hierarchy
// transforms and face shading are owned by internal/render.
//
// Flat-colored primitives bypass SHD; textured primitives use the canonical
// per-vertex rows [03 §4.3][03 §2.4.1].

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

type modelCorner struct {
	x, y, z float64
	u, v    float64
}

type unitModel struct {
	compiled    *compiledmodel.Model
	pieceByName map[string]int // lower-case name → piece index
	// tris is retained only for the opt-in probe test's reporting surface; the
	// production draw path never reads or populates this diagnostic cache.
	tris []modelTri
}

type modelTri struct {
	c      [3]modelCorner
	piece  string
	color  uint8
	hasTex bool
}

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
	key   string            // immutable catalog identity used to key per-instance cursors
	frame *formats.GAFFrame // static frame (kind static)
	entry *formats.GAFEntry // team/animated: full frame list
	cum   []int             // animated: cumulative delay ticks per frame
	total int               // animated: full-cycle length in ticks
}

// modelTextureCursor binds the generic presentation cursor to decoded GAF
// frames. The cursor owns only an AssetID sequence; this adapter keeps the
// already-resolved frame pointers alongside it, so draw loops never perform a
// VFS lookup or share animation phase between instances [03 §2.4.1][03 §4.4].
type modelTextureCursor struct {
	player *presentationrender.TexturePlayer
	frames []*formats.GAFFrame
}

type modelTextureKey struct {
	kind uint8
	id   uint64
	tex  string
}

const (
	modelCursorUnit       uint8 = 0 // [03 §2.4.1] unit model texture cursor family
	modelCursorFeature    uint8 = 1 // [03 §5.1] feature model texture cursor family
	modelCursorProjectile uint8 = 2 // [03 §5.2] projectile model texture cursor family
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
	frames := make([]*formats.GAFFrame, len(ref.entry.Frames))
	ids := make([]content.AssetID, len(frames))
	durations := make([]uint32, len(frames))
	for i, frame := range ref.entry.Frames {
		frames[i] = frame.Frame
		ids[i] = content.AssetID(ref.key + "#" + fmt.Sprint(i))
		durations[i] = uint32(frame.Value)
	}
	p := &modelTextureCursor{
		player: presentationrender.NewTexturePlayer(content.AssetSequence{
			ID: content.AssetID(ref.key), Frames: ids, Durations: durations, Loop: true,
		}),
		frames: frames,
	}
	c.modelPresentation[key] = p
	return p
}

func (c *Client) modelAnimatedFrame(ref texRef, kind uint8, id uint64) *formats.GAFFrame {
	p := c.modelCursor(modelTextureKey{kind: kind, id: id, tex: ref.key}, ref)
	if p == nil {
		return ref.frame
	}
	asset, ok := p.player.Frame()
	if !ok {
		return nil
	}
	for i := range p.frames {
		if content.AssetID(ref.key+"#"+fmt.Sprint(i)) == asset {
			return p.frames[i]
		}
	}
	return nil
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

// animatedGAFFrame is the same per-instance cursor adapter for feature
// sequences. The instance identity comes from FeatureView.InstanceID.
func (c *Client) animatedGAFFrame(key string, id uint64, entry *formats.GAFEntry) *formats.GAFFrame {
	if entry == nil || len(entry.Frames) <= 1 {
		if entry != nil && len(entry.Frames) == 1 {
			return entry.Frames[0].Frame
		}
		return nil
	}
	if id == 0 {
		// TODO(question): FeatureView lacks a published stable identity for this
		// animated sequence; suppress it rather than sharing cursor zero.
		return nil
	}
	ref := texRef{kind: texAnimated, key: key, entry: entry, frame: entry.Frames[0].Frame}
	return c.modelAnimatedFrame(ref, modelCursorFeature, id)
}

func (k texKind) String() string {
	switch k {
	case texTeam:
		return "team"
	case texAnimated:
		return "animated"
	}
	return "static"
}

// TickTextureAnimators advances the texture-animation clock by n simulation
// frames. Presentation-only state (I6): animated model textures tick with the
// simulation frame rate, driven here from the session owner. Divergence
// (documented): retail runs one player per 3DO instance so instances drift
// apart; Nanolathe phases all instances from one clock until snapshots carry
// spawn ticks.
func (c *Client) TickTextureAnimators(n int) {
	if c == nil || n <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		for _, p := range c.modelPresentation {
			p.player.Advance(true)
		}
	}
}

// buildTextureIndex enumerates textures/*.gaf and indexes entries by name.
// Team textures are the 10-frame entries (LOGOS.GAF): frame n is player n's
// colored copy [fmt 3do].
func (c *Client) buildTextureIndex() {
	fs := c.modelFS
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
			c.texIndex[strings.ToLower(entry.Name)] = ref
		}
	}
}

// unitModelFor expands and caches a model; nil when the authored 3DO is unavailable.
func (c *Client) unitModelFor(name string) *unitModel {
	if name == "" {
		return nil
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
// Pieces walk depth-first (root → child → sibling); primitives fan-triangulate
// [03 §2.4 "N-gon primitives"]. Faces draw double-sided in the 8-bit path —
// no backface cull exists in the established model composer [03 §2.4.1].
// This version stores the hierarchy for dynamic piece transforms [03 §2.4] C21–C22.
func (c *Client) expandModel(name string) *unitModel {
	path := name
	if !strings.Contains(strings.ToLower(path), ".3do") {
		path = "objects3d/" + path + ".3do"
	}
	compiled, err := compiledmodel.Load(c.modelFS, path)
	if err != nil || compiled == nil || len(compiled.Pieces) == 0 {
		return nil
	}
	byName := make(map[string]int, len(compiled.Pieces))
	for i, piece := range compiled.Pieces {
		byName[strings.ToLower(piece.Name)] = i
	}
	// Loading, selection-primitive placement, primitive ordering, and the
	// authored half-turn are complete in internal/model.Load [03 §2.4].
	return &unitModel{compiled: compiled, pieceByName: byName}
}

type screenTri struct {
	x, y  [3]int32
	u, v  [3]float64
	row   [3]float64 // interpolated SHD row, per corner [03 §2.4.1]
	color uint8
	frame *formats.GAFFrame
	entry *formats.GAFEntry
	team  bool
	order int
	depth int32 // mean screen y for painter order
}

type modelPrimitiveMode uint8

const (
	modelPrimitiveSkip modelPrimitiveMode = iota
	modelPrimitiveFlat
	modelPrimitiveTexture
)

// modelPrimitiveDispatch preserves the authored IsColored precedence. A
// missing texture takes the established flat-quad miss path; unsupported
// flat arities remain suppressed [fmt 3do][03 §2.4.1].
func modelPrimitiveDispatch(pr presentationrender.PrimitiveDraw, resolved bool) modelPrimitiveMode {
	n := len(pr.VertexIndices)
	if pr.TextureName == "" {
		if pr.IsColored == 0 || n != 4 {
			return modelPrimitiveSkip
		}
		return modelPrimitiveFlat
	}
	if !resolved {
		if n != 4 {
			return modelPrimitiveSkip
		}
		return modelPrimitiveFlat
	}
	// Only the canonical flag and an in-range color override a resolved
	// texture. Other nonzero values are editor data and retain texturing
	// [fmt 3do].
	if pr.IsColored == 1 && pr.ColorIndex < 256 {
		return modelPrimitiveFlat
	}
	return modelPrimitiveTexture
}

// modelStates copies committed piece lanes into the canonical presentation state.
// All model callers use this representation, so hierarchy traversal and angle
// composition have one implementation in internal/render [03 §2.4][03 §5.2].
func (c *Client) modelStates(m *unitModel, pieces []frame.PieceView) []compiledmodel.PieceState {
	if m == nil || m.compiled == nil {
		return nil
	}
	states := make([]compiledmodel.PieceState, len(m.compiled.Pieces))
	for _, pv := range pieces {
		idx := pv.Index
		if pv.Name != "" {
			found, ok := m.pieceByName[strings.ToLower(pv.Name)]
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
			DontShade: pv.DontShade, Hidden: pv.Hidden, DontShadow: pv.DontShadow,
		}
	}
	return states
}

// collectDrawTris adapts canonical render records to the indexed framebuffer.
// It owns no hierarchy math: units, features, and projectiles all pass through
// render.UnitDraw [03 §2.4][03 §5.2].
func (c *Client) collectDrawTris(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8) []screenTri {
	if c == nil || c.cam == nil || draw == nil || draw.Model == nil {
		return nil
	}
	var tris []screenTri
	for pi, piece := range draw.Pieces {
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		for pri, pr := range piece.Primitives {
			if draw.Model.Pieces[pi].Selection && pri == 0 {
				continue // selection plate is retained by the model but never rasterized [03 §2.4.1]
			}
			n := len(pr.VertexIndices)
			if n < 3 {
				continue
			}
			ref, textured := resolveTextureRef(nil, c.texIndex, pr.TextureName)
			mode := modelPrimitiveDispatch(pr, textured)
			if mode == modelPrimitiveSkip {
				continue
			}
			color := uint8(pr.ColorIndex & 0xff)
			if pr.TextureName != "" && !textured {
				color = 0xd1 // unresolved authored texture is the established flat miss [03 §2.4.1]
			}
			var texFrame *formats.GAFFrame
			if mode == modelPrimitiveTexture {
				switch ref.kind {
				case texAnimated:
					if kind == modelCursorFeature && id == 0 {
						// TODO(question): FeatureView lacks a published stable
						// identity for animated model textures; suppress this
						// sequence rather than sharing cursor zero.
						continue
					}
					texFrame = c.modelAnimatedFrame(ref, kind, id)
				case texTeam:
					if ref.entry == nil {
						continue
					}
					oi := int(owner) % 10
					if oi >= 0 && oi < len(ref.entry.Frames) {
						texFrame = ref.entry.Frames[oi].Frame
					}
				default:
					texFrame = ref.frame
				}
				if texFrame == nil {
					continue
				}
			}
			if mode == modelPrimitiveTexture && n > 4 {
				// TODO(question): the published PrimitiveDraw has no edge-table
				// scanline inputs for retail's affine 5–16-gon mapper. Suppress
				// unsupported authored n-gons rather than inventing fan/bounds UVs
				// [fmt 3do][03 §2.4.1].
				continue
			}
			valid := true
			for _, vi := range pr.VertexIndices {
				if int(vi) >= len(piece.WorldVertices) {
					valid = false
					break
				}
			}
			if !valid {
				continue // malformed primitive suppresses the whole face [fmt 3do]
			}
			uvFor := func(corner int) (float64, float64) {
				uv := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
				return uv[corner][0], uv[corner][1]
			}
			for k := 1; k+1 < n; k++ { // quads split only for the indexed raster adapter [03 §2.4.1]
				indices := [3]int{int(pr.VertexIndices[0]), int(pr.VertexIndices[k]), int(pr.VertexIndices[k+1])}
				var tri screenTri
				tri.color, tri.frame, tri.order = color, texFrame, pri
				for corner, vi := range indices {
					v := piece.WorldVertices[vi]
					sx, sy := c.cam.WorldToScreen(v[0], v[1], v[2])
					tri.x[corner], tri.y[corner] = sx-camera.OriginX, sy-camera.OriginY
					tri.depth += tri.y[corner]
					primitiveCorner := [3]int{0, k, k + 1}[corner]
					if primitiveCorner < len(pr.ShadeRows) {
						tri.row[corner] = float64(pr.ShadeRows[primitiveCorner])
					} else {
						tri.row[corner] = 15
					}
				}
				tri.depth /= 3
				if texFrame != nil {
					tri.u[0], tri.v[0] = uvFor(0)
					tri.u[1], tri.v[1] = uvFor(k)
					tri.u[2], tri.v[2] = uvFor(k + 1)
				}
				tris = append(tris, tri)
			}
		}
	}
	return tris
}

// drawModel is the sole indexed model raster entry. Traversal is performed by
// internal/render and this function only resolves authored textures and blits
// the resulting triangles [03 §2.4][03 §2.4.1].
func (c *Client) drawModel(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8, nanoframe bool) bool {
	// TODO(question): model shadows remain suppressed because the published
	// frame has no visual-options word, terrain ground/depth input, or model
	// shadow flag needed by the established pre-body shadow path [03 §5.3].
	tris := c.collectDrawTris(draw, owner, id, kind)
	for i := range tris {
		if nanoframe {
			if tris[i].frame != nil {
				c.blitTexturedTriNanoframe(&tris[i], tris[i].frame)
			} else {
				c.fillTriNanoframe(&tris[i], tris[i].color)
			}
		} else if tris[i].frame != nil {
			c.blitTexturedTri(&tris[i], tris[i].frame)
		} else {
			c.fillTri(&tris[i], tris[i].color)
		}
	}
	return len(tris) != 0
}

// drawUnitModel uses the concrete hierarchy traversal for every unit kind.
func (c *Client) drawUnitModel(v frame.UnitView, sx, sy int32) bool {
	m := c.unitModelFor(v.Model)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	states := c.modelStates(m, v.Pieces)
	draw := presentationrender.BuildUnitDraw(m.compiled, states, v.Heading, v.Pitch, v.Bank, v, c.orientationCache(unitPresentationID(v)))
	return c.drawModel(draw, v.Owner, unitPresentationID(v), modelCursorUnit, v.BuildRemaining > 0)
}

// drawFeatureModel and drawProjectileModel share the same concrete traversal.
func (c *Client) drawFeatureModel(f frame.FeatureView) bool {
	m := c.unitModelFor(f.Model)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	draw := presentationrender.BuildUnitDrawSimple(m.compiled, nil, 0, 0, 0, [3]numeric.Fixed{f.X, f.Y, f.Z})
	return c.drawModel(draw, 0, featurePresentationID(f), modelCursorFeature, f.IsSinking)
}

func (c *Client) drawProjectileModel(p frame.ProjectileView) bool {
	m := c.unitModelFor(p.Model)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	draw := presentationrender.BuildProjectileDraw(m.compiled, nil, p.Yaw, p.Pitch, [3]numeric.Fixed{p.X, p.Y, p.Z})
	return c.drawModel(draw, 0, projectilePresentationID(p), modelCursorProjectile, false)
}

// fillTriNanoframe is the nanoframe variant of fillTri with stipple [03 §5.7].
func (c *Client) fillTriNanoframe(t *screenTri, color uint8) {
	minX, minY, maxX, maxY := t.x[0], t.y[0], t.x[0], t.y[0]
	for k := 1; k < 3; k++ {
		if t.x[k] < minX {
			minX = t.x[k]
		}
		if t.x[k] > maxX {
			maxX = t.x[k]
		}
		if t.y[k] < minY {
			minY = t.y[k]
		}
		if t.y[k] > maxY {
			maxY = t.y[k]
		}
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= int32(c.width) {
		maxX = int32(c.width - 1)
	}
	if maxY >= int32(c.height) {
		maxY = int32(c.height - 1)
	}
	for py := minY; py <= maxY; py++ {
		row := py * int32(c.width)
		for px := minX; px <= maxX; px++ {
			if (px+py)%2 == 0 {
				continue // stipple [03 §5.7] nanoframe translucency
			}
			if pointInTri(t, px, py) {
				c.indexed[row+px] = color
			}
		}
	}
}

// blitTexturedTriNanoframe is the nanoframe stipple variant of blitTexturedTri [03 §5.7].
func (c *Client) blitTexturedTriNanoframe(t *screenTri, frame *formats.GAFFrame) {
	minX, minY, maxX, maxY := t.x[0], t.y[0], t.x[0], t.y[0]
	for k := 1; k < 3; k++ {
		if t.x[k] < minX {
			minX = t.x[k]
		}
		if t.x[k] > maxX {
			maxX = t.x[k]
		}
		if t.y[k] < minY {
			minY = t.y[k]
		}
		if t.y[k] > maxY {
			maxY = t.y[k]
		}
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= int32(c.width) {
		maxX = int32(c.width - 1)
	}
	if maxY >= int32(c.height) {
		maxY = int32(c.height - 1)
	}
	d := baryDenom(t)
	if d == 0 {
		return
	}
	w, h := int(frame.Width), int(frame.Height)
	for py := minY; py <= maxY; py++ {
		row := py * int32(c.width)
		for px := minX; px <= maxX; px++ {
			if (px+py)%2 == 0 {
				continue
			}
			l0, l1, l2 := bary(t, d, px, py)
			if l0 < 0 || l1 < 0 || l2 < 0 {
				continue
			}
			u := l0*t.u[0] + l1*t.u[1] + l2*t.u[2]
			v := l0*t.v[0] + l1*t.v[1] + l2*t.v[2]
			tx, ty := int(u*float64(w)), int(v*float64(h))
			if tx < 0 {
				tx = 0
			} else if tx >= w {
				tx = w - 1
			}
			if ty < 0 {
				ty = 0
			} else if ty >= h {
				ty = h - 1
			}
			b, ok := frame.At(tx, ty)
			if !ok {
				continue
			}
			r := l0*t.row[0] + l1*t.row[1] + l2*t.row[2]
			ri := int(r)
			if ri < 0 {
				ri = 0
			} else if ri > 31 {
				ri = 31
			}
			if c.pal != nil {
				b = c.pal.Shade[ri][b]
			}
			c.indexed[row+px] = b
		}
	}
}

func (c *Client) fillTri(t *screenTri, color uint8) {
	minX, minY, maxX, maxY := t.x[0], t.y[0], t.x[0], t.y[0]
	for k := 1; k < 3; k++ {
		if t.x[k] < minX {
			minX = t.x[k]
		}
		if t.x[k] > maxX {
			maxX = t.x[k]
		}
		if t.y[k] < minY {
			minY = t.y[k]
		}
		if t.y[k] > maxY {
			maxY = t.y[k]
		}
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= int32(c.width) {
		maxX = int32(c.width - 1)
	}
	if maxY >= int32(c.height) {
		maxY = int32(c.height - 1)
	}
	for py := minY; py <= maxY; py++ {
		row := py * int32(c.width)
		for px := minX; px <= maxX; px++ {
			if pointInTri(t, px, py) {
				c.indexed[row+px] = color
			}
		}
	}
}

// blitTexturedTri affinely samples the texture frame across the triangle,
// routing every sample through PALETTE.SHD at the barycentrically
// interpolated per-vertex row: row = ftol(dot(N, L)*5) & 31 with the piece's
// smooth vertex normals [03 §2.4.1]; flat-colored faces take no SHD at all.
// Transparent texels skip.
func (c *Client) blitTexturedTri(t *screenTri, frame *formats.GAFFrame) {
	minX, minY, maxX, maxY := t.x[0], t.y[0], t.x[0], t.y[0]
	for k := 1; k < 3; k++ {
		if t.x[k] < minX {
			minX = t.x[k]
		}
		if t.x[k] > maxX {
			maxX = t.x[k]
		}
		if t.y[k] < minY {
			minY = t.y[k]
		}
		if t.y[k] > maxY {
			maxY = t.y[k]
		}
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= int32(c.width) {
		maxX = int32(c.width - 1)
	}
	if maxY >= int32(c.height) {
		maxY = int32(c.height - 1)
	}
	d := baryDenom(t)
	if d == 0 {
		return
	}
	w, h := int(frame.Width), int(frame.Height)
	for py := minY; py <= maxY; py++ {
		row := py * int32(c.width)
		for px := minX; px <= maxX; px++ {
			l0, l1, l2 := bary(t, d, px, py)
			if l0 < 0 || l1 < 0 || l2 < 0 {
				continue
			}
			u := l0*t.u[0] + l1*t.u[1] + l2*t.u[2]
			v := l0*t.v[0] + l1*t.v[1] + l2*t.v[2]
			tx, ty := int(u*float64(w)), int(v*float64(h))
			if tx < 0 {
				tx = 0
			} else if tx >= w {
				tx = w - 1
			}
			if ty < 0 {
				ty = 0
			} else if ty >= h {
				ty = h - 1
			}
			b, ok := frame.At(tx, ty)
			if !ok {
				continue
			}
			r := l0*t.row[0] + l1*t.row[1] + l2*t.row[2]
			ri := int(r)
			if ri < 0 {
				ri = 0
			} else if ri > 31 {
				ri = 31
			}
			if c.pal != nil {
				b = c.pal.Shade[ri][b]
			}
			c.indexed[row+px] = b
		}
	}
}

func edge(ax, ay, bx, by, px, py int32) int32 {
	return (bx-ax)*(py-ay) - (by-ay)*(px-ax)
}

func baryDenom(t *screenTri) int32 {
	return edge(t.x[0], t.y[0], t.x[1], t.y[1], t.x[2], t.y[2])
}

func bary(t *screenTri, d int32, px, py int32) (float64, float64, float64) {
	w0 := edge(t.x[1], t.y[1], t.x[2], t.y[2], px, py)
	w1 := edge(t.x[2], t.y[2], t.x[0], t.y[0], px, py)
	w2 := edge(t.x[0], t.y[0], t.x[1], t.y[1], px, py)
	fd := float64(d)
	if fd < 0 {
		fd = -fd
	}
	l0 := float64(w0) / fd
	l1 := float64(w1) / fd
	l2 := float64(w2) / fd
	if d < 0 {
		// Keep barycentrics consistent under flipped winding.
		return -l0, -l1, -l2
	}
	return l0, l1, l2
}

func pointInTri(t *screenTri, px, py int32) bool {
	d := baryDenom(t)
	if d == 0 {
		return false
	}
	l0, l1, l2 := bary(t, d, px, py)
	return (l0 >= 0 && l1 >= 0 && l2 >= 0) || (l0 <= 0 && l1 <= 0 && l2 <= 0)
}

// Destination helpers for the offscreen cache share the same raster rules as
// fillTri/blitTexturedTri while writing an arbitrary indexed target.

func fillTriToDest(dest []byte, mask []bool, w, h int, t *screenTri, color uint8) {
	minX, minY, maxX, maxY := t.x[0], t.y[0], t.x[0], t.y[0]
	for k := 1; k < 3; k++ {
		if t.x[k] < minX {
			minX = t.x[k]
		}
		if t.x[k] > maxX {
			maxX = t.x[k]
		}
		if t.y[k] < minY {
			minY = t.y[k]
		}
		if t.y[k] > maxY {
			maxY = t.y[k]
		}
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= int32(w) {
		maxX = int32(w - 1)
	}
	if maxY >= int32(h) {
		maxY = int32(h - 1)
	}
	for py := minY; py <= maxY; py++ {
		row := py * int32(w)
		for px := minX; px <= maxX; px++ {
			if pointInTri(t, px, py) {
				idx := row + px
				dest[idx] = color
				if mask != nil {
					mask[idx] = true
				}
			}
		}
	}
}

func blitTexturedTriToDest(dest []byte, mask []bool, w, h int, t *screenTri, frame *formats.GAFFrame, pal *palette.Tables) {
	minX, minY, maxX, maxY := t.x[0], t.y[0], t.x[0], t.y[0]
	for k := 1; k < 3; k++ {
		if t.x[k] < minX {
			minX = t.x[k]
		}
		if t.x[k] > maxX {
			maxX = t.x[k]
		}
		if t.y[k] < minY {
			minY = t.y[k]
		}
		if t.y[k] > maxY {
			maxY = t.y[k]
		}
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= int32(w) {
		maxX = int32(w - 1)
	}
	if maxY >= int32(h) {
		maxY = int32(h - 1)
	}
	d := baryDenom(t)
	if d == 0 {
		return
	}
	fw, fh := int(frame.Width), int(frame.Height)
	for py := minY; py <= maxY; py++ {
		row := py * int32(w)
		for px := minX; px <= maxX; px++ {
			l0, l1, l2 := bary(t, d, px, py)
			if l0 < 0 || l1 < 0 || l2 < 0 {
				continue
			}
			u := l0*t.u[0] + l1*t.u[1] + l2*t.u[2]
			v := l0*t.v[0] + l1*t.v[1] + l2*t.v[2]
			tx, ty := int(u*float64(fw)), int(v*float64(fh))
			if tx < 0 {
				tx = 0
			} else if tx >= fw {
				tx = fw - 1
			}
			if ty < 0 {
				ty = 0
			} else if ty >= fh {
				ty = fh - 1
			}
			b, ok := frame.At(tx, ty)
			if !ok {
				continue
			}
			r := l0*t.row[0] + l1*t.row[1] + l2*t.row[2]
			ri := int(r)
			if ri < 0 {
				ri = 0
			} else if ri > 31 {
				ri = 31
			}
			if pal != nil {
				b = pal.Shade[ri][b]
			}
			idx := row + px
			dest[idx] = b
			if mask != nil {
				mask[idx] = true
			}
		}
	}
}

// drawUnitChrome overlays selection brackets and the health bar for a
// model-drawn unit. Geometry follows the footprint box [04 §6.2]; the model
// itself may extend above it.
func (c *Client) drawUnitChrome(v frame.UnitView, sx, sy int32) {
	fx, fz := int(v.FootX), int(v.FootZ)
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	const pxPerCell = 16
	halfW := int32(fx * pxPerCell / 2)
	halfH := int32(fz * pxPerCell / 2)
	if halfW < 4 {
		halfW = 4
	}
	if halfH < 4 {
		halfH = 4
	}
	if v.Flags&SelectionFlag != 0 {
		corners := [4][2]int32{
			{sx - halfW, sy - halfH}, {sx + halfW, sy - halfH},
			{sx + halfW, sy + halfH}, {sx - halfW, sy + halfH},
		}
		for _, cnr := range corners {
			for d := int32(-3); d <= 3; d++ {
				for _, off := range [2][2]int32{{d, 0}, {0, d}} {
					px, py := cnr[0]+off[0], cnr[1]+off[1]
					if px >= 0 && px < int32(c.width) && py >= 0 && py < int32(c.height) {
						c.indexed[py*int32(c.width)+px] = unitStyle.SelectWhite
					}
				}
			}
		}
	}
	if v.MaxHealth > 0 && (v.Health != v.MaxHealth || v.Flags&SelectionFlag != 0) {
		bw := halfW * 2
		if bw < 12 {
			bw = 12
		}
		frac := float64(v.Health) / float64(v.MaxHealth)
		if frac < 0 {
			frac = 0
		} else if frac > 1 {
			frac = 1
		}
		by := sy + halfH + 2
		background := c.paletteIndex(0)
		fill := c.retailHealthColor(v.Health, v.MaxHealth)
		for i := int32(0); i < bw; i++ {
			px := sx - bw/2 + i
			if px < 0 || px >= int32(c.width) || by < 0 || by >= int32(c.height) {
				continue
			}
			idx := background
			if float64(i) < frac*float64(bw) {
				idx = fill
			}
			c.indexed[by*int32(c.width)+px] = idx
		}
	}
}
