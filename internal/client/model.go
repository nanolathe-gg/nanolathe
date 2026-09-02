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
		// Not a retail question: FeatureView carries no published stable
		// identity for this animated sequence, so every feature would share
		// cursor zero and step in lockstep. Suppressing is the honest answer
		// until the publication boundary carries an identity.
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
// [03 §2.4 "N-gon primitives"]. A face is dropped when its projected corner
// ring runs counter-clockwise, which is what retail's two-chain edge walk does
// to a back face [R-RAST-01 §1] step 7 — see modelFacePaints. (This comment
// previously said faces draw double-sided with no backface cull, citing
// [03 §2.4.1]; that sentence predates the scan converter's closure and is
// wrong — the cull is not a separate test, it is the span comparison.)
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

// spanU..spanRow index the attributes the two-chain edge walk carries beside
// the edge X. Retail promotes each of them to signed 16.16 when the walk
// starts, steps it down the chain edges, and steps it again across the span
// [R-RAST-01 §1] steps 3-5. The SHD row rides the same interpolation as the
// key, chosen per vertex from the smoothed normal [R-RAST-01 §5].
const (
	spanU = iota
	spanV
	spanKey
	spanRow
	spanAttrs
)

// spanEdgeBias is what retail adds to a chain edge's starting 16.16 X: the
// largest 16.16 fraction, one step short of a whole unit. Followed by the
// arithmetic shift down it is a ceiling for every non-integer edge position and
// the identity for an integer one, which is the section's whole pixel-coverage
// rule [R-RAST-01 §1] steps 3 and 5. Attributes carry no such bias.
const spanEdgeBias int64 = (1 << 16) - 1

// spanEdge is one row of a chain's edge table: the pixel X the chain reaches on
// that row, and the attributes still in 16.16 [R-RAST-01 §1] step 3.
type spanEdge struct {
	x   int32
	a   [spanAttrs]int64
	set bool
}

// screenPoly is one authored primitive as retail's scan converter consumes it:
// the projected corners in **authored index order** — never re-ordered, never
// fan-triangulated — with one integer attribute lane per corner.
//
// Index order is load-bearing. The walk treats the chain of decreasing indices
// from the topmost corner as the left edge and the chain of increasing indices
// as the right edge, so the vertex order is what decides which faces paint at
// all [R-RAST-01 §1] steps 3, 4 and 7.
type screenPoly struct {
	x, y []int32
	// oddHeight records the low bit of each corner's model-relative whole-unit
	// height. The supersampled shear is (2*z) - y rather than 2*(z - (y>>1)),
	// which is one pixel lower exactly when that bit is set [R-REN-03A §6].
	oddHeight []bool
	// attr holds the per-corner lanes the walk interpolates, indexed by
	// spanU..spanRow. u and v are texel coordinates, not normalised: retail's
	// quad mapper defaults the four corners to (0,0) (w-1,0) (w-1,h-1) (0,h-1)
	// in vertex-index order [R-RAST-01 §1].
	attr [spanAttrs][]int32

	color      uint8
	frame      *formats.GAFFrame
	useSHD     bool // textured texels pass through PALETTE.SHD [R-RND-02A]
	candidate  uint32
	piece      int
	primitive  int
	texture    string
	frameIndex int
	frameState RendererValueState
}

// newScreenPoly allocates one face's corner lanes out of a single backing
// array, so a model with a few hundred primitives costs a few hundred
// allocations rather than a few thousand.
func newScreenPoly(n int) screenPoly {
	buf := make([]int32, n*(2+spanAttrs))
	p := screenPoly{
		x:         buf[0:n:n],
		y:         buf[n : 2*n : 2*n],
		oddHeight: make([]bool, n),
	}
	for k := 0; k < spanAttrs; k++ {
		lo := (2 + k) * n
		p.attr[k] = buf[lo : lo+n : lo+n]
	}
	return p
}

// traceFace adapts a face to the per-candidate trace helpers, which read only
// the face's identity fields and never its corners.
func (p *screenPoly) traceFace() *screenTri {
	return &screenTri{
		color: p.color, frame: p.frame, useSHD: p.useSHD,
		candidate: p.candidate, piece: p.piece, primitive: p.primitive,
		texture: p.texture, frameIndex: p.frameIndex, frameState: p.frameState,
	}
}

// spanByte narrows an interpolated 16.16 attribute the way the span writers'
// key test does: an arithmetic shift down, then the low byte [R-RAST-01 §1]
// step 6.
func spanByte(v int64) uint8 { return uint8((v >> 16) & 0xFF) }

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
	// scanLeft and scanRight are the two chains' edge tables [R-RAST-01 §1]
	// steps 3-4, retained across the faces of one composition so the walk
	// allocates once per image rather than once per primitive.
	scanLeft, scanRight []spanEdge
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

// storedKey reads the key plane for the trace. An image whose definition
// authors no ZBuffer has no key plane at all, and every candidate then reports
// a stored key of zero [R-REN-03A §2].
func (t *modelTarget) storedKey(idx int) uint8 {
	if t == nil || t.height == nil || idx < 0 || idx >= len(t.height) {
		return 0
	}
	return t.height[idx]
}

// polyScan is retail's two-chain scanline edge walk over one face
// [R-RAST-01 §1] steps 1-5, in the composition-image variant: the bounds are
// the image's own, L = 0, T = 0, R = width-1, B = height-1.
//
// It replaces a fan triangulation with a barycentric point-in-triangle test.
// The two are not equivalent, and the difference is the whole point of the
// section. Retail never looks at a face as a whole: from the topmost corner it
// walks decreasing indices as the *left* chain and increasing indices as the
// *right* chain, and each row is painted only where the right chain is still
// strictly right of the left one. That per-scanline comparison is simultaneously
// the back-face cull — a ring that projects counter-clockwise with screen Y
// increasing downward yields an empty span on every row (step 7) — and the rule
// for a folded projection, which paints exactly the rows above the crossing and
// nothing at or below it.
//
// span is called once per painted row with the span already clamped to the
// image and the attribute accumulator positioned at its first pixel, together
// with the per-pixel step. Left and top are inclusive, right and bottom
// exclusive: the walk never writes the image's last row or last column, which
// the two-pixel margin of [R-REN-03A §1] makes harmless.
func (t *modelTarget) polyScan(p *screenPoly, span func(row, xl, xr int32, a, da *[spanAttrs]int64)) {
	n := len(p.x)
	if n < 3 || t == nil || t.width <= 0 || t.heightPx <= 0 {
		// A two-corner flat primitive makes both chains the same single edge,
		// so xr == xl on every row and nothing is drawn [R-RAST-01 §1] step 7.
		return
	}
	const boundLeft, boundTop = int32(0), int32(0)
	boundRight, boundBottom := int32(t.width)-1, int32(t.heightPx)-1

	// Step 1. The top and bottom indices are the FIRST corners attaining their
	// extreme, under a strict comparison; a tie keeps the earlier index.
	minY, maxY, minX, maxX := p.y[0], p.y[0], p.x[0], p.x[0]
	topIdx, botIdx := 0, 0
	for i := 1; i < n; i++ {
		if p.y[i] < minY {
			minY, topIdx = p.y[i], i
		}
		if p.y[i] > maxY {
			maxY, botIdx = p.y[i], i
		}
		if p.x[i] < minX {
			minX = p.x[i]
		}
		if p.x[i] > maxX {
			maxX = p.x[i]
		}
	}
	// Step 2. Reject against the bounds, then clip the row range. The fill is
	// exclusive of yEnd, so the bottom row is never written.
	if maxX < boundLeft || minX > boundRight || maxY < boundTop || minY > boundBottom {
		return
	}
	yStart, yEnd := minY, maxY
	if yStart < boundTop {
		yStart = boundTop
	}
	if yEnd > boundBottom {
		yEnd = boundBottom
	}
	if yStart >= yEnd {
		return
	}
	rows := int(yEnd - yStart)
	if cap(t.scanLeft) < rows || cap(t.scanRight) < rows {
		t.scanLeft = make([]spanEdge, rows)
		t.scanRight = make([]spanEdge, rows)
	}
	leftTab, rightTab := t.scanLeft[:rows], t.scanRight[:rows]
	for i := 0; i < rows; i++ {
		leftTab[i].set, rightTab[i].set = false, false
	}

	// Steps 3 and 4. One walk serves both chains; only the index direction
	// differs. An edge contributes only when it descends — horizontal edges and
	// edges that go up are skipped outright — and both chains index the table
	// from yStart, so an edge clipped at T lands on the row an unclipped one
	// would have.
	walk := func(step int, tab []spanEdge) {
		cur := topIdx
		for guard := 0; guard <= n; guard++ {
			next := cur + step
			if next < 0 {
				next = n - 1
			} else if next >= n {
				next = 0
			}
			if p.y[next] > p.y[cur] {
				dy := int64(p.y[next] - p.y[cur])
				// The edge X is the only biased quantity: one fraction short of
				// a whole unit, followed by the arithmetic shift, is a ceiling
				// for a fractional position and the identity for a whole one,
				// which is what makes two faces sharing an edge neither overlap
				// nor leave a gap.
				x := int64(p.x[cur])<<16 + spanEdgeBias
				xStep := (int64(p.x[next]-p.x[cur]) << 16) / dy
				var a, aStep [spanAttrs]int64
				for k := 0; k < spanAttrs; k++ {
					a[k] = int64(p.attr[k][cur]) << 16
					aStep[k] = (int64(p.attr[k][next]-p.attr[k][cur]) << 16) / dy
				}
				row := p.y[cur]
				if row < boundTop {
					d := int64(boundTop - row)
					x += xStep * d
					for k := range a {
						a[k] += aStep[k] * d
					}
					row = boundTop
				}
				stop := p.y[next]
				if stop > boundBottom {
					stop = boundBottom
				}
				for ; row < stop; row++ {
					if i := int(row - yStart); i >= 0 && i < rows {
						tab[i].x, tab[i].a, tab[i].set = int32(x>>16), a, true
					}
					x += xStep
					for k := range a {
						a[k] += aStep[k]
					}
				}
			}
			if next == botIdx {
				return
			}
			cur = next
		}
	}
	walk(-1, leftTab)  // step 3: decreasing indices are the left chain
	walk(+1, rightTab) // step 4: increasing indices are the right chain

	// Step 5. The composition span writers run only where xr > xl. The
	// per-pixel attribute step divides by the UNCLAMPED width, and only then is
	// the span clamped into the image.
	for r := yStart; r < yEnd; r++ {
		i := int(r - yStart)
		l, rt := &leftTab[i], &rightTab[i]
		if !l.set || !rt.set {
			// TODO(question): a chain that folds back on itself leaves some
			// rows of its edge table unwritten, and [R-RAST-01 §1] does not say
			// what retail's table holds there — its buffer is not documented as
			// initialised, so the row would take stale contents from an earlier
			// face. Treating an unwritten row as an empty span is the
			// conservative reading; tracing the edge tables' allocation and any
			// per-face clear would settle it.
			continue
		}
		if rt.x <= l.x {
			continue // step 7's cull, evaluated per scanline
		}
		width := int64(rt.x - l.x)
		var a, da [spanAttrs]int64
		for k := 0; k < spanAttrs; k++ {
			a[k] = l.a[k]
			da[k] = (rt.a[k] - l.a[k]) / width
		}
		xl, xr := l.x, rt.x
		if xl < boundLeft {
			d := int64(boundLeft - xl)
			for k := range a {
				a[k] += da[k] * d
			}
			xl = boundLeft
		}
		if xr > boundRight {
			xr = boundRight
		}
		if xr <= xl {
			continue
		}
		span(r, xl, xr, &a, &da)
	}
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

// modelFacePaints applies retail's winding cull to one authored primitive
// [R-RAST-01 §1] step 7.
//
// Retail has no normal test, no signed-area test and no backface flag. What
// removes a back face is the scan converter itself: from the polygon's topmost
// corner it walks decreasing indices as the *left* edge chain and increasing
// indices as the *right* chain, and a span writer runs only where `xr > xl`.
// A convex face whose projected corners run clockwise in index order (screen Y
// increasing downward) therefore paints, and a counter-clockwise one produces
// an empty span on every row and paints nothing. The doubled signed area of
// the projected corner ring is exactly the sign of that comparison for a
// convex face, and it is positive for a clockwise ring in a Y-down frame.
//
// This is why it matters here rather than as a micro-optimisation. Authored
// 3DO order is counter-clockwise seen from outside the piece (measured: 2,029
// pieces outward against 90 inward across the 608 stock models, [fmt 3do]
// "Unknowns and caveats"), and the composition projection negates the
// model-relative Z ([R-RAST-01 §2]), a reflection that reverses winding — so
// outward faces arrive clockwise and paint while the inward faces behind them
// are dropped. Drawing both sides let an interior face that shares a plane
// with the skin win the height key's equal-key tie-break, which is drawn later
// wins ([R-REN-03A §2]): on `ARMCK` the team-coloured inner face of each
// shoulder flap is coplanar with the grey ribbed outer face and index-ordered
// after it, so the whole flap came out solid team colour.
//
// The corners are the projection the face is actually rasterized with, taken
// before the presentation zoom of scaleModelLocal, so the test is evaluated on
// exactly the quantity retail's chains compare. The shadow pass passes its own
// quarter-shear projection for the same reason.
//
// The body raster no longer needs this predicate: polyScan performs the real
// per-scanline comparison, so a folded projection paints exactly the rows where
// its right chain is still right of its left one instead of being dropped
// whole. What remains here is the convex summary of the same comparison, used
// by the two passes that do not run a span writer at all — the nanoframe
// wireframe, which draws edges rather than spans, and the shadow
// rasterization's own quarter-shear pass.
func modelFacePaints(vertices [][3]numeric.Fixed, indices []uint16, origin [3]numeric.Fixed) bool {
	return facePaints(vertices, indices, origin, modelLocalVertex)
}

// facePaints is modelFacePaints over an arbitrary vertex projection, so the
// body and shadow passes share one cull evaluated on their own corners.
func facePaints(vertices [][3]numeric.Fixed, indices []uint16, origin [3]numeric.Fixed, project func(v, origin [3]numeric.Fixed) (int32, int32, int32)) bool {
	n := len(indices)
	if n < 3 {
		// A two-corner flat primitive makes both chains the same single edge,
		// so `xr == xl` on every row and nothing is drawn [R-RAST-01 §1] step 7.
		return false
	}
	for _, vi := range indices {
		if int(vi) >= len(vertices) {
			return false // malformed primitive suppresses the whole face [fmt 3do]
		}
	}
	var area int64
	px, py, _ := project(vertices[indices[n-1]], origin)
	for _, vi := range indices {
		x, y, _ := project(vertices[vi], origin)
		area += int64(px)*int64(y) - int64(x)*int64(py)
		px, py = x, y
	}
	return area > 0
}

// collectDrawPolys adapts canonical render records to the indexed framebuffer.
// It owns no hierarchy math: units, features, and projectiles all pass through
// render.UnitDraw [03 §2.4][03 §5.2].
//
// One authored primitive becomes one face, at its authored arity and in its
// authored corner order. There is no fan triangulation: retail's scan converter
// takes the vertex count as an argument and derives its two chains from the
// index order, so splitting a quad into triangles would change both the chains
// and the interpolation the section specifies [R-RAST-01 §1]. The winding cull
// is not applied here either — it is the span comparison inside the walk.
func (c *Client) collectDrawPolys(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8) []screenPoly {
	if c == nil || c.cam == nil || draw == nil || draw.Model == nil {
		return nil
	}
	var polys []screenPoly
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
						// Same publication gap as the sprite path above:
						// FeatureView carries no stable identity, so an
						// animated model texture would share cursor zero
						// across every feature. Suppressed, not shared.
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
			poly := newScreenPoly(n)
			poly.color, poly.frame = color, texFrame
			poly.useSHD = pr.ShadeRow != presentationrender.NoShadeRow
			poly.candidate, poly.piece, poly.primitive, poly.texture = uint32(len(polys)), pi, pri, pr.TextureName
			if texFrame != nil && c.rendererTraceSink != nil {
				poly.frameState = RendererValueAvailable
				if ref.kind == texStatic {
					poly.frameIndex = 0
				} else if ref.entry != nil {
					for fi := range ref.entry.Frames {
						if ref.entry.Frames[fi].Frame == texFrame {
							poly.frameIndex = fi
							break
						}
					}
				}
			}
			for corner, vi := range pr.VertexIndices {
				v := piece.WorldVertices[vi]
				// Retail composes model-relative: the piece chain result is
				// narrowed once, the shear applied, and the unit's position
				// enters only at the final blit [03 §5.2][R-REN-03A §1].
				lx, ly, ry := modelLocalVertex(v, draw.WorldPos)
				lx, ly = c.scaleModelLocal(lx, ly)
				poly.x[corner], poly.y[corner] = lx, ly
				poly.oddHeight[corner] = ry&1 != 0
				poly.attr[spanKey][corner] = modelHeightKey(v[1].Sub(draw.WorldPos[1]), draw.DiggerClip)
				if poly.useSHD {
					// The DONT_SHADE pin is the one retail value this lane can
					// hold before a corner's own row is known [R-RAST-01 §5].
					row := int32(presentationrender.SHDIdentityRow)
					if corner < len(pr.ShadeRows) {
						row = int32(pr.ShadeRows[corner])
					}
					poly.attr[spanRow][corner] = row
				}
			}
			if texFrame != nil {
				// Retail's quad mapper defaults the four corners to
				// (0,0) (w-1,0) (w-1,h-1) (0,h-1) in vertex-index order, with
				// the clamp built into the default rather than applied after
				// sampling [R-RAST-01 §1][R-REN-03A §5].
				u := [4]int32{0, int32(texFrame.Width) - 1, int32(texFrame.Width) - 1, 0}
				vv := [4]int32{0, 0, int32(texFrame.Height) - 1, int32(texFrame.Height) - 1}
				for corner := 0; corner < n && corner < 4; corner++ {
					poly.attr[spanU][corner], poly.attr[spanV][corner] = u[corner], vv[corner]
				}
			}
			polys = append(polys, poly)
		}
	}
	return polys
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

// modelExtentPolys and placePolys are modelExtent and placeTris over the body
// path's n-corner faces. The rules are identical — extrema seeded at the model
// origin with a two-pixel margin [R-REN-03A §1], and the supersampled shear's
// odd-height term [R-REN-03A §6]; only the corner count differs. The shadow
// rasterization still fan-triangulates, so both shapes are live.
func modelExtentPolys(polys []screenPoly) (width, height int, originX, originY int32) {
	var minX, minY, maxX, maxY int32 // seeded at the model origin, not at a vertex
	for i := range polys {
		for k := range polys[i].x {
			if polys[i].x[k] < minX {
				minX = polys[i].x[k]
			}
			if polys[i].x[k] > maxX {
				maxX = polys[i].x[k]
			}
			if polys[i].y[k] < minY {
				minY = polys[i].y[k]
			}
			if polys[i].y[k] > maxY {
				maxY = polys[i].y[k]
			}
		}
	}
	originX = modelTargetMargin - minX
	originY = modelTargetMargin - minY
	return int(maxX - minX + 2*modelTargetMargin), int(maxY - minY + 2*modelTargetMargin), originX, originY
}

func placePolys(polys []screenPoly, originX, originY, scale int32) {
	for i := range polys {
		for k := range polys[i].x {
			if scale == 2 {
				polys[i].x[k] = 2 * (polys[i].x[k] + originX)
				polys[i].y[k] = 2*(polys[i].y[k]+originY) - boolToInt32(polys[i].oddHeight[k])
				continue
			}
			polys[i].x[k] += originX
			polys[i].y[k] += originY
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
	polys := c.collectDrawPolys(draw, owner, id, kind)
	if len(polys) == 0 {
		return false
	}
	anchorX, anchorY := c.modelAnchor(draw)
	width, height, originX, originY := modelExtentPolys(polys)

	scale := int32(1)
	if c.supersampleModel(draw.Structure) {
		scale = 2
	}
	placePolys(polys, originX, originY, scale)

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

	for i := range polys {
		if polys[i].frame != nil {
			c.blitTexturedPolyTarget(raster, &polys[i], polys[i].frame, reveal, id)
			continue
		}
		c.fillPolyTarget(raster, &polys[i], polys[i].color, reveal, id)
	}
	if scale == 2 {
		raster.resolveSupersample(target, &c.pal.Alpha)
	}
	if draw.DiggerClip {
		// A Digger definition raises every key by 75; erasing at or below 125
		// therefore removes exactly the geometry at or below the model origin,
		// which is the buried half of a pop-up defence [R-REN-03A §8].
		target.eraseAtOrBelow(uint8(diggerEraseThreshold))
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
			if !modelFacePaints(piece.WorldVertices, pr.VertexIndices, draw.WorldPos) {
				// The outline is the same edge walk as the body, so the
				// winding cull removes the same faces from it [R-COMP-01 §3].
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
	// Retail keys the pulse off the unit's own sixteen-bit identifier. The pool
	// slot index is that number in this build — a stable per-unit value in the
	// same range — so this is a naming difference, not an open question.
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

// fillPolyTarget is retail's flat polygon span writer over the two-chain walk:
// one colour byte per pixel, the key interpolated across the span and tested
// per pixel [R-RAST-01 §1] steps 5-6, [R-REN-03A §5]. A non-nil reveal composes
// the nanoframe verdict on top of the same admission [03 §5.2].
//
// Unimplemented: [R-REN-03A §5]'s writer table gives the *shaded* flat polygon
// `SHD[row*256 + colour]`. This writer emits the raw colour byte for every flat
// face, as the client has since the flat filler was introduced; adopting the
// shaded form recolours every untextured face in the game and is its own unit.
func (c *Client) fillPolyTarget(target *modelTarget, p *screenPoly, color uint8, rev *presentationrender.NanoframeReveal, ids ...uint64) {
	if target == nil || p == nil {
		return
	}
	id := rendererID(ids)
	face := p.traceFace()
	target.polyScan(p, func(row, xl, xr int32, a, da *[spanAttrs]int64) {
		base := row * int32(target.width)
		acc := *a
		for px := xl; px < xr; px++ {
			key := spanByte(acc[spanKey])
			idx := int(base + px)
			event := -1
			if target.trace != nil {
				event = target.traceCandidate(rendererCandidate(face, target.tick, id, px, row, key, target.storedKey(idx), color, 0, rendererTexture(face, 0, 0, 0, RendererValueUnavailable, false)))
			}
			switch {
			case !target.admit(idx, key):
				target.traceRejected(event, RendererReasonHeightRejected, "height")
			case rev == nil:
				target.traceAdmitted(idx, event)
				target.write(idx, color, true)
			default:
				target.traceAdmitted(idx, event)
				if b, ok := nanoframeVerdict(*rev, key, color); ok {
					if event >= 0 {
						target.trace.events[event].CandidateIndex = b
					}
					target.write(idx, b, true)
				} else {
					target.traceRejected(event, RendererReasonNanoframeErase, "nanoframe-erase")
					target.write(idx, 0, false)
				}
			}
			for k := range acc {
				acc[k] += da[k]
			}
		}
	})
}

// blitTexturedPolyTarget is retail's textured quad mapper over the same walk.
// The texel is sampled at the interpolated integer texel coordinates —
// `pixels[(v >> 16) * w + (u >> 16)]` — with no perspective divide and no
// stored UVs; the default corners keep u in [0, w-1] and v in [0, h-1] because
// the interpolation never reaches the right corner's value [R-RAST-01 §1]
// steps 5-6. The SHD row rides the same interpolation as the key
// [R-RAST-01 §5].
//
// Unimplemented: [R-REN-03A §5] establishes (bounded-negative) that none of the
// four span writers tests the sampled texel against a transparent index — a
// texture is fully opaque inside the model raster path, and transparency is
// expressed only by the composition image's own background. This writer still
// skips a texel the GAF marks transparent, which is what the client has always
// done; dropping that test changes the silhouette of every textured face and is
// its own unit.
func (c *Client) blitTexturedPolyTarget(target *modelTarget, p *screenPoly, gafFrame *formats.GAFFrame, rev *presentationrender.NanoframeReveal, ids ...uint64) {
	if target == nil || p == nil || gafFrame == nil {
		return
	}
	id := rendererID(ids)
	face := p.traceFace()
	w, h := int(gafFrame.Width), int(gafFrame.Height)
	target.polyScan(p, func(row, xl, xr int32, a, da *[spanAttrs]int64) {
		base := row * int32(target.width)
		acc := *a
		for px := xl; px < xr; px++ {
			tx, ty := int(acc[spanU]>>16), int(acc[spanV]>>16)
			b, opaque := gafFrame.At(tx, ty)
			key := spanByte(acc[spanKey])
			idx := int(base + px)
			sourceState := RendererValueUnavailable
			if tx >= 0 && ty >= 0 && tx < w && ty < h && ty*w+tx < len(gafFrame.Pixels) {
				sourceState = RendererValueAvailable
				b = gafFrame.Pixels[ty*w+tx]
			}
			shade := presentationrender.NoShadeRow
			if p.useSHD {
				shade = spanShadeRow(acc[spanRow])
			}
			event := -1
			if target.trace != nil {
				event = target.traceCandidate(rendererCandidate(face, target.tick, id, px, row, key, target.storedKey(idx), b, shade, rendererTexture(face, tx, ty, b, sourceState, !opaque)))
			}
			switch {
			case !opaque:
				target.traceRejected(event, RendererReasonTransparentTexel, "transparent-texel")
			case !target.admit(idx, key):
				target.traceRejected(event, RendererReasonHeightRejected, "height")
			default:
				target.traceAdmitted(idx, event)
				if c != nil && c.pal != nil && p.useSHD {
					b = c.pal.Shade[shade][b]
				}
				if rev != nil {
					var ok bool
					if b, ok = nanoframeVerdict(*rev, key, b); !ok {
						target.traceRejected(event, RendererReasonNanoframeErase, "nanoframe-erase")
						target.write(idx, 0, false)
						break
					}
				}
				if event >= 0 {
					target.trace.events[event].CandidateIndex = b
				}
				target.write(idx, b, true)
			}
			for k := range acc {
				acc[k] += da[k]
			}
		}
	})
}

// spanShadeRow narrows an interpolated SHD row. The rows the model path carries
// are already 0..31 by construction (`trunc(dot * 5.0) & 0x1F`), so the clamp
// only guards the table lookup [R-RAST-01 §5][03 §2.4.1].
func spanShadeRow(v int64) int {
	r := int(v >> 16)
	if r < 0 {
		return 0
	}
	if r > 31 {
		return 31
	}
	return r
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
