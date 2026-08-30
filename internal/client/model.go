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
	x, y [3]int32
	// oddHeight records the low bit of the corner's model-relative whole-unit
	// height. The supersampled shear is (2*z) - y rather than 2*(z - (y>>1)),
	// which is one pixel lower exactly when that bit is set [R-REN-03A §6].
	oddHeight  [3]bool
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

// modelTarget is the retail per-unit composition image [R-REN-03A §1]. It is
// sized to the model's own projected extent plus a two-pixel margin, not to
// the framebuffer, and it records where the model's own (0,0) sits inside it
// so the finished image can be blitted at the unit's screen anchor.
//
// The colour plane is prefilled with transparentIndex and the final blit skips
// that index, so uncovered pixels leave terrain and earlier world passes
// untouched. The height plane is the per-pixel depth buffer of
// [R-REN-03A §2]; it is nil when the unit's definition authors no ZBuffer,
// and every writer then falls through to unconditional painter-order writes,
// exactly as retail's span writers do with a null key plane.
type modelTarget struct {
	color, height    []uint8
	covered          []bool
	width, heightPx  int   // image dimensions
	originX, originY int32 // image pixel holding the model's own (0,0)
	anchorX, anchorY int32 // framebuffer pixel holding the model's own (0,0)
	transparent      uint8
	// scale is 1, or 2 while the structure anti-alias supersample is active
	// [R-REN-03A §6]. It is descriptive: the caller has already multiplied the
	// dimensions and origin.
	scale  int32
	trace  *rendererTrace
	winner []int
	tick   uint32
}

// transparentModelIndex is the composition image's background. Retail prefills
// the colour plane with it and records it in the image header as the index the
// final blit skips; it is also what the anti-alias downscale blends against at
// the silhouette, which is where the red/purple fringe comes from
// [R-REN-03A §1][R-REN-03A §7].
const transparentModelIndex uint8 = 1

// modelTargetMargin is retail's two-pixel border on every side of the measured
// extent [R-REN-03A §1].
const modelTargetMargin int32 = 2

func newModelTarget(width, height int) *modelTarget {
	return newModelImage(width, height, 0, 0, 0, 0, true, 1)
}

// newModelImage allocates a composition image. keyPlane follows the unit's
// ZBuffer authoring [R-REN-03A §2].
func newModelImage(width, height int, originX, originY, anchorX, anchorY int32, keyPlane bool, scale int32) *modelTarget {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	n := width * height
	t := &modelTarget{
		color: make([]uint8, n), covered: make([]bool, n),
		width: width, heightPx: height,
		originX: originX, originY: originY,
		anchorX: anchorX, anchorY: anchorY,
		transparent: transparentModelIndex,
		scale:       scale,
	}
	if keyPlane {
		t.height = make([]uint8, n)
	}
	for i := range t.color {
		t.color[i] = t.transparent
	}
	return t
}

// screenX and screenY map an image pixel back to the framebuffer. The image
// carries the model's (0,0) at (originX, originY) and that point lands on the
// framebuffer at (anchorX, anchorY) [R-REN-03A §1].
func (t *modelTarget) screenX(ix int32) int32 { return t.anchorX + ix - t.originX }
func (t *modelTarget) screenY(iy int32) int32 { return t.anchorY + iy - t.originY }

// imageX and imageY are the inverse, used to record framebuffer-space
// diagnostics against the image the model was rasterized into.
func (t *modelTarget) imageX(sx int32) int32 { return sx - t.anchorX + t.originX }
func (t *modelTarget) imageY(sy int32) int32 { return sy - t.anchorY + t.originY }

// admit applies the height-key test. With no key plane every candidate is
// admitted and composition is pure painter order [R-REN-03A §2].
func (t *modelTarget) admit(idx int, key uint8) bool {
	if t.height == nil {
		return true
	}
	if t.height[idx] > key {
		return false
	}
	t.height[idx] = key
	return true
}

func (t *modelTarget) write(idx int, color uint8, present bool) {
	if !present {
		// Nanoframe erase writes the image background, which the final blit
		// skips [03 §5.2][R-REN-03A §1].
		t.color[idx], t.covered[idx] = t.transparent, false
		return
	}
	t.color[idx], t.covered[idx] = color, true
}

// bounds clips a triangle's bounding box to the image.
func (t *modelTarget) bounds(tri *screenTri) (minX, minY, maxX, maxY int32) {
	minX, minY, maxX, maxY = tri.x[0], tri.y[0], tri.x[0], tri.y[0]
	for k := 1; k < 3; k++ {
		if tri.x[k] < minX {
			minX = tri.x[k]
		}
		if tri.x[k] > maxX {
			maxX = tri.x[k]
		}
		if tri.y[k] < minY {
			minY = tri.y[k]
		}
		if tri.y[k] > maxY {
			maxY = tri.y[k]
		}
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX > int32(t.width)-1 {
		maxX = int32(t.width) - 1
	}
	if maxY > int32(t.heightPx)-1 {
		maxY = int32(t.heightPx) - 1
	}
	return
}

// resolveSupersample is retail's 2:1 resolve of an anti-aliased structure
// image [R-REN-03A §6]. The colour plane goes through three ALP lookups per
// output pixel — the two horizontal pairs first, then the two results — and
// the key plane is nearest-sampled from the top-left of each block with no
// blending at all.
//
// The filter does not exclude the image background from the blend, and that is
// deliberate: it is the retail defect that puts a one-pixel border of reds and
// dusty purples around every anti-aliased building. ALP's diagonal is exact
// identity, so a block wholly outside the model still resolves to the
// background index and stays transparent [R-REN-03A §7].
func (t *modelTarget) resolveSupersample(dst *modelTarget, alp *[65536]byte) {
	if t == nil || dst == nil || alp == nil {
		return
	}
	for y := 0; y < dst.heightPx; y++ {
		srcTop, srcBottom := 2*y*t.width, (2*y+1)*t.width
		if srcBottom+t.width > len(t.color) {
			break
		}
		for x := 0; x < dst.width; x++ {
			if 2*x+1 >= t.width {
				break
			}
			top := alp[int(t.color[srcTop+2*x])*256+int(t.color[srcTop+2*x+1])]
			bottom := alp[int(t.color[srcBottom+2*x])*256+int(t.color[srcBottom+2*x+1])]
			out := alp[int(top)*256+int(bottom)]
			i := y*dst.width + x
			dst.color[i] = out
			dst.covered[i] = out != dst.transparent
			if dst.height != nil && t.height != nil {
				dst.height[i] = t.height[srcTop+2*x]
			}
		}
	}
}

// commit blits the finished image into the framebuffer, skipping the
// background index [R-REN-03A §1].
func (t *modelTarget) commit(dst []uint8, width, height int) {
	if t == nil || width <= 0 || height <= 0 {
		return
	}
	for iy := 0; iy < t.heightPx; iy++ {
		sy := t.screenY(int32(iy))
		if sy < 0 || sy >= int32(height) {
			continue
		}
		row := int(sy) * width
		src := iy * t.width
		for ix := 0; ix < t.width; ix++ {
			i := src + ix
			if !t.covered[i] {
				continue
			}
			sx := t.screenX(int32(ix))
			if sx < 0 || sx >= int32(width) {
				continue
			}
			dst[row+int(sx)] = t.color[i]
		}
	}
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
					// Retail composes model-relative: the piece chain result is
					// narrowed once, the shear applied, and the unit's position
					// enters only at the final blit [03 §5.2][R-REN-03A §1].
					lx, ly, ry := modelLocalVertex(v, draw.WorldPos)
					lx, ly = c.scaleModelLocal(lx, ly)
					tri.x[corner], tri.y[corner] = lx, ly
					tri.oddHeight[corner] = ry&1 != 0
					tri.depth += ly
					key := ry + presentationrender.NanoframeHeightBias
					if draw.DiggerClip {
						key += diggerKeyBias
					}
					tri.key[corner] = float64(key)
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

// modelExtent measures the composition image from the collected triangles:
// retail walks every visible piece's vertices, tracks the min and max of the
// projected offsets with the extrema seeded at zero so the box always contains
// the model origin, then adds a two-pixel margin on every side
// [R-REN-03A §1].
func modelExtent(tris []screenTri) (width, height int, originX, originY int32) {
	var minX, minY, maxX, maxY int32 // seeded at the model origin, not at a vertex
	for i := range tris {
		for k := 0; k < 3; k++ {
			if tris[i].x[k] < minX {
				minX = tris[i].x[k]
			}
			if tris[i].x[k] > maxX {
				maxX = tris[i].x[k]
			}
			if tris[i].y[k] < minY {
				minY = tris[i].y[k]
			}
			if tris[i].y[k] > maxY {
				maxY = tris[i].y[k]
			}
		}
	}
	originX = modelTargetMargin - minX
	originY = modelTargetMargin - minY
	return int(maxX - minX + 2*modelTargetMargin), int(maxY - minY + 2*modelTargetMargin), originX, originY
}

// placeTris rewrites the collected model-relative triangles into image-local
// coordinates. At scale 2 the supersampled shear is (2*z) - y, which is one
// pixel above twice the plain shear exactly when the corner's whole-unit
// height is odd, so reproduce that term rather than doubling the 1x result
// [R-REN-03A §6].
func placeTris(tris []screenTri, originX, originY, scale int32) {
	for i := range tris {
		for k := 0; k < 3; k++ {
			if scale == 2 {
				// The doubled image's origin is 2*origin, so the placed
				// coordinate is 2*(local + origin); the Y term then loses one
				// pixel for an odd height because retail's supersampled shear
				// is (2*z) - y rather than 2*(z - (y>>1)) [R-REN-03A §6].
				tris[i].x[k] = 2 * (tris[i].x[k] + originX)
				tris[i].y[k] = 2*(tris[i].y[k]+originY) - boolToInt32(tris[i].oddHeight[k])
				continue
			}
			tris[i].x[k] += originX
			tris[i].y[k] += originY
		}
	}
}

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// supersampleModel reports whether this subject takes retail's structure
// anti-aliasing: the Anti_Alias display option on, and the unit's authored
// class bit saying structure. Mobile units are rasterized at 1x whatever the
// option says, which is why buildings read smoother than units in retail
// [R-REN-03A §6].
func (c *Client) supersampleModel(structure bool) bool {
	return c != nil && c.antiAlias && structure && c.pal != nil
}

// drawModel is the sole indexed model raster entry. Traversal is performed by
// internal/render and this function only resolves authored textures, composes
// the unit into its own image, and blits that image once [03 §2.4][03 §2.4.1]
// [R-REN-03A §1]. A non-nil reveal composes the unit exactly as a finished one
// and then recolours it band by band and overdraws its polygon outlines, which
// is the nanoframe [03 §5.2].
func (c *Client) drawModel(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) bool {
	tris := c.collectDrawTris(draw, owner, id, kind)
	if len(tris) == 0 {
		return false
	}
	anchorX, anchorY := c.modelAnchor(draw)
	width, height, originX, originY := modelExtent(tris)

	scale := int32(1)
	if c.supersampleModel(draw.Structure) {
		scale = 2
	}
	placeTris(tris, originX, originY, scale)

	// The image the unit is blitted from is always 1x; when anti-aliasing is
	// active the model is rasterized into a doubled scratch first and resolved
	// into it [R-REN-03A §6].
	keyPlane := draw.KeyPlane
	target := newModelImage(width, height, originX, originY, anchorX, anchorY, keyPlane, 1)
	raster := target
	if scale == 2 {
		raster = newModelImage(2*width, 2*height, 2*originX, 2*originY, anchorX, anchorY, keyPlane, 2)
	}
	c.attachModelTrace(raster, id)

	for i := range tris {
		switch {
		case reveal != nil && tris[i].frame != nil:
			c.blitTexturedTriNanoframeTarget(raster, &tris[i], tris[i].frame, *reveal, id)
		case reveal != nil:
			c.fillTriNanoframeTarget(raster, &tris[i], tris[i].color, *reveal, id)
		case tris[i].frame != nil:
			c.blitTexturedTriTarget(raster, &tris[i], tris[i].frame, id)
		default:
			c.fillTriTarget(raster, &tris[i], tris[i].color, id)
		}
	}
	if scale == 2 {
		raster.resolveSupersample(target, &c.pal.Alpha)
	}
	if draw.DiggerClip {
		// A Digger definition raises every key by 75; erasing at or below 125
		// therefore removes exactly the geometry at or below the model origin,
		// which is the buried half of a pop-up defence [R-REN-03A §8].
		target.eraseAtOrBelow(uint8(diggerKeyBias + presentationrender.NanoframeHeightBias))
	}
	// The shadow is composed and blitted before the body for the same subject
	// [03 §5.3]. It reads the finished body image to punch the body's own
	// silhouette out of itself [R-REN-03D §5][R-RAST-01 §4], which is why it
	// runs after the raster and the anti-alias resolve rather than first.
	c.drawModelShadow(draw, target)
	target.commit(c.indexed, c.width, c.height)
	if reveal != nil {
		c.drawModelOutline(draw, outline, raster)
	}
	if raster.trace != nil {
		raster.trace.resolve(raster, c.indexed, c.width, c.height)
		raster.trace.emit(c.rendererTraceSink, c.rendererTraceFilter)
	}
	return true
}

// modelAnchor is the framebuffer pixel the composition image's recorded origin
// lands on. Position enters the model path here and nowhere else
// [03 §5.2][R-REN-03A §1].
func (c *Client) modelAnchor(draw *presentationrender.UnitDraw) (int32, int32) {
	if c == nil || c.cam == nil || draw == nil {
		return 0, 0
	}
	sx, sy := c.cam.WorldToScreen(draw.WorldPos[0], draw.WorldPos[1], draw.WorldPos[2])
	return sx - camera.OriginX, sy - camera.OriginY
}

// attachModelTrace wires the parity trace to the image actually rasterized
// into, which is the doubled scratch while anti-aliasing.
func (c *Client) attachModelTrace(t *modelTarget, id uint64) {
	if c == nil || c.rendererTraceSink == nil || t == nil {
		return
	}
	t.trace = newRendererTrace(t.width * t.heightPx)
	t.winner = t.trace.winner
	t.trace.unit = id
	t.trace.tick = c.frameTick
	t.trace.width = t.width
	t.trace.height = t.heightPx
	t.tick = c.frameTick
}

// drawModelOutline overdraws every primitive of every visible piece as a
// closed polyline. The selection plate is the one primitive the outline pass
// skips, exactly as the raster pass does [03 §5.2][03 §2.4.1].
// The trace target, when present, is the image the model was rasterized into,
// so outline points are converted back into its coordinate space before they
// are recorded; the visible line itself is drawn in framebuffer space.
//
// The outline projects each vertex through exactly the body's own path:
// [R-COMP-01 §3] gives the outline vertex as `sx = hi16(x) + originX`,
// `sy = hi16(-z) - (hi16(y) >> 1) + originY` — the composition image's own
// projection and origin pair — and the image is then blitted at the unit
// anchor, so a screen point is modelLocalVertex plus modelAnchor.
//
// This previously passed the composed vertices straight to the camera's
// world-space projection. Those vertices are the model-space piece chain with
// the unit position added componentwise ([03 §2.4]), not world coordinates:
// the camera adds screen Y from +Z, so the wireframe came out mirrored against
// the body it outlines by the handedness flip of [R-RAST-01 §2], and it
// composed the unit's own height into the model's half-height shear instead of
// leaving it to the anchor. A nanoframe's outline therefore drifted off its
// body and turned the wrong way as the unit turned.
func (c *Client) drawModelOutline(draw *presentationrender.UnitDraw, color uint8, target *modelTarget) {
	if c == nil || c.cam == nil || draw == nil || draw.Model == nil {
		return
	}
	var trace *rendererTrace
	if target != nil {
		trace = target.trace
	}
	anchorX, anchorY := c.modelAnchor(draw)
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
				lx, ly, _ := modelLocalVertex(piece.WorldVertices[vi], draw.WorldPos)
				lx, ly = c.scaleModelLocal(lx, ly)
				sx, sy := anchorX+lx, anchorY+ly
				if k > 0 && !c.segmentOffscreen(px, py, sx, sy) {
					c.drawIndexedLine(px, py, sx, sy, color)
					if trace != nil {
						ix0, iy0 := target.imageX(px), target.imageY(py)
						ix1, iy1 := target.imageX(sx), target.imageY(sy)
						traceOutlineLine(trace, ix0, iy0, ix1, iy1, color, pi, pri)
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
	// BMcode=0 is the structure class [R-RND-02A]; a unit under construction
	// always gets the height plane because the nanoframe reveal reads it
	// [R-REN-03A §2].
	draw.Structure = !v.BMCode
	draw.KeyPlane = v.ZBuffer || v.BuildRemaining > 0
	draw.CastsShadow = c.castsModelShadow(v.NoShadow, v.CanHover, v.Floater)
	draw.GroundY = c.groundHeightUnder(v.X, v.Z)
	draw.DiggerClip = v.Digger
	if v.Digger {
		// The Digger key bias is applied to every vertex, so the clip
		// threshold and the composed keys stay on one scale [R-REN-03A §8].
		draw.KeyPlane = true
	}
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
	// The feature-backed pseudo-unit has no FBI to read and sets both the
	// structure class bit and the height-plane bit unconditionally at
	// construction, so 3DO wrecks anti-alias like buildings [R-REN-03A §2].
	draw.Structure, draw.KeyPlane = true, true
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
	// A projectile is not a unit instance and carries no class or ZBuffer
	// bit. It composes with the height plane so its own pieces resolve, and
	// without the structure supersample.
	// TODO(question): whether retail's projectile model records allocate a key
	// plane, and whether they can reach the anti-alias gate at all.
	draw.KeyPlane = true
	return c.drawModel(draw, 0, projectilePresentationID(p), modelCursorProjectile, nil, 0)
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

// scaleModelLocal applies presentation zoom to a model-relative offset. Retail
// has no zoom; at the retail scale of 1 this is the identity and the offsets
// stay exactly as [R-REN-03A §1] computes them.
func (c *Client) scaleModelLocal(lx, ly int32) (int32, int32) {
	if c == nil || c.cam == nil {
		return lx, ly
	}
	s := c.cam.EffectiveScale()
	if s == 1 {
		return lx, ly
	}
	return int32(float32(lx) * s), int32(float32(ly) * s)
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
// The narrowing floors rather than truncating toward zero: retail extracts the
// high word of the model-relative 16.16 value with an arithmetic shift, not
// through __ftol, so I3's truncate-toward-zero rule does not apply here
// [R-REN-03A §2].
//
// TODO(R-REN-03A §8): a definition authoring the FBI Digger key adds a further
// +75 here and then erases the image wherever the key is at or below 125. The
// erase pass is not implemented, and adding the offset alone would only shift
// every key uniformly, so both are deferred together. Three stock units are
// affected: ARMAMB, CORTOAST, CORVIPE.
func modelHeightKey(relativeY numeric.Fixed) int32 {
	return int32(relativeY.Floor()) + presentationrender.NanoframeHeightBias
}

// diggerKeyBias is the extra height-key offset a definition authoring the FBI
// Digger key carries. It exists so the clip threshold of [R-REN-03A §8] lands
// exactly at the model origin. Three stock units author it: ARMAMB, CORTOAST,
// CORVIPE.
const diggerKeyBias int32 = 75

// groundHeightUnder samples the terrain height beneath a unit, which is what
// the shadow shear uses [R-REN-03D §3].
func (c *Client) groundHeightUnder(x, z numeric.Fixed) numeric.Fixed {
	if c == nil || c.terrain == nil {
		return 0
	}
	return c.terrain.HeightAt(x, z)
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
	minX, minY, maxX, maxY := target.bounds(t)
	d := baryDenom(t)
	if d == 0 {
		return
	}
	for py := minY; py <= maxY; py++ {
		row := py * int32(target.width)
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
	minX, minY, maxX, maxY := target.bounds(t)
	d := baryDenom(t)
	if d == 0 {
		return
	}
	w, h := int(frame.Width), int(frame.Height)
	for py := minY; py <= maxY; py++ {
		row := py * int32(target.width)
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
	minX, minY, maxX, maxY := target.bounds(t)
	for py := minY; py <= maxY; py++ {
		row := py * int32(target.width)
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
	minX, minY, maxX, maxY := target.bounds(t)
	d := baryDenom(t)
	if d == 0 {
		return
	}
	w, h := int(frame.Width), int(frame.Height)
	for py := minY; py <= maxY; py++ {
		row := py * int32(target.width)
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
