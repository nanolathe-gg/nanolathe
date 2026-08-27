package client

// Software 3DO model presentation [fmt 3do][03 §2.5].
//
// Presentation only (I6): this file never touches sim state. It expands a
// loaded ThreeDO into world-space triangles once (fan triangulation), rotates
// them by the unit heading at draw time (model draw
// trig is on the I2 float allowlist), projects through the orthographic
// half-shear [03 §2.5], and rasterizes with painter's order.
//
// Face shading follows retail: flat-colored primitives keep palette color without SHD,
// textured primitives shade via SHD row = __ftol(dot*5)&31 (trunc*5 mod 32) with dont-shade pin 15
// [03 §4.3][03 §2.4.1][rr-09 addendum] via per-vertex averaged normals; implemented below.

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/presentation"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// lightDir is retail's shipped default model light [03 §2.4.1].
var lightDir = [3]float64{-0.8, 1.0, 0.25}

func sqrt3(x float64) float64 { return math.Sqrt(x) }

type modelCorner struct {
	x, y, z float64 // model space in world units (3DO 16.16 >>16)
	u, v    float64 // implied texture coords, quads map in index order [fmt 3do]
}

type modelTri struct {
	c      [3]modelCorner
	piece  string // source piece name, for diagnostics/provenance
	color  uint8
	hasTex bool     // resolved to a texture (static, team, or animated)
	ref    texRef   // texture resolution; animated frames pick at draw time
	row    [3]int   // per-corner SHD row [03 §2.4.1]
	vkey   [3]int64 // packed piece+vertex id, resolved to rows post-walk
	order  int      // expansion order; draw order is load-fixed [03 2.4]
}

// pieceModel is the per-piece authored geometry after load-time half-turn [03 §2.4].
type pieceModel struct {
	name         string
	parent       int // -1=root
	children     []int
	translate    [3]numeric.Fixed // authored parent translation after half-turn (neg X,Z) [03 §2.4]
	vertices     [][3]numeric.Fixed
	prims        []primModel
	hasSelection bool // root declares selection primitive at index 0 [03 §2.4]
}

// primModel is one primitive after load-time reorder [03 §2.4] [fmt 3do].
type primModel struct {
	color       uint8
	indices     []uint16
	hasTex      bool
	ref         texRef
	order       int
	isSelection bool // true if this is the selection plate (index 0 when hasSelection)
}

// unitModel is the compiled 3DO after load-time reorder, half-turn, and texture resolve [fmt 3do][03 §2.4][03 §2.4.1].
type unitModel struct {
	// compiled is the canonical hierarchy product.  The pieceModel fields are
	// a deliberately thin raster adapter retained for the client framebuffer;
	// they are copied from compiled and never perform another 3DO conversion.
	compiled    *compiledmodel.Model
	pieces      []pieceModel
	pieceByName map[string]int // lower-case name → piece index
	tris        []modelTri     // legacy flat list kept for fallback when piece transforms unavailable
}

// xformNode is the leaf→root snapshot for hierarchical Compose [03 §2.4] C21.
type pieceState struct {
	rotX, rotY, rotZ uint16
	tx, ty, tz       numeric.Fixed
	dontShade        bool
	hidden           bool
	dontShadow       bool
}

type xformNode struct {
	t          [3]numeric.Fixed
	ax, ay, az uint16
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

// resolveTextureRef makes side-before-default precedence explicit. Callers
// pass the already compiled side set and fallback set; enumeration order is
// never allowed to decide which authored entry wins [03 §2.4.1].
func resolveTextureRef(side, fallback map[string]texRef, name string) (texRef, bool) {
	key := strings.ToLower(name)
	if ref, ok := side[key]; ok {
		return ref, true
	}
	ref, ok := fallback[key]
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
	ids := make([]presentation.AssetID, len(frames))
	durations := make([]uint32, len(frames))
	for i, frame := range ref.entry.Frames {
		frames[i] = frame.Frame
		ids[i] = presentation.AssetID(ref.key + "#" + fmt.Sprint(i))
		durations[i] = uint32(frame.Value)
	}
	p := &modelTextureCursor{
		player: presentationrender.NewTexturePlayer(presentation.AssetSequence{
			ID: presentation.AssetID(ref.key), Frames: ids, Durations: durations, Loop: true,
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
		if presentation.AssetID(ref.key+"#"+fmt.Sprint(i)) == asset {
			return p.frames[i]
		}
	}
	return nil
}

func unitPresentationID(v snapshot.UnitView) uint64 {
	if v.InstanceID != 0 {
		return v.InstanceID
	}
	return uint64(v.Slot)
}

func featurePresentationID(v snapshot.FeatureView) uint64 {
	if v.InstanceID != 0 {
		return v.InstanceID
	}
	return uint64(uint32(v.CX))<<32 | uint64(uint32(v.CZ))
}

func projectilePresentationID(v snapshot.ProjectileView) uint64 {
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
	ref := texRef{kind: texAnimated, key: key, entry: entry, frame: entry.Frames[0].Frame}
	return c.modelAnimatedFrame(ref, 2, id)
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
		if err != nil {
			c.modelErrors[name] = err
		}
		return nil
	}
	um := &unitModel{
		compiled:    compiled,
		pieces:      make([]pieceModel, len(compiled.Pieces)),
		pieceByName: map[string]int{},
	}
	order := 0
	// Adapt the canonical hierarchy to the rasterizer.  Half-turn conversion,
	// selection swap, and primitive ordering have already happened in
	// formats/internal-model at load time [03 §2.4].
	for i, o := range compiled.Pieces {
		p := &um.pieces[i]
		p.name = o.Name
		p.parent = int(o.Parent)
		p.translate = o.Translate
		p.vertices = append([][3]numeric.Fixed(nil), o.Vertices...)
		p.hasSelection = o.Selection
		um.pieceByName[strings.ToLower(o.Name)] = i
		for pi, pp := range o.Primitives {
			hasTex := false
			var ref texRef
			color := uint8(pp.ColorIndex & 0xff)
			if pp.TextureName != "" {
				if r, ok := c.texIndex[strings.ToLower(pp.TextureName)]; ok {
					hasTex = true
					ref = r
				} else {
					color = 0xd1 // authored texture miss becomes the established flat quad [03 §2.4.1]
				}
			}
			// Keep all primitives for completeness; draw will skip flat non-quads and selection plate.
			// flare/flash pieces are emit points: they have 1 vert 0 prims so no prims; keep but not drawn.
			p.prims = append(p.prims, primModel{
				color:       color,
				indices:     append([]uint16(nil), pp.VertexIndices...),
				hasTex:      hasTex,
				ref:         ref,
				order:       order,
				isSelection: o.Selection && pi == 0,
			})
			order++
		}
	}
	// Build children lists from parent [fmt 3do][03 §2.4]
	for i := range um.pieces {
		um.pieces[i].children = nil
	}
	for i, p := range um.pieces {
		if p.parent >= 0 && p.parent < len(um.pieces) {
			par := p.parent
			um.pieces[par].children = append(um.pieces[par].children, i)
		}
	}
	// Legacy flat tris remain empty; all authored models use the hierarchy path.
	um.tris = nil
	if len(um.pieces) == 0 {
		return nil
	}
	return um
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

// drawUnitModel projects and rasterizes the unit's authored model; false when
// no model is available so the caller may use its separate diagnostic body.
// It uses hierarchical piece transforms via VM.Pieces [03 §2.4] C21–C22, heading
// folded into root [03 §2.4] C24, SHD row = trunc(dot*5) mod 32 with
// per-vertex averaged normals and dont-shade pin 15 [03 §2.4.1], flat quads
// only and textured any count [03 §2.4.1], selection plate never draws
// (primitive loop starts at 1) [03 §2.4.1], team LOGOS 10 frames per-owner
// [fmt 3do], miss 0xd1 gray [03 §2.4.1], shadows [03 §5.3], and
// nanoframe presentation [03 §5.7][05 "Construction target state"].
func (c *Client) drawUnitModelDirect(v snapshot.UnitView, sx, sy int32) bool {
	m := c.unitModelFor(v.Model)
	if m == nil || c.cam == nil || len(m.pieces) == 0 {
		// Model-load diagnostic emitted once per unit slot, not per frame spam [ON-08].
		if m == nil && c.cam != nil && v.Model != "" {
			if c.modelFallbacks == nil {
				c.modelFallbacks = map[uint16]struct{}{}
			}
			key := uint16(v.Slot)
			if _, seen := c.modelFallbacks[key]; !seen {
				err := c.modelErrors[v.Model]
				if err == nil {
					err = fmt.Errorf("model not available")
				}
				fmt.Fprintf(os.Stderr, "{\"level\":\"warn\",\"msg\":\"authored model unavailable\",\"slot\":%d,\"model\":%q,\"owner\":%d,\"error\":%q}\n", v.Slot, v.Model, v.Owner, err.Error())
				c.modelFallbacks[key] = struct{}{}
			}
		}
		return false
	}
	// Build per-piece dynamic states from snapshot [03 §2.4] C21–C22.
	n := len(m.pieces)
	states := make([]pieceState, n)
	// Map snapshot pieces by name or index [03 §2.4] C23; fallback to index.
	for _, pv := range v.Pieces {
		idx := -1
		if pv.Name != "" {
			if pi, ok := m.pieceByName[strings.ToLower(pv.Name)]; ok {
				idx = pi
			}
		} else if pv.Index >= 0 && pv.Index < n {
			idx = pv.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		states[idx].rotX = pv.RotX
		states[idx].rotY = pv.RotY
		states[idx].rotZ = pv.RotZ
		states[idx].tx = pv.Tx
		states[idx].ty = pv.Ty
		states[idx].tz = pv.Tz
		states[idx].dontShade = pv.DontShade
		states[idx].hidden = pv.Hidden
		states[idx].dontShadow = pv.DontShadow
	}
	// Fold unit orientation into root [03 §2.4] C24 bank→Z heading→Y pitch→X outermost.
	root := -1
	for i, p := range m.pieces {
		if p.parent == -1 {
			root = i
			break
		}
	}
	if root >= 0 {
		states[root].rotZ += v.Bank
		states[root].rotY -= v.Heading // heading clockwise vs Y CCW [03 §2.4] C21
		states[root].rotX += v.Pitch
	}
	ux, uy, uz := int32(v.X>>16), int32(v.Y>>16), int32(v.Z>>16)
	owner := int(v.Owner) % 10
	isNanoframe := v.BuildRemaining > 0
	// Collect tris for painter order: no per-frame sort, load-fixed order across pieces [03 §2.4].
	var tris []screenTri
	// Per-piece transform and SHD handling. For each piece, build chain and transform vertices.
	for pi, piece := range m.pieces {
		if states[pi].hidden {
			continue
		}
		// Hidden parent hides child: walk parent chain [03 §2.4] C22.
		{
			hiddenAncestor := false
			cur := piece.parent
			seen := map[int]bool{}
			for cur >= 0 && cur < len(m.pieces) {
				if seen[cur] {
					break
				}
				seen[cur] = true
				if states[cur].hidden {
					hiddenAncestor = true
					break
				}
				cur = m.pieces[cur].parent
			}
			if hiddenAncestor {
				continue
			}
		}
		// Build leaf→root chain for this piece [03 §2.4] C21.
		chain := c.buildPieceChain(m, pi, states)
		if chain == nil && pi != root && len(piece.vertices) > 0 {
			continue
		}
		// Transform all vertices of this piece into model space [03 §2.4] C21.
		worldVerts := make([][3]numeric.Fixed, len(piece.vertices))
		modelVertsF := make([][3]float64, len(piece.vertices)) // for normals (model space, without world pos)
		for vi, lv := range piece.vertices {
			modelPos := c.applyPiece(m, pi, lv, states)
			worldVerts[vi] = [3]numeric.Fixed{
				modelPos[0].Add(numeric.Fixed(int64(ux) << 16)),
				modelPos[1].Add(numeric.Fixed(int64(uy) << 16)),
				modelPos[2].Add(numeric.Fixed(int64(uz) << 16)),
			}
			// modelVertsF for normal computation (without world translation, translation cancels)
			modelVertsF[vi] = [3]float64{
				float64(modelPos[0].Raw()) / 65536,
				float64(modelPos[1].Raw()) / 65536,
				float64(modelPos[2].Raw()) / 65536,
			}
		}
		// Per-vertex normal accumulation for this piece's prims [03 §2.4.1].
		normAcc := make([][3]float64, len(piece.vertices))
		normCnt := make([]int, len(piece.vertices))
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue // selection plate never draws [03 §2.4.1]
			}
			n := len(pr.indices)
			if n < 3 {
				continue
			}
			if !pr.hasTex && n != 4 {
				continue // flat only quads [03 §2.4.1]
			}
			// Face normal from first three vertices cross(b-a,b-c) [03 §2.4.1]
			if n >= 3 {
				aIdx := int(pr.indices[0])
				bIdx := int(pr.indices[1])
				cIdx := int(pr.indices[2])
				if aIdx < len(modelVertsF) && bIdx < len(modelVertsF) && cIdx < len(modelVertsF) {
					av := modelVertsF[aIdx]
					bv := modelVertsF[bIdx]
					cv := modelVertsF[cIdx]
					ax, ay, az := bv[0]-av[0], bv[1]-av[1], bv[2]-av[2]
					bx, by, bz := bv[0]-cv[0], bv[1]-cv[1], bv[2]-cv[2]
					nx := ay*bz - az*by
					ny := az*bx - ax*bz
					nz := ax*by - ay*bx
					if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
						nx, ny, nz = nx/l, ny/l, nz/l
					} else {
						nx, ny, nz = 0, 1, 0
					}
					for _, vi := range pr.indices {
						if int(vi) < len(normAcc) {
							normAcc[vi][0] += nx
							normAcc[vi][1] += ny
							normAcc[vi][2] += nz
							normCnt[vi]++
						}
					}
				}
			}
		}
		// Build per-vertex SHD rows [03 §2.4.1] row = trunc(dot*5) &31, dont-shade pin 15.
		vertRows := make([]int, len(piece.vertices))
		for vi := range piece.vertices {
			if states[pi].dontShade {
				vertRows[vi] = 15
				continue
			}
			cnt := normCnt[vi]
			if cnt == 0 {
				vertRows[vi] = 15
				continue
			}
			nx := normAcc[vi][0] / float64(cnt)
			ny := normAcc[vi][1] / float64(cnt)
			nz := normAcc[vi][2] / float64(cnt)
			d := nx*lightDir[0] + ny*lightDir[1] + nz*lightDir[2]
			vertRows[vi] = int(d*5.0) & 31
		}
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue
			}
			n := len(pr.indices)
			if n < 3 {
				continue
			}
			if !pr.hasTex && n != 4 {
				continue
			}
			// Resolve texture frame at draw time [fmt 3do]
			var frame *formats.GAFFrame
			var entry *formats.GAFEntry
			isTeam := false
			if pr.hasTex {
				switch pr.ref.kind {
				case texAnimated:
					frame = c.modelAnimatedFrame(pr.ref, 0, unitPresentationID(v))
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
				}
				if frame == nil {
					continue
				}
			}
			// Handle quad UVs and n-gon bbox UVs [fmt 3do][03 §2.4.1]
			uvs := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
			ngonUV := n > 4
			ua, ub := 0, 2
			var aMin, aSpan, bMin, bSpan float64
			if ngonUV {
				var lo, hi [3]float64
				first := true
				for _, vi := range pr.indices {
					if int(vi) >= len(piece.vertices) {
						continue
					}
					// Use model-space for bbox (without world)
					mv := modelVertsF[vi]
					if first {
						lo, hi = mv, mv
						first = false
						continue
					}
					for a := 0; a < 3; a++ {
						if mv[a] < lo[a] {
							lo[a] = mv[a]
						}
						if mv[a] > hi[a] {
							hi[a] = mv[a]
						}
					}
				}
				var ext [3]float64
				for a := 0; a < 3; a++ {
					ext[a] = hi[a] - lo[a]
				}
				ua, ub = 0, 1
				if ext[1] >= ext[0] && ext[1] >= ext[2] {
					ua, ub = 1, 2
				} else if ext[2] >= ext[0] && ext[2] >= ext[1] {
					ua, ub = 0, 2
				}
				if ext[ua] > ext[ub] {
					ua, ub = ub, ua
				}
				aMin, aSpan = lo[ua], ext[ua]
				bMin, bSpan = lo[ub], ext[ub]
				if aSpan == 0 {
					aSpan = 1
				}
				if bSpan == 0 {
					bSpan = 1
				}
			}
			// Fan triangulation [03 §2.4]
			for k := 1; k+1 < n; k++ {
				idx0 := int(pr.indices[0])
				idx1 := int(pr.indices[k])
				idx2 := int(pr.indices[k+1])
				if idx0 >= len(worldVerts) || idx1 >= len(worldVerts) || idx2 >= len(worldVerts) {
					continue
				}
				var st screenTri
				st.color = pr.color
				st.order = pr.order
				// Per-corner rows
				st.row = [3]float64{float64(vertRows[idx0]), float64(vertRows[idx1]), float64(vertRows[idx2])}
				// Team flag
				if isTeam {
					st.entry = entry
					st.team = true
				}
				if pr.hasTex {
					// Use resolved frame for this draw; will be selected per owner later
					if isTeam {
						st.frame = frame
					} else {
						st.frame = frame
					}
					// UVs
					uvFor := func(vi int, cornerIdx int) [2]float64 {
						if ngonUV {
							mv := modelVertsF[vi]
							return [2]float64{(mv[ua] - aMin) / aSpan, (mv[ub] - bMin) / bSpan}
						}
						return uvs[cornerIdx]
					}
					// Need to map triangle corners to uv index: fan 0,k,k+1 where 0 is always corner 0,
					// k is corner k, k+1 is corner k+1 in original polygon order.
					st.u[0], st.v[0] = uvFor(idx0, 0)[0], uvFor(idx0, 0)[1]
					st.u[1], st.v[1] = uvFor(idx1, k)[0], uvFor(idx1, k)[1]
					st.u[2], st.v[2] = uvFor(idx2, k+1)[0], uvFor(idx2, k+1)[1]
					// Fix quad case where k=1 and k+1=2 etc need correct uvs mapping.
					if !ngonUV {
						// For quads, fan gives two tris: (0,1,2) and (0,2,3)
						// uvs already 0→(0,0)1→(1,0)2→(1,1)3→(0,1) so mapping holds.
					}
				}
				for triCorner, vi := range []int{idx0, idx1, idx2} {
					wv := worldVerts[vi]
					wx := int32(wv[0] >> 16)
					wy := int32(wv[1] >> 16)
					wz := int32(wv[2] >> 16)
					px := wx - c.cam.X
					py := wz - (wy >> 1) - c.cam.Z
					st.x[triCorner], st.y[triCorner] = px, py
					st.depth += py
				}
				st.depth /= 3
				tris = append(tris, st)
			}
		}
	}
	// Model shadows are intentionally not emitted by this compatibility
	// raster adapter. The established stencil requires option, terrain-depth,
	// SHD-row, and dither inputs that this path does not own; a guessed offset
	// triangle would violate [03 §5.3]. The canonical shadow compositor will
	// consume those inputs when published.
	// Draw model tris
	for i := range tris {
		t := &tris[i]
		if isNanoframe {
			// Nanoframe presentation: stipple pattern [03 §5.7][05 "Construction target state"]
			// Draw with checker skip to appear translucent; use health green tint for outline?
			// For now, draw with dither: skip pixels where (x+y)%2==0 to mimic transparency.
			// We achieve by drawing normally then punching holes? Instead we modify rasterizer to skip.
			// Simple: for nanoframe, draw flat tris with color 0xd1 gray placeholder and stipple via fillTriNanoframe
			if t.frame != nil {
				// Textured nanoframe: still textured but with stipple; use blit with nanoframe flag
				sFrame := t.frame
				if t.team && t.entry != nil && owner < len(t.entry.Frames) {
					sFrame = t.entry.Frames[owner].Frame
				}
				if sFrame != nil {
					c.blitTexturedTriNanoframe(t, sFrame)
					continue
				}
			}
			c.fillTriNanoframe(t, t.color)
			continue
		}
		if t.frame != nil {
			frame := t.frame
			if t.team && t.entry != nil && owner < len(t.entry.Frames) {
				frame = t.entry.Frames[owner].Frame
			}
			if frame != nil {
				c.blitTexturedTri(t, frame)
				continue
			}
		}
		c.fillTri(t, t.color)
	}
	return true
}

// drawUnitModel dispatches the authored model raster path:
// fixed buildings (IsBuilding) take the 2x supersampled path with per-piece
// dont-shade [03 §2.4.1][04 §4.3] 0x1000e000, mobile units take the 1x cached
// path with no shading and then both blit in Y-bucket order [03 §1].
// Offscreen caches are per-unit and then blitted, matching retail's
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Client) drawUnitModel(v snapshot.UnitView, sx, sy int32) bool {
	// A model identity is an authored visual request.  Once present, the
	// compatibility renderer must not replace an unresolved or non-rasterizable
	// model with a guessed footprint body; missing art is an empty draw [03 §5.1].
	// The concrete paths still run for their one-shot diagnostic accounting.
	if v.Model != "" {
		if v.IsBuilding {
			_ = c.drawBuildingModelOffscreen(v)
		} else {
			_ = c.drawMobileModelOffscreen(v)
		}
		return true
	}
	if v.IsBuilding {
		return c.drawBuildingModelOffscreen(v)
	}
	return c.drawMobileModelOffscreen(v)
}

// collectUnitTris builds the triangle list through internal/render's canonical
// transform and normal pipeline. The client owns only indexed raster
// adaptation; authored geometry and shade rows come from the canonical model
// draw records [03 §2.4][03 §2.4.1].
func (c *Client) collectUnitTris(v snapshot.UnitView, useShade bool) ([]screenTri, int32, int32, int32, int32) {
	m := c.unitModelFor(v.Model)
	if m == nil || m.compiled == nil || c.cam == nil {
		return c.collectUnitTrisLegacy(v, useShade)
	}
	base := make([]compiledmodel.PieceState, len(m.compiled.Pieces))
	for _, pv := range v.Pieces {
		idx := -1
		if pv.Name != "" {
			if found, ok := m.pieceByName[strings.ToLower(pv.Name)]; ok {
				idx = found
			}
		} else if pv.Index >= 0 && pv.Index < len(base) {
			idx = pv.Index
		}
		if idx < 0 || idx >= len(base) {
			continue
		}
		base[idx] = compiledmodel.PieceState{
			RotX: pv.RotX, RotY: pv.RotY, RotZ: pv.RotZ,
			Trans:     [3]numeric.Fixed{pv.Tx, pv.Ty, pv.Tz},
			DontShade: pv.DontShade, Hidden: pv.Hidden, DontShadow: pv.DontShadow,
		}
	}
	draw := presentationrender.BuildUnitDraw(m.compiled, base, v.Heading, v.Pitch, v.Bank, v, v, 0, c.orientationCache(unitPresentationID(v)))
	if draw == nil {
		return nil, 0, 0, 0, 0
	}
	var tris []screenTri
	owner := int(v.Owner) % 10
	for pi, piece := range draw.Pieces {
		if pi < len(m.compiled.Pieces) && m.compiled.Pieces[pi].Selection {
			// Selection plates remain in the compiled record but are excluded
			// from the face raster pass [03 §2.4.1].
			if len(piece.Primitives) > 0 {
				piece.Primitives = piece.Primitives[1:]
			}
		}
		for _, pr := range piece.Primitives {
			n := len(pr.VertexIndices)
			if n < 3 {
				continue
			}
			ref, textured := resolveTextureRef(nil, c.texIndex, pr.TextureName)
			color := uint8(pr.ColorIndex & 0xff)
			if pr.TextureName != "" && !textured {
				color = 0xd1
			}
			var frame *formats.GAFFrame
			var entry *formats.GAFEntry
			team := false
			if textured {
				switch ref.kind {
				case texAnimated:
					frame = c.modelAnimatedFrame(ref, 0, unitPresentationID(v))
				case texTeam:
					entry, team = ref.entry, true
					if owner >= 0 && owner < len(ref.entry.Frames) {
						frame = ref.entry.Frames[owner].Frame
					}
				default:
					frame = ref.frame
				}
				if frame == nil {
					continue // an unresolved authored frame contributes no pixels [I9]
				}
			}
			uvFor := func(corner int, vi int) (float64, float64) {
				if n == 4 {
					uvs := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
					return uvs[corner][0], uvs[corner][1]
				}
				// N-gons use affine edge coordinates over their dominant
				// projected extents; this preserves every original corner's row
				// and UV before fan expansion [03 §2.4.1].
				if vi < 0 || vi >= len(piece.WorldVertices) {
					return 0, 0
				}
				var minX, maxX, minZ, maxZ int64
				first := true
				for _, idx := range pr.VertexIndices {
					if int(idx) >= len(piece.WorldVertices) {
						continue
					}
					p := piece.WorldVertices[idx]
					x, z := p[0].Raw(), p[2].Raw()
					if first {
						minX, maxX, minZ, maxZ, first = x, x, z, z, false
						continue
					}
					if x < minX {
						minX = x
					}
					if x > maxX {
						maxX = x
					}
					if z < minZ {
						minZ = z
					}
					if z > maxZ {
						maxZ = z
					}
				}
				if maxX == minX {
					maxX = minX + 1
				}
				if maxZ == minZ {
					maxZ = minZ + 1
				}
				p := piece.WorldVertices[vi]
				return float64(p[0].Raw()-minX) / float64(maxX-minX), float64(p[2].Raw()-minZ) / float64(maxZ-minZ)
			}
			for k := 1; k+1 < n; k++ {
				indices := [3]int{int(pr.VertexIndices[0]), int(pr.VertexIndices[k]), int(pr.VertexIndices[k+1])}
				var st screenTri
				st.color, st.frame, st.entry, st.team, st.order = color, frame, entry, team, pi
				for corner, vi := range indices {
					if vi < 0 || vi >= len(piece.WorldVertices) {
						st.x[corner], st.y[corner] = 0, 0
						continue
					}
					wv := piece.WorldVertices[vi]
					wx, wy, wz := int32(wv[0]>>16), int32(wv[1]>>16), int32(wv[2]>>16)
					st.x[corner] = wx - c.cam.X
					st.y[corner] = wz - (wy >> 1) - c.cam.Z
					st.depth += st.y[corner]
					if vi < len(pr.ShadeRows) {
						st.row[corner] = float64(pr.ShadeRows[vi])
					} else {
						st.row[corner] = 15
					}
				}
				st.depth /= 3
				if frame != nil {
					st.u[0], st.v[0] = uvFor(0, indices[0])
					st.u[1], st.v[1] = uvFor(k, indices[1])
					st.u[2], st.v[2] = uvFor(k+1, indices[2])
				}
				tris = append(tris, st)
			}
		}
	}
	_ = useShade // both fixed and mobile textured paths consume canonical rows [03 §2.4.1]
	return triBounds(tris)
}

func triBounds(tris []screenTri) ([]screenTri, int32, int32, int32, int32) {
	if len(tris) == 0 {
		return nil, 0, 0, 0, 0
	}
	minX, minY, maxX, maxY := tris[0].x[0], tris[0].y[0], tris[0].x[0], tris[0].y[0]
	for _, t := range tris {
		for i := 0; i < 3; i++ {
			if t.x[i] < minX {
				minX = t.x[i]
			}
			if t.x[i] > maxX {
				maxX = t.x[i]
			}
			if t.y[i] < minY {
				minY = t.y[i]
			}
			if t.y[i] > maxY {
				maxY = t.y[i]
			}
		}
	}
	return tris, minX, minY, maxX, maxY
}

// collectUnitTrisLegacy is retained solely for synthetic client fixtures that
// construct a unitModel without a compiled internal/model.Model.
func (c *Client) collectUnitTrisLegacy(v snapshot.UnitView, useShade bool) ([]screenTri, int32, int32, int32, int32) {
	m := c.unitModelFor(v.Model)
	if m == nil || len(m.pieces) == 0 {
		if m == nil && c.cam != nil && v.Model != "" {
			if c.modelFallbacks == nil {
				c.modelFallbacks = map[uint16]struct{}{}
			}
			key := uint16(v.Slot)
			if _, seen := c.modelFallbacks[key]; !seen {
				err := c.modelErrors[v.Model]
				if err == nil {
					err = fmt.Errorf("model not available")
				}
				fmt.Fprintf(os.Stderr, "{\"level\":\"warn\",\"msg\":\"authored model unavailable\",\"slot\":%d,\"model\":%q,\"owner\":%d,\"error\":%q}\n", v.Slot, v.Model, v.Owner, err.Error())
				c.modelFallbacks[key] = struct{}{}
			}
		}
		return nil, 0, 0, 0, 0
	}
	n := len(m.pieces)
	states := make([]pieceState, n)
	for _, pv := range v.Pieces {
		idx := -1
		if pv.Name != "" {
			if pi, ok := m.pieceByName[strings.ToLower(pv.Name)]; ok {
				idx = pi
			}
		} else if pv.Index >= 0 && pv.Index < n {
			idx = pv.Index
		}
		if idx < 0 || idx >= n {
			continue
		}
		states[idx].rotX = pv.RotX
		states[idx].rotY = pv.RotY
		states[idx].rotZ = pv.RotZ
		states[idx].tx = pv.Tx
		states[idx].ty = pv.Ty
		states[idx].tz = pv.Tz
		states[idx].dontShade = pv.DontShade
		states[idx].hidden = pv.Hidden
		states[idx].dontShadow = pv.DontShadow
	}
	root := -1
	for i, p := range m.pieces {
		if p.parent == -1 {
			root = i
			break
		}
	}
	if root >= 0 {
		states[root].rotZ += v.Bank
		states[root].rotY -= v.Heading // heading clockwise vs Y CCW [03 §2.4] C21
		states[root].rotX += v.Pitch
	}
	ux, uy, uz := int32(v.X>>16), int32(v.Y>>16), int32(v.Z>>16)
	if c.terrain != nil {
		if h := c.terrain.HeightAt(v.X, v.Z); h != numeric.Fixed(-1) {
			_ = h
		}
	}
	isNanoframe := v.BuildRemaining > 0
	_ = isNanoframe
	var tris []screenTri
	for pi, piece := range m.pieces {
		if states[pi].hidden {
			continue
		}
		hiddenAncestor := false
		cur := piece.parent
		seen := map[int]bool{}
		for cur >= 0 && cur < len(m.pieces) {
			if seen[cur] {
				break
			}
			seen[cur] = true
			if states[cur].hidden {
				hiddenAncestor = true
				break
			}
			cur = m.pieces[cur].parent
		}
		if hiddenAncestor {
			continue
		}
		chain := c.buildPieceChain(m, pi, states)
		if chain == nil && pi != root && len(piece.vertices) > 0 {
			continue
		}
		worldVerts := make([][3]numeric.Fixed, len(piece.vertices))
		modelVertsF := make([][3]float64, len(piece.vertices))
		for vi, lv := range piece.vertices {
			mp := c.applyPiece(m, pi, lv, states)
			worldVerts[vi] = [3]numeric.Fixed{mp[0].Add(numeric.Fixed(int64(ux) << 16)), mp[1].Add(numeric.Fixed(int64(uy) << 16)), mp[2].Add(numeric.Fixed(int64(uz) << 16))}
			modelVertsF[vi] = [3]float64{float64(mp[0].Raw()) / 65536, float64(mp[1].Raw()) / 65536, float64(mp[2].Raw()) / 65536}
		}
		normAcc := make([][3]float64, len(piece.vertices))
		normCnt := make([]int, len(piece.vertices))
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue
			}
			np := len(pr.indices)
			if np < 3 {
				continue
			}
			if !pr.hasTex && np != 4 {
				continue
			}
			if np >= 3 {
				aIdx := int(pr.indices[0])
				bIdx := int(pr.indices[1])
				cIdx := int(pr.indices[2])
				if aIdx < len(modelVertsF) && bIdx < len(modelVertsF) && cIdx < len(modelVertsF) {
					av, bv, cv := modelVertsF[aIdx], modelVertsF[bIdx], modelVertsF[cIdx]
					ax, ay, az := bv[0]-av[0], bv[1]-av[1], bv[2]-av[2]
					bx, by, bz := bv[0]-cv[0], bv[1]-cv[1], bv[2]-cv[2]
					nx := ay*bz - az*by
					ny := az*bx - ax*bz
					nz := ax*by - ay*bx
					if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
						nx, ny, nz = nx/l, ny/l, nz/l
					} else {
						nx, ny, nz = 0, 1, 0
					}
					for _, vi := range pr.indices {
						if int(vi) < len(normAcc) {
							normAcc[vi][0] += nx
							normAcc[vi][1] += ny
							normAcc[vi][2] += nz
							normCnt[vi]++
						}
					}
				}
			}
		}
		vertRows := make([]int, len(piece.vertices))
		for vi := range piece.vertices {
			if !useShade {
				vertRows[vi] = 15
				continue
			}
			if states[pi].dontShade {
				vertRows[vi] = 15
				continue
			}
			cnt := normCnt[vi]
			if cnt == 0 {
				vertRows[vi] = 15
				continue
			}
			nx := normAcc[vi][0] / float64(cnt)
			ny := normAcc[vi][1] / float64(cnt)
			nz := normAcc[vi][2] / float64(cnt)
			d := nx*lightDir[0] + ny*lightDir[1] + nz*lightDir[2]
			vertRows[vi] = int(d*5.0) & 31
		}
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue
			}
			np := len(pr.indices)
			if np < 3 {
				continue
			}
			if !pr.hasTex && np != 4 {
				continue
			}
			var frame *formats.GAFFrame
			var entry *formats.GAFEntry
			isTeam := false
			if pr.hasTex {
				switch pr.ref.kind {
				case texAnimated:
					frame = c.modelAnimatedFrame(pr.ref, 0, unitPresentationID(v))
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
				}
				if frame == nil {
					continue
				}
			}
			uvs := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
			ngonUV := np > 4
			ua, ub := 0, 2
			var aMin, aSpan, bMin, bSpan float64
			if ngonUV {
				var lo, hi [3]float64
				first := true
				for _, vi := range pr.indices {
					if int(vi) >= len(piece.vertices) {
						continue
					}
					mv := modelVertsF[vi]
					if first {
						lo, hi = mv, mv
						first = false
						continue
					}
					for a := 0; a < 3; a++ {
						if mv[a] < lo[a] {
							lo[a] = mv[a]
						}
						if mv[a] > hi[a] {
							hi[a] = mv[a]
						}
					}
				}
				var ext [3]float64
				for a := 0; a < 3; a++ {
					ext[a] = hi[a] - lo[a]
				}
				ua, ub = 0, 1
				if ext[1] >= ext[0] && ext[1] >= ext[2] {
					ua, ub = 1, 2
				} else if ext[2] >= ext[0] && ext[2] >= ext[1] {
					ua, ub = 0, 2
				}
				if ext[ua] > ext[ub] {
					ua, ub = ub, ua
				}
				aMin, aSpan = lo[ua], ext[ua]
				bMin, bSpan = lo[ub], ext[ub]
				if aSpan == 0 {
					aSpan = 1
				}
				if bSpan == 0 {
					bSpan = 1
				}
			}
			for k := 1; k+1 < np; k++ {
				idx0 := int(pr.indices[0])
				idx1 := int(pr.indices[k])
				idx2 := int(pr.indices[k+1])
				if idx0 >= len(worldVerts) || idx1 >= len(worldVerts) || idx2 >= len(worldVerts) {
					continue
				}
				var st screenTri
				st.color = pr.color
				st.order = pr.order
				st.row = [3]float64{float64(vertRows[idx0]), float64(vertRows[idx1]), float64(vertRows[idx2])}
				if isTeam {
					st.entry = entry
					st.team = true
				}
				if pr.hasTex {
					st.frame = frame
					uvFor := func(vi, cidx int) [2]float64 {
						if ngonUV {
							mv := modelVertsF[vi]
							return [2]float64{(mv[ua] - aMin) / aSpan, (mv[ub] - bMin) / bSpan}
						}
						return uvs[cidx]
					}
					st.u[0], st.v[0] = uvFor(idx0, 0)[0], uvFor(idx0, 0)[1]
					st.u[1], st.v[1] = uvFor(idx1, k)[0], uvFor(idx1, k)[1]
					st.u[2], st.v[2] = uvFor(idx2, k+1)[0], uvFor(idx2, k+1)[1]
				}
				for triCorner, vi := range []int{idx0, idx1, idx2} {
					wv := worldVerts[vi]
					wx := int32(wv[0] >> 16)
					wy := int32(wv[1] >> 16)
					wz := int32(wv[2] >> 16)
					px := wx - c.cam.X
					py := wz - (wy >> 1) - c.cam.Z
					st.x[triCorner], st.y[triCorner] = px, py
					st.depth += py
				}
				st.depth /= 3
				// Nanoframe buildings should still render (stipple handled
				// later); don't skip tris here. The original skip was for
				// direct nanoframe wireframe path, but offscreen cache should
				// show the building's geometry even while constructing.
				tris = append(tris, st)
			}
		}
	}
	if len(tris) == 0 {
		return nil, 0, 0, 0, 0
	}
	minX, minY, maxX, maxY := tris[0].x[0], tris[0].y[0], tris[0].x[0], tris[0].y[0]
	for _, t := range tris {
		for k := 0; k < 3; k++ {
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
	}
	return tris, minX, minY, maxX, maxY
}

// drawBuildingModelOffscreen renders a building at 2x supersampled into an
// offscreen cache then downscales via ALP 2x2 blend and blits in Y-bucket order
// [03 §2.5][rr-10]. Shading respects per-piece DontShade (row 15) [03 §2.4.1][04 §4.3].
func (c *Client) drawBuildingModelOffscreen(v snapshot.UnitView) bool {
	tris, minX, minY, maxX, maxY := c.collectUnitTris(v, true)
	if len(tris) == 0 {
		return false
	}
	bw := int(maxX - minX + 1)
	bh := int(maxY - minY + 1)
	if bw <= 0 || bh <= 0 || bw > 600 || bh > 600 {
		return false
	}
	// 2x supersampled temp with mask
	tw, th := bw*2, bh*2
	temp := make([]byte, tw*th)
	mask := make([]bool, tw*th)
	// Render scaled tris into temp
	for i := range tris {
		t := tris[i]
		var st screenTri = t
		for k := 0; k < 3; k++ {
			st.x[k] = (t.x[k] - minX) * 2
			st.y[k] = (t.y[k] - minY) * 2
		}
		if st.frame != nil {
			frame := st.frame
			if st.team && st.entry != nil {
				owner := int(v.Owner) % 10
				if owner < len(st.entry.Frames) {
					frame = st.entry.Frames[owner].Frame
				}
			}
			if frame != nil {
				blitTexturedTriToDest(temp, mask, tw, th, &st, frame, c.pal)
				continue
			}
		}
		fillTriToDest(temp, mask, tw, th, &st, st.color)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	dstW, dstH := bw, bh
	// Blit with clipping
	for y := 0; y < dstH; y++ {
		sy0 := y * 2
		sy1 := sy0 + 1
		dy := int(minY) + y
		if dy < 0 || dy >= c.height {
			continue
		}
		for x := 0; x < dstW; x++ {
			sx0 := x * 2
			sx1 := sx0 + 1
			dx := int(minX) + x
			if dx < 0 || dx >= c.width {
				continue
			}
			// Gather 2x2 block via mask
			vals := make([]byte, 0, 4)
			if mask[sy0*tw+sx0] {
				vals = append(vals, temp[sy0*tw+sx0])
			}
			if mask[sy0*tw+sx1] {
				vals = append(vals, temp[sy0*tw+sx1])
			}
			if mask[sy1*tw+sx0] {
				vals = append(vals, temp[sy1*tw+sx0])
			}
			if mask[sy1*tw+sx1] {
				vals = append(vals, temp[sy1*tw+sx1])
			}
			if len(vals) == 0 {
				continue
			}
			var out byte
			if len(vals) == 1 {
				out = vals[0]
			} else if c.pal != nil {
				// Blend via ALP sequentially
				out = vals[0]
				for i := 1; i < len(vals); i++ {
					out = c.pal.Alpha[int(out)*256+int(vals[i])]
				}
			} else {
				// Fallback average without palette
				sum := 0
				for _, v := range vals {
					sum += int(v)
				}
				out = byte(sum / len(vals))
			}
			c.indexed[dy*c.width+dx] = out
		}
	}
	return true
}

// drawMobileModelOffscreen renders a mobile unit at 1x without shading
// (row 15) into an offscreen cache then blits in Y-bucket order.
// No downscale, no SHD darkening — flat and team textures at identity [03 §4.3].
func (c *Client) drawMobileModelOffscreen(v snapshot.UnitView) bool {
	tris, minX, minY, maxX, maxY := c.collectUnitTris(v, false)
	if len(tris) == 0 {
		return false
	}
	bw := int(maxX - minX + 1)
	bh := int(maxY - minY + 1)
	if bw <= 0 || bh <= 0 || bw > 600 || bh > 600 {
		return false
	}
	temp := make([]byte, bw*bh)
	mask := make([]bool, bw*bh)
	for i := range tris {
		t := tris[i]
		var st screenTri = t
		for k := 0; k < 3; k++ {
			st.x[k] = t.x[k] - minX
			st.y[k] = t.y[k] - minY
		}
		// Force no shade: rows already 15, but ensure
		st.row = [3]float64{15, 15, 15}
		if st.frame != nil {
			frame := st.frame
			if st.team && st.entry != nil {
				owner := int(v.Owner) % 10
				if owner < len(st.entry.Frames) {
					frame = st.entry.Frames[owner].Frame
				}
			}
			if frame != nil {
				blitTexturedTriToDest(temp, mask, bw, bh, &st, frame, c.pal)
				continue
			}
		}
		fillTriToDest(temp, mask, bw, bh, &st, st.color)
	}
	// Blit 1:1 to main with transparency via mask
	for y := 0; y < bh; y++ {
		dy := int(minY) + y
		if dy < 0 || dy >= c.height {
			continue
		}
		for x := 0; x < bw; x++ {
			dx := int(minX) + x
			if dx < 0 || dx >= c.width {
				continue
			}
			if !mask[y*bw+x] {
				continue
			}
			c.indexed[dy*c.width+dx] = temp[y*bw+x]
		}
	}
	return true
}

// buildPieceChain builds leaf→root chain for piece idx [03 §2.4] C21.
func (c *Client) buildPieceChain(m *unitModel, idx int, states []pieceState) []xformNode {
	if m == nil || idx < 0 || idx >= len(m.pieces) {
		return nil
	}
	chain := []int{}
	cur := idx
	seen := map[int]bool{}
	for cur != -1 {
		if seen[cur] {
			break
		}
		seen[cur] = true
		chain = append(chain, cur)
		if cur < 0 || cur >= len(m.pieces) {
			break
		}
		cur = m.pieces[cur].parent
		if len(chain) > len(m.pieces) {
			break
		}
	}
	nodes := make([]xformNode, len(chain))
	for i, pi := range chain {
		var t [3]numeric.Fixed = m.pieces[pi].translate
		t[0] = t[0].Add(states[pi].tx)
		t[1] = t[1].Add(states[pi].ty)
		t[2] = t[2].Add(states[pi].tz)
		nodes[i] = xformNode{t: t, ax: states[pi].rotX, ay: states[pi].rotY, az: states[pi].rotZ}
	}
	return nodes
}

// applyPiece uses the canonical internal/model transform for models loaded
// from authored 3DO data. Synthetic test models retain the small local adapter
// path, but production geometry has one rotate-then-translate implementation
// [03 §2.4].
func (c *Client) applyPiece(m *unitModel, idx int, local [3]numeric.Fixed, states []pieceState) [3]numeric.Fixed {
	if m != nil && m.compiled != nil && idx >= 0 && idx < len(m.compiled.Pieces) {
		canonical := make([]compiledmodel.PieceState, len(m.compiled.Pieces))
		for i := range states {
			if i >= len(canonical) {
				break
			}
			canonical[i].RotX = states[i].rotX
			canonical[i].RotY = states[i].rotY
			canonical[i].RotZ = states[i].rotZ
			canonical[i].Trans = [3]numeric.Fixed{states[i].tx, states[i].ty, states[i].tz}
			canonical[i].DontShade = states[i].dontShade
			canonical[i].Hidden = states[i].hidden
			canonical[i].DontShadow = states[i].dontShadow
		}
		return compiledmodel.Compose(m.compiled, canonical, idx).Apply(local)
	}
	return c.applyChain(local, c.buildPieceChain(m, idx, states))
}

// applyChain transforms point p via nodes leaf→root [03 §2.4] C21.
func (c *Client) applyChain(p [3]numeric.Fixed, nodes []xformNode) [3]numeric.Fixed {
	x := float64(p[0].Raw())
	y := float64(p[1].Raw())
	z := float64(p[2].Raw())
	for _, n := range nodes {
		if n.az != 0 {
			theta := float64(n.az) * 2 * math.Pi / 65536
			co := math.Cos(theta)
			si := math.Sin(theta)
			nx := math.Round(co*x - si*y)
			ny := math.Round(si*x + co*y)
			x, y = nx, ny
		}
		if n.ax != 0 {
			theta := float64(n.ax) * 2 * math.Pi / 65536
			co := math.Cos(theta)
			si := math.Sin(theta)
			ny := math.Round(co*y - si*z)
			nz := math.Round(si*y + co*z)
			y, z = ny, nz
		}
		if n.ay != 0 {
			theta := float64(n.ay) * 2 * math.Pi / 65536
			co := math.Cos(theta)
			si := math.Sin(theta)
			nx := math.Round(co*x - si*z)
			nz := math.Round(si*x + co*z)
			x, z = nx, nz
		}
		x += float64(n.t[0].Raw())
		y += float64(n.t[1].Raw())
		z += float64(n.t[2].Raw())
	}
	return [3]numeric.Fixed{numeric.Fixed(int64(x)), numeric.Fixed(int64(y)), numeric.Fixed(int64(z))}
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

// drawFeatureModel draws a 3D feature model at its world position [fmt 3do][03 §2.4].
func (c *Client) drawFeatureModel(f snapshot.FeatureView) bool {
	if f.Model == "" {
		return false
	}
	m := c.unitModelFor(f.Model)
	if m == nil || c.cam == nil {
		return false
	}
	n := len(m.pieces)
	states := make([]pieceState, n)
	ux, uy, uz := int32(f.X>>16), int32(f.Y>>16), int32(f.Z>>16)
	var tris []screenTri
	for pi, piece := range m.pieces {
		worldVerts := make([][3]numeric.Fixed, len(piece.vertices))
		modelVertsF := make([][3]float64, len(piece.vertices))
		for vi, lv := range piece.vertices {
			mp := c.applyPiece(m, pi, lv, states)
			worldVerts[vi] = [3]numeric.Fixed{mp[0].Add(numeric.Fixed(int64(ux) << 16)), mp[1].Add(numeric.Fixed(int64(uy) << 16)), mp[2].Add(numeric.Fixed(int64(uz) << 16))}
			modelVertsF[vi] = [3]float64{float64(mp[0].Raw()) / 65536, float64(mp[1].Raw()) / 65536, float64(mp[2].Raw()) / 65536}
		}
		normAcc := make([][3]float64, len(piece.vertices))
		normCnt := make([]int, len(piece.vertices))
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue
			}
			np := len(pr.indices)
			if np < 3 {
				continue
			}
			if !pr.hasTex && np != 4 {
				continue
			}
			if np >= 3 && len(pr.indices) >= 3 {
				aIdx := int(pr.indices[0])
				bIdx := int(pr.indices[1])
				cIdx := int(pr.indices[2])
				if aIdx < len(modelVertsF) && bIdx < len(modelVertsF) && cIdx < len(modelVertsF) {
					av, bv, cv := modelVertsF[aIdx], modelVertsF[bIdx], modelVertsF[cIdx]
					ax, ay, az := bv[0]-av[0], bv[1]-av[1], bv[2]-av[2]
					bx, by, bz := bv[0]-cv[0], bv[1]-cv[1], bv[2]-cv[2]
					nx := ay*bz - az*by
					ny := az*bx - ax*bz
					nz := ax*by - ay*bx
					if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
						nx, ny, nz = nx/l, ny/l, nz/l
					} else {
						nx, ny, nz = 0, 1, 0
					}
					for _, vi := range pr.indices {
						if int(vi) < len(normAcc) {
							normAcc[vi][0] += nx
							normAcc[vi][1] += ny
							normAcc[vi][2] += nz
							normCnt[vi]++
						}
					}
				}
			}
		}
		vertRows := make([]int, len(piece.vertices))
		for vi := range piece.vertices {
			cnt := normCnt[vi]
			if cnt == 0 {
				vertRows[vi] = 15
				continue
			}
			nx := normAcc[vi][0] / float64(cnt)
			ny := normAcc[vi][1] / float64(cnt)
			nz := normAcc[vi][2] / float64(cnt)
			d := nx*lightDir[0] + ny*lightDir[1] + nz*lightDir[2]
			vertRows[vi] = int(d*5.0) & 31
		}
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue
			}
			np := len(pr.indices)
			if np < 3 {
				continue
			}
			if !pr.hasTex && np != 4 {
				continue
			}
			var frame *formats.GAFFrame
			var entry *formats.GAFEntry
			isTeam := false
			if pr.hasTex {
				switch pr.ref.kind {
				case texAnimated:
					frame = c.modelAnimatedFrame(pr.ref, 1, featurePresentationID(f))
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
				}
				if frame == nil {
					continue
				}
			}
			uvs := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
			ngonUV := np > 4
			ua, ub := 0, 2
			var aMin, aSpan, bMin, bSpan float64
			if ngonUV {
				var lo, hi [3]float64
				first := true
				for _, vi := range pr.indices {
					if int(vi) >= len(modelVertsF) {
						continue
					}
					mv := modelVertsF[vi]
					if first {
						lo, hi = mv, mv
						first = false
						continue
					}
					for a := 0; a < 3; a++ {
						if mv[a] < lo[a] {
							lo[a] = mv[a]
						}
						if mv[a] > hi[a] {
							hi[a] = mv[a]
						}
					}
				}
				var ext [3]float64
				for a := 0; a < 3; a++ {
					ext[a] = hi[a] - lo[a]
				}
				ua, ub = 0, 1
				if ext[1] >= ext[0] && ext[1] >= ext[2] {
					ua, ub = 1, 2
				} else if ext[2] >= ext[0] && ext[2] >= ext[1] {
					ua, ub = 0, 2
				}
				if ext[ua] > ext[ub] {
					ua, ub = ub, ua
				}
				aMin, aSpan = lo[ua], ext[ua]
				bMin, bSpan = lo[ub], ext[ub]
				if aSpan == 0 {
					aSpan = 1
				}
				if bSpan == 0 {
					bSpan = 1
				}
			}
			for k := 1; k+1 < np; k++ {
				idx0 := int(pr.indices[0])
				idx1 := int(pr.indices[k])
				idx2 := int(pr.indices[k+1])
				if idx0 >= len(worldVerts) || idx1 >= len(worldVerts) || idx2 >= len(worldVerts) {
					continue
				}
				var st screenTri
				st.color = pr.color
				st.order = pr.order
				st.row = [3]float64{float64(vertRows[idx0]), float64(vertRows[idx1]), float64(vertRows[idx2])}
				if isTeam {
					st.entry = entry
					st.team = true
				}
				if pr.hasTex {
					st.frame = frame
					uvFor := func(vi, cidx int) [2]float64 {
						if ngonUV {
							mv := modelVertsF[vi]
							return [2]float64{(mv[ua] - aMin) / aSpan, (mv[ub] - bMin) / bSpan}
						}
						return uvs[cidx]
					}
					st.u[0], st.v[0] = uvFor(idx0, 0)[0], uvFor(idx0, 0)[1]
					st.u[1], st.v[1] = uvFor(idx1, k)[0], uvFor(idx1, k)[1]
					st.u[2], st.v[2] = uvFor(idx2, k+1)[0], uvFor(idx2, k+1)[1]
				}
				for i, vi := range []int{idx0, idx1, idx2} {
					wv := worldVerts[vi]
					wx := int32(wv[0] >> 16)
					wy := int32(wv[1] >> 16)
					wz := int32(wv[2] >> 16)
					px := wx - c.cam.X
					py := wz - (wy >> 1) - c.cam.Z
					st.x[i], st.y[i] = px, py
					st.depth += py
				}
				st.depth /= 3
				tris = append(tris, st)
			}
		}
	}
	for _, t := range tris {
		if t.frame != nil {
			frame := t.frame
			if frame != nil {
				c.blitTexturedTri(&t, frame)
				continue
			}
		}
		if f.IsSinking {
			c.fillTriNanoframe(&t, t.color)
			continue
		}
		c.fillTri(&t, t.color)
	}
	return true
}

// drawProjectileModel draws a projectile's 3DO model with yaw/pitch offsets [03 §5.2].
func (c *Client) drawProjectileModel(p snapshot.ProjectileView, alpha float32) bool {
	if p.Model == "" {
		return false
	}
	m := c.unitModelFor(p.Model)
	if m == nil || c.cam == nil {
		return false
	}
	n := len(m.pieces)
	states := make([]pieceState, n)
	root := -1
	for i, pc := range m.pieces {
		if pc.parent == -1 {
			root = i
			break
		}
	}
	if root >= 0 {
		states[root].rotY += p.Yaw + 0x8000
		states[root].rotX += p.Pitch + 0x8000
	}
	x, y, z := int32(p.X>>16), int32(p.Y>>16), int32(p.Z>>16)
	var tris []screenTri
	for pi, piece := range m.pieces {
		worldVerts := make([][3]numeric.Fixed, len(piece.vertices))
		modelVertsF := make([][3]float64, len(piece.vertices))
		for vi, lv := range piece.vertices {
			mp := c.applyPiece(m, pi, lv, states)
			worldVerts[vi] = [3]numeric.Fixed{mp[0].Add(numeric.Fixed(int64(x) << 16)), mp[1].Add(numeric.Fixed(int64(y) << 16)), mp[2].Add(numeric.Fixed(int64(z) << 16))}
			modelVertsF[vi] = [3]float64{float64(mp[0].Raw()) / 65536, float64(mp[1].Raw()) / 65536, float64(mp[2].Raw()) / 65536}
		}
		normAcc := make([][3]float64, len(piece.vertices))
		normCnt := make([]int, len(piece.vertices))
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue
			}
			np := len(pr.indices)
			if np < 3 {
				continue
			}
			if !pr.hasTex && np != 4 {
				continue
			}
			if np >= 3 {
				aIdx := int(pr.indices[0])
				bIdx := int(pr.indices[1])
				cIdx := int(pr.indices[2])
				if aIdx < len(modelVertsF) && bIdx < len(modelVertsF) && cIdx < len(modelVertsF) {
					av, bv, cv := modelVertsF[aIdx], modelVertsF[bIdx], modelVertsF[cIdx]
					ax, ay, az := bv[0]-av[0], bv[1]-av[1], bv[2]-av[2]
					bx, by, bz := bv[0]-cv[0], bv[1]-cv[1], bv[2]-cv[2]
					nx := ay*bz - az*by
					ny := az*bx - ax*bz
					nz := ax*by - ay*bx
					if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
						nx, ny, nz = nx/l, ny/l, nz/l
					} else {
						nx, ny, nz = 0, 1, 0
					}
					for _, vi := range pr.indices {
						if int(vi) < len(normAcc) {
							normAcc[vi][0] += nx
							normAcc[vi][1] += ny
							normAcc[vi][2] += nz
							normCnt[vi]++
						}
					}
				}
			}
		}
		vertRows := make([]int, len(piece.vertices))
		for vi := range piece.vertices {
			cnt := normCnt[vi]
			if cnt == 0 {
				vertRows[vi] = 15
				continue
			}
			nx := normAcc[vi][0] / float64(cnt)
			ny := normAcc[vi][1] / float64(cnt)
			nz := normAcc[vi][2] / float64(cnt)
			d := nx*lightDir[0] + ny*lightDir[1] + nz*lightDir[2]
			vertRows[vi] = int(d*5.0) & 31
		}
		for _, pr := range piece.prims {
			if pr.isSelection {
				continue
			}
			np := len(pr.indices)
			if np < 3 || (!pr.hasTex && np != 4) {
				continue
			}
			var frame *formats.GAFFrame
			var entry *formats.GAFEntry
			isTeam := false
			if pr.hasTex {
				switch pr.ref.kind {
				case texAnimated:
					frame = c.modelAnimatedFrame(pr.ref, 2, projectilePresentationID(p))
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
				}
				if frame == nil {
					continue
				}
			}
			uvs := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
			ngonUV := np > 4
			ua, ub := 0, 2
			var aMin, aSpan, bMin, bSpan float64
			if ngonUV {
				var lo, hi [3]float64
				first := true
				for _, vi := range pr.indices {
					if int(vi) >= len(modelVertsF) {
						continue
					}
					mv := modelVertsF[vi]
					if first {
						lo, hi = mv, mv
						first = false
						continue
					}
					for a := 0; a < 3; a++ {
						if mv[a] < lo[a] {
							lo[a] = mv[a]
						}
						if mv[a] > hi[a] {
							hi[a] = mv[a]
						}
					}
				}
				var ext [3]float64
				for a := 0; a < 3; a++ {
					ext[a] = hi[a] - lo[a]
				}
				ua, ub = 0, 1
				if ext[1] >= ext[0] && ext[1] >= ext[2] {
					ua, ub = 1, 2
				} else if ext[2] >= ext[0] && ext[2] >= ext[1] {
					ua, ub = 0, 2
				}
				if ext[ua] > ext[ub] {
					ua, ub = ub, ua
				}
				aMin, aSpan = lo[ua], ext[ua]
				bMin, bSpan = lo[ub], ext[ub]
				if aSpan == 0 {
					aSpan = 1
				}
				if bSpan == 0 {
					bSpan = 1
				}
			}
			for k := 1; k+1 < np; k++ {
				idx0 := int(pr.indices[0])
				idx1 := int(pr.indices[k])
				idx2 := int(pr.indices[k+1])
				if idx0 >= len(worldVerts) || idx1 >= len(worldVerts) || idx2 >= len(worldVerts) {
					continue
				}
				var st screenTri
				st.color = pr.color
				st.order = pr.order
				st.row = [3]float64{float64(vertRows[idx0]), float64(vertRows[idx1]), float64(vertRows[idx2])}
				if isTeam {
					st.entry = entry
					st.team = true
				}
				if pr.hasTex {
					st.frame = frame
					uvFor := func(vi, cidx int) [2]float64 {
						if ngonUV {
							mv := modelVertsF[vi]
							return [2]float64{(mv[ua] - aMin) / aSpan, (mv[ub] - bMin) / bSpan}
						}
						return uvs[cidx]
					}
					st.u[0], st.v[0] = uvFor(idx0, 0)[0], uvFor(idx0, 0)[1]
					st.u[1], st.v[1] = uvFor(idx1, k)[0], uvFor(idx1, k)[1]
					st.u[2], st.v[2] = uvFor(idx2, k+1)[0], uvFor(idx2, k+1)[1]
				}
				for i, vi := range []int{idx0, idx1, idx2} {
					wv := worldVerts[vi]
					wx := int32(wv[0] >> 16)
					wy := int32(wv[1] >> 16)
					wz := int32(wv[2] >> 16)
					px := wx - c.cam.X
					py := wz - (wy >> 1) - c.cam.Z
					st.x[i], st.y[i] = px, py
					st.depth += py
				}
				st.depth /= 3
				tris = append(tris, st)
			}
		}
	}
	for _, t := range tris {
		if t.frame != nil {
			frame := t.frame
			if frame != nil {
				c.blitTexturedTri(&t, frame)
				continue
			}
		}
		c.fillTri(&t, t.color)
	}
	return true
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
func (c *Client) drawUnitChrome(v snapshot.UnitView, sx, sy int32) {
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
