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
	"github.com/nanolathe/nanolathe/internal/hud"
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
	// Only unit model instances belong to phase 7. Feature and projectile
	// sequences are advanced by their owning presentation paths
	// [R-CRD-005 §1]. Registration is append-only here; retail teardown
	// ownership is unknown, so do not invent removal or compaction.
	if key.kind == modelCursorUnit {
		c.modelPlayers = append(c.modelPlayers, p)
	}
	return p
}

func (c *Client) modelAnimatedFrame(ref texRef, kind uint8, id uint64) *formats.GAFFrame {
	return c.modelAnimatedFrameAt(ref, kind, id, 0, 0)
}

// modelAnimatedFrameAt resolves the cursor for one concrete model primitive.
// A repeated texture on two primitives still represents two cloned playback
// players under the retail model-instance contract [R-CRD-005 §1].
func (c *Client) modelAnimatedFrameAt(ref texRef, kind uint8, id uint64, piece, primitive int) *formats.GAFFrame {
	p := c.modelCursor(modelTextureKey{kind: kind, id: id, tex: ref.key, piece: piece, primitive: primitive}, ref)
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

// StepPhase7 advances the registered unit model-texture players once at the
// phase-7 boundary. The entry count is captured before traversal and entries
// are visited from newest to oldest; an append during traversal waits for the
// next invocation [R-CRD-005 §1]. Presentation metadata only is touched and
// no RNG stream is consulted [I4][I6].
func (c *Client) StepPhase7() {
	if c == nil {
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
	x, y       [3]int32
	u, v       [3]float64
	row        [3]float64 // interpolated SHD row, per corner [03 §2.4.1]
	key        [3]float64 // interpolated nanoframe height key, per corner [03 §5.2]
	color      uint8
	frame      *formats.GAFFrame
	entry      *formats.GAFEntry
	team       bool
	useSHD     bool // textured texels pass through PALETTE.SHD [R-RND-02A]
	order      int
	candidate  uint32
	piece      int
	primitive  int
	texture    string
	frameIndex int
	frameState RendererValueState
	depth      int32 // mean screen y for painter order
}

// modelTarget is the per-unit indexed composition image. Its color, height,
// and coverage planes stay private until the complete model (including its
// nanoframe reveal) is finished; uncovered pixels therefore leave terrain and
// earlier world passes untouched [03 §5.2].
type modelTarget struct {
	color, height          []uint8
	covered                []bool
	width, heightPx        int
	minX, minY, maxX, maxY int32
	trace                  *rendererTrace
	winner                 []int
	tick                   uint32
}

func newModelTarget(width, height int) *modelTarget {
	n := width * height
	return &modelTarget{
		color: make([]uint8, n), height: make([]uint8, n), covered: make([]bool, n),
		width: width, heightPx: height, maxX: int32(width - 1), maxY: int32(height - 1),
		winner: nil,
	}
}

func (t *modelTarget) admit(idx int, key uint8) bool {
	if t.height[idx] > key {
		return false
	}
	t.height[idx] = key
	return true
}

func (t *modelTarget) write(idx int, color uint8, present bool) {
	if !present {
		// Nanoframe erase removes the composed unit pixel. It must not write
		// an indexed zero into the destination world image [03 §5.2].
		t.covered[idx] = false
		return
	}
	t.color[idx], t.covered[idx] = color, true
}

func (t *modelTarget) commit(dst []uint8) {
	for py := t.minY; py <= t.maxY; py++ {
		row := int(py) * t.width
		for px := t.minX; px <= t.maxX; px++ {
			i := row + int(px)
			if t.covered[i] {
				dst[i] = t.color[i]
			}
		}
	}
}

func (c *Client) reusableModelTarget(tris []screenTri) *modelTarget {
	n := len(c.indexed)
	if len(c.modelTargetColor) != n {
		c.modelTargetColor = make([]uint8, n)
		c.modelTargetHeight = make([]uint8, n)
		c.modelTargetCovered = make([]bool, n)
	}
	t := &modelTarget{
		color: c.modelTargetColor, height: c.modelTargetHeight, covered: c.modelTargetCovered,
		width: c.width, heightPx: c.height, minX: 1, minY: 1, maxX: 0, maxY: 0,
		tick: c.frameTick,
	}
	if c.rendererTraceSink != nil {
		t.trace = newRendererTrace(n)
		t.winner = t.trace.winner
		t.trace.unit = 0
		t.trace.tick = c.frameTick
		t.trace.width = c.width
		t.trace.height = c.height
	}
	if len(tris) == 0 {
		return t
	}
	t.minX, t.minY, t.maxX, t.maxY = c.triBounds(&tris[0])
	for i := 1; i < len(tris); i++ {
		x0, y0, x1, y1 := c.triBounds(&tris[i])
		if x0 < t.minX {
			t.minX = x0
		}
		if y0 < t.minY {
			t.minY = y0
		}
		if x1 > t.maxX {
			t.maxX = x1
		}
		if y1 > t.maxY {
			t.maxY = y1
		}
	}
	for py := t.minY; py <= t.maxY; py++ {
		row := int(py) * t.width
		for px := t.minX; px <= t.maxX; px++ {
			i := row + int(px)
			t.height[i], t.color[i], t.covered[i] = 0, 0, false
			if t.winner != nil {
				t.winner[i] = -1
			}
		}
	}
	return t
}

type modelPrimitiveMode uint8

const (
	modelPrimitiveSkip modelPrimitiveMode = iota
	modelPrimitiveFlat
	modelPrimitiveTexture
)

// modelPrimitiveDispatch reproduces the retail per-primitive dispatch: bit 0
// of the authored IsColored field selects the flat polygon filler, which takes
// an explicit vertex count and draws any arity, and only when that bit is
// clear does the renderer require a quad before it binds a texture
// [R-REN-03A §5][fmt 3do].
//
// Bit 0 is also what makes editor garbage in IsColored harmless: across the
// 608 stock models it is set on exactly the 6,598 primitives with no texture
// name and clear on exactly the 43,845 with one, so the garbage values that
// co-occur with a texture are all even [R-REN-03A §5]. An untextured face with
// the bit clear is the format's "clear" primitive — it reaches the quad mapper
// with no texture bound and the mapper draws nothing [fmt 3do].
func modelPrimitiveDispatch(pr presentationrender.PrimitiveDraw, resolved bool) modelPrimitiveMode {
	if pr.IsColored&1 != 0 {
		return modelPrimitiveFlat // flat filler, any vertex count [R-REN-03A §5]
	}
	if len(pr.VertexIndices) != 4 {
		return modelPrimitiveSkip // textured faces are quads only [R-REN-03A §5]
	}
	if pr.TextureName == "" {
		// The format's "clear" primitive: it reaches the quad mapper with no
		// texture bound, and the mapper's null guard draws nothing [fmt 3do].
		return modelPrimitiveSkip
	}
	if !resolved {
		// A texture miss is rewritten to the gray placeholder at load; it is
		// already a quad by the test above [03 §2.4.1].
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
	// Retail walks the piece list last-to-first. Because the height-key test
	// admits equal keys, draw order is the tie-break, so this direction is what
	// makes piece 0 win every tie against every later piece — reversing it
	// lifts a factory's build plate through its roof and lets an opened solar
	// collector's panels swallow the column they intersect [R-REN-03A §3].
	for pi := len(draw.Pieces) - 1; pi >= 0; pi-- {
		piece := draw.Pieces[pi]
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
					texFrame = c.modelAnimatedFrameAt(ref, kind, id, pi, pri)
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
			// modelPrimitiveDispatch has already rejected any textured face
			// that is not a quad: retail's quad mapper is hard-wired to four
			// corners and there is no textured n-gon path [R-REN-03A §5].

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
				tri.useSHD = pr.ShadeRow != presentationrender.NoShadeRow
				tri.candidate, tri.piece, tri.primitive, tri.texture = uint32(len(tris)), pi, pri, pr.TextureName
				if texFrame != nil && c.rendererTraceSink != nil {
					tri.frameState = RendererValueAvailable
					if ref.kind == texStatic {
						tri.frameIndex = 0
					} else if ref.entry != nil {
						for fi := range ref.entry.Frames {
							if ref.entry.Frames[fi].Frame == texFrame {
								tri.frameIndex = fi
								break
							}
						}
					}
				}
				for corner, vi := range indices {
					v := piece.WorldVertices[vi]
					sx, sy := c.cam.WorldToScreen(v[0], v[1], v[2])
					tri.x[corner], tri.y[corner] = sx-camera.OriginX, sy-camera.OriginY
					tri.depth += tri.y[corner]
					// Form the key from the transformed vertex's model-relative
					// whole-world-unit Y. A right shift would floor negative values;
					// retail narrowing truncates toward zero [I3][03 §5.2].
					tri.key[corner] = float64(modelHeightKey(v[1].Sub(draw.WorldPos[1])))
					if tri.useSHD {
						primitiveCorner := [3]int{0, k, k + 1}[corner]
						if primitiveCorner < len(pr.ShadeRows) {
							tri.row[corner] = float64(pr.ShadeRows[primitiveCorner])
						} else {
							tri.row[corner] = 15
						}
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
// the resulting triangles [03 §2.4][03 §2.4.1]. A non-nil reveal composes the
// unit exactly as a finished one and then recolours it band by band and
// overdraws its polygon outlines, which is the nanoframe [03 §5.2].
func (c *Client) drawModel(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) bool {
	// TODO(question): model shadows remain suppressed because the published
	// frame has no visual-options word, terrain ground/depth input, or model
	// shadow flag needed by the established pre-body shadow path [03 §5.3].
	tris := c.collectDrawTris(draw, owner, id, kind)
	// The indexed body and byte height plane are one per-unit render target.
	// Completed and unfinished models both use this target; nanoframe reveal
	// only adds a recolour/read step after ordinary admission [03 §5.2].
	target := c.reusableModelTarget(tris)
	if target.trace != nil {
		target.trace.unit = id
	}
	for i := range tris {
		switch {
		case reveal != nil && tris[i].frame != nil:
			c.blitTexturedTriNanoframeTarget(target, &tris[i], tris[i].frame, *reveal, id)
		case reveal != nil:
			c.fillTriNanoframeTarget(target, &tris[i], tris[i].color, *reveal, id)
		case tris[i].frame != nil:
			c.blitTexturedTriTarget(target, &tris[i], tris[i].frame, id)
		default:
			c.fillTriTarget(target, &tris[i], tris[i].color, id)
		}
	}
	target.commit(c.indexed)
	if reveal != nil {
		c.drawModelOutline(draw, outline, target.trace)
	}
	if target.trace != nil {
		target.trace.resolve(target, c.indexed, c.width, c.height)
		target.trace.emit(c.rendererTraceSink, c.rendererTraceFilter)
	}
	return len(tris) != 0
}

// drawModelOutline overdraws every primitive of every visible piece as a
// closed polyline. The selection plate is the one primitive the outline pass
// skips, exactly as the raster pass does [03 §5.2][03 §2.4.1].
func (c *Client) drawModelOutline(draw *presentationrender.UnitDraw, color uint8, trace *rendererTrace) {
	if c == nil || c.cam == nil || draw == nil || draw.Model == nil {
		return
	}
	for pi, piece := range draw.Pieces {
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		for pri, pr := range piece.Primitives {
			if draw.Model.Pieces[pi].Selection && pri == 0 {
				continue
			}
			n := len(pr.VertexIndices)
			if n < 2 {
				continue
			}
			var px, py int32
			for k := 0; k <= n; k++ {
				vi := int(pr.VertexIndices[k%n])
				if vi >= len(piece.WorldVertices) {
					break
				}
				v := piece.WorldVertices[vi]
				sx, sy := c.cam.WorldToScreen(v[0], v[1], v[2])
				sx, sy = sx-camera.OriginX, sy-camera.OriginY
				if k > 0 && !c.segmentOffscreen(px, py, sx, sy) {
					c.drawIndexedLine(px, py, sx, sy, color)
					if trace != nil {
						traceOutlineLine(trace, px, py, sx, sy, color, pi, pri)
					}
				}
				px, py = sx, sy
			}
		}
	}
}

// segmentOffscreen trivially rejects a wireframe edge that cannot cross the
// framebuffer, so an off-screen nanoframe costs no Bresenham steps.
func (c *Client) segmentOffscreen(x0, y0, x1, y1 int32) bool {
	w, h := int32(c.width), int32(c.height)
	return (x0 < 0 && x1 < 0) || (y0 < 0 && y1 < 0) || (x0 >= w && x1 >= w) || (y0 >= h && y1 >= h)
}

// unitNanoframeReveal builds the reveal for an unfinished unit, or nil when
// the unit is complete. The pulse phase is offset by the unit's own
// identifier so neighbouring nanoframes do not pulse together [03 §5.2].
func (c *Client) unitNanoframeReveal(v frame.UnitView) (*presentationrender.NanoframeReveal, uint8) {
	if v.BuildRemaining <= 0 {
		return nil, 0
	}
	// TODO(question): retail keys the pulse off the unit's own sixteen-bit
	// identifier; Nanolathe uses the pool slot index, which is the equivalent
	// stable per-unit number in this build.
	band, outline := presentationrender.NanoframePulse(uint16(v.Slot), c.frameTick)
	reveal := presentationrender.BuildNanoframeReveal(v.BuildRemaining, band, outline)
	return &reveal, outline
}

// drawUnitModel uses the concrete hierarchy traversal for every unit kind.
func (c *Client) drawUnitModel(v frame.UnitView, sx, sy int32) bool {
	m := c.unitModelFor(v.Model)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	states := c.modelStates(m, v.Pieces)
	draw := presentationrender.BuildUnitDraw(m.compiled, states, v.Heading, v.Pitch, v.Bank, v, c.orientationCache(unitPresentationID(v)))
	reveal, outline := c.unitNanoframeReveal(v)
	return c.drawModel(draw, v.Owner, unitPresentationID(v), modelCursorUnit, reveal, outline)
}

// drawFeatureModel and drawProjectileModel share the same concrete traversal.
func (c *Client) drawFeatureModel(f frame.FeatureView) bool {
	m := c.unitModelFor(f.Model)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	draw := presentationrender.BuildUnitDrawSimple(m.compiled, nil, 0, 0, 0, [3]numeric.Fixed{f.X, f.Y, f.Z})
	// The nanoframe reveal is a construction-fraction contract; a sinking
	// feature is not an unfinished unit and takes the ordinary model path.
	return c.drawModel(draw, 0, featurePresentationID(f), modelCursorFeature, nil, 0)
}

func (c *Client) drawProjectileModel(p frame.ProjectileView) bool {
	m := c.unitModelFor(p.Model)
	if m == nil || m.compiled == nil || c.cam == nil {
		return false
	}
	draw := presentationrender.BuildProjectileDraw(m.compiled, nil, p.Yaw, p.Pitch, [3]numeric.Fixed{p.X, p.Y, p.Z})
	return c.drawModel(draw, 0, projectilePresentationID(p), modelCursorProjectile, nil, 0)
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
// [R-REN-03A §2 "Correction to the key formula"].
//
// TODO(R-REN-03A §8): a definition authoring the FBI Digger key adds a further
// +75 here and then erases the image wherever the key is at or below 125. The
// erase pass is not implemented, and adding the offset alone would only shift
// every key uniformly, so both are deferred together. Three stock units are
// affected: ARMAMB, CORTOAST, CORVIPE.
func modelHeightKey(relativeY numeric.Fixed) int32 {
	whole := int32(relativeY.Raw() / (1 << 16)) // __ftol-style truncation [I3]
	return whole + presentationrender.NanoframeHeightBias
}

// scanlineHeightKey performs the model rasterizer's fixed-point height
// progression. Each edge intersection is represented as 16.16 X and key;
// the key is then stepped from the left intersection across the scanline,
// with every division truncating toward zero before the final byte narrowing
// [I3][03 §5.2]. It is intentionally separate from barycentric color/UV
// interpolation, which follows their own established paths.
func scanlineHeightKey(t *screenTri, px, py int32) uint8 {
	type endpoint struct{ x, key int64 }
	var hits [3]endpoint
	n := 0
	for i := 0; i < 3; i++ {
		j := (i + 1) % 3
		x0, y0 := int64(t.x[i]), int64(t.y[i])
		x1, y1 := int64(t.x[j]), int64(t.y[j])
		if int64(py) < minI64(y0, y1) || int64(py) > maxI64(y0, y1) {
			continue
		}
		dy := y1 - y0
		dx := x1 - x0
		ky0, ky1 := int64(t.key[i]), int64(t.key[j])
		if dy == 0 {
			hits[n] = endpoint{x: x0 << 16, key: ky0 << 16}
		} else {
			relY := int64(py) - y0
			hits[n] = endpoint{
				x:   (x0 << 16) + ((dx << 16) * relY / dy),
				key: (ky0 << 16) + (((ky1 - ky0) << 16) * relY / dy),
			}
		}
		n++
	}
	if n == 0 {
		return 0
	}
	left, right := hits[0], hits[0]
	for i := 1; i < n; i++ {
		if hits[i].x < left.x {
			left = hits[i]
		}
		if hits[i].x > right.x {
			right = hits[i]
		}
	}
	key := left.key
	if span := right.x - left.x; span != 0 {
		key += ((int64(px)<<16 - left.x) * (right.key - left.key)) / span
	}
	return uint8(key / (1 << 16))
}

func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// nanoframeVerdict resolves one composed nanoframe pixel. It returns the
// palette index to write and whether the pixel is drawn at all [03 §5.2].
func nanoframeVerdict(rev presentationrender.NanoframeReveal, key uint8, composed uint8) (uint8, bool) {
	switch v := rev.Verdict(key); v {
	case presentationrender.NanoframeErase:
		// Erase removes the composed scratch pixel. The target caller clears
		// coverage; it never writes palette zero into the world destination
		// [03 §5.2].
		return 0, false
	case presentationrender.NanoframeKeep:
		return composed, true
	default:
		return uint8(v), true
	}
}

func (c *Client) fillTriNanoframeTarget(target *modelTarget, t *screenTri, color uint8, rev presentationrender.NanoframeReveal, ids ...uint64) {
	id := rendererID(ids)
	minX, minY, maxX, maxY := c.triBounds(t)
	d := baryDenom(t)
	if d == 0 {
		return
	}
	for py := minY; py <= maxY; py++ {
		row := py * int32(c.width)
		for px := minX; px <= maxX; px++ {
			l0, l1, l2 := bary(t, d, px, py)
			if l0 < 0 || l1 < 0 || l2 < 0 {
				continue
			}
			key := scanlineHeightKey(t, px, py)
			idx := int(row + px)
			event := -1
			if target.trace != nil {
				event = target.traceCandidate(rendererCandidate(t, target.tick, id, px, py, key, target.height[idx], color, 0, rendererTexture(t, 0, 0, 0, RendererValueUnavailable, false)))
			}
			if !target.admit(idx, key) {
				target.traceRejected(event, RendererReasonHeightRejected, "height")
				continue
			}
			target.traceAdmitted(idx, event)
			if b, ok := nanoframeVerdict(rev, key, color); ok {
				if event >= 0 {
					target.trace.events[event].CandidateIndex = b
				}
				target.write(idx, b, true)
			} else {
				target.traceRejected(event, RendererReasonNanoframeErase, "nanoframe-erase")
				target.write(idx, 0, false)
			}
		}
	}
}

func (c *Client) blitTexturedTriNanoframeTarget(target *modelTarget, t *screenTri, frame *formats.GAFFrame, rev presentationrender.NanoframeReveal, ids ...uint64) {
	id := rendererID(ids)
	minX, minY, maxX, maxY := c.triBounds(t)
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
			tx, ty := texelAt(t, l0, l1, l2, w, h)
			b, ok := frame.At(tx, ty)
			key := scanlineHeightKey(t, px, py)
			idx := int(row + px)
			sourceState := RendererValueUnavailable
			if tx >= 0 && ty >= 0 && tx < w && ty < h && ty*w+tx < len(frame.Pixels) {
				sourceState = RendererValueAvailable
				b = frame.Pixels[ty*w+tx]
			}
			event := -1
			if target.trace != nil {
				shade := presentationrender.NoShadeRow
				if t.useSHD {
					shade = shadeRowAt(t, l0, l1, l2)
				}
				event = target.traceCandidate(rendererCandidate(t, target.tick, id, px, py, key, target.height[idx], b, shade, rendererTexture(t, tx, ty, b, sourceState, !ok)))
			}
			if !ok {
				target.traceRejected(event, RendererReasonTransparentTexel, "transparent-texel")
				continue
			}
			if !target.admit(idx, key) {
				target.traceRejected(event, RendererReasonHeightRejected, "height")
				continue
			}
			target.traceAdmitted(idx, event)
			if c.pal != nil && t.useSHD {
				b = c.pal.Shade[shadeRowAt(t, l0, l1, l2)][b]
			}
			if b, ok := nanoframeVerdict(rev, key, b); ok {
				if event >= 0 {
					target.trace.events[event].CandidateIndex = b
				}
				target.write(idx, b, true)
			} else {
				target.traceRejected(event, RendererReasonNanoframeErase, "nanoframe-erase")
				target.write(idx, 0, false)
			}
		}
	}
}

// texelAt is the shared affine texture sample used by both raster variants.
func texelAt(t *screenTri, l0, l1, l2 float64, w, h int) (int, int) {
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
	return tx, ty
}

// shadeRowAt is the shared clamped SHD row lookup [03 §2.4.1].
func shadeRowAt(t *screenTri, l0, l1, l2 float64) int {
	ri := int(l0*t.row[0] + l1*t.row[1] + l2*t.row[2])
	if ri < 0 {
		return 0
	}
	if ri > 31 {
		return 31
	}
	return ri
}

func (c *Client) fillTriTarget(target *modelTarget, t *screenTri, color uint8, ids ...uint64) {
	id := rendererID(ids)
	minX, minY, maxX, maxY := c.triBounds(t)
	for py := minY; py <= maxY; py++ {
		row := py * int32(c.width)
		for px := minX; px <= maxX; px++ {
			idx := int(row + px)
			if pointInTri(t, px, py) {
				key := scanlineHeightKey(t, px, py)
				event := -1
				if target.trace != nil {
					event = target.traceCandidate(rendererCandidate(t, target.tick, id, px, py, key, target.height[idx], color, 0, rendererTexture(t, 0, 0, 0, RendererValueUnavailable, false)))
				}
				if target.admit(idx, key) {
					target.traceAdmitted(idx, event)
					target.write(idx, color, true)
				} else if event >= 0 {
					target.traceRejected(event, RendererReasonHeightRejected, "height")
				}
			}
		}
	}
}

func (c *Client) blitTexturedTriTarget(target *modelTarget, t *screenTri, frame *formats.GAFFrame, ids ...uint64) {
	id := rendererID(ids)
	minX, minY, maxX, maxY := c.triBounds(t)
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
			tx, ty := texelAt(t, l0, l1, l2, w, h)
			b, ok := frame.At(tx, ty)
			key := scanlineHeightKey(t, px, py)
			idx := int(row + px)
			sourceState := RendererValueUnavailable
			if tx >= 0 && ty >= 0 && tx < w && ty < h && ty*w+tx < len(frame.Pixels) {
				sourceState = RendererValueAvailable
				b = frame.Pixels[ty*w+tx]
			}
			shade := presentationrender.NoShadeRow
			if t.useSHD {
				shade = shadeRowAt(t, l0, l1, l2)
			}
			event := -1
			if target.trace != nil {
				event = target.traceCandidate(rendererCandidate(t, target.tick, id, px, py, key, target.height[idx], b, shade, rendererTexture(t, tx, ty, b, sourceState, !ok)))
			}
			if !ok {
				target.traceRejected(event, RendererReasonTransparentTexel, "transparent-texel")
				continue
			}
			if !target.admit(idx, key) {
				target.traceRejected(event, RendererReasonHeightRejected, "height")
				continue
			}
			target.traceAdmitted(idx, event)
			if c.pal != nil && t.useSHD {
				b = c.pal.Shade[shade][b]
			}
			if event >= 0 {
				target.trace.events[event].CandidateIndex = b
			}
			target.write(idx, b, true)
		}
	}
}

// triBounds clips a triangle's screen bounding box to the framebuffer.
func (c *Client) triBounds(t *screenTri) (minX, minY, maxX, maxY int32) {
	minX, minY, maxX, maxY = t.x[0], t.y[0], t.x[0], t.y[0]
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
	return
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
			if pal != nil && t.useSHD {
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

// drawUnitChrome overlays the selected/damaged health bar for a model-drawn
// unit. Retail does not emit a generic per-unit footprint bracket in this
// post-fog stage [R-SEL-02A]. Geometry follows the footprint box [04 §6.2];
// the model itself may extend above it.
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
	if v.MaxHealth > 0 && (v.Health != v.MaxHealth || v.Flags&hud.SelectionFlag != 0) {
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
