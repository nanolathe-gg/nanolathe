package client

// Software 3DO model presentation [fmt 3do][03 §2.5][decompile rendering §3/§5].
//
// Presentation only (I6): this file never touches sim state. It expands a
// loaded ThreeDO into world-space triangles once (fan triangulation per
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// the shipped default].
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
	row    [3]int   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	frame *formats.GAFFrame // static frame (kind static)
	entry *formats.GAFEntry // team/animated: full frame list
	cum   []int             // animated: cumulative delay ticks per frame
	total int               // animated: full-cycle length in ticks
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
	c.animClock += n
}

// animatedFrame picks the current frame of an animated entry from the clock,
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func animatedFrame(ref texRef, clock int) *formats.GAFFrame {
	if ref.total <= 0 || len(ref.cum) == 0 {
		return ref.frame
	}
	t := ((clock % ref.total) + ref.total) % ref.total
	for i, c := range ref.cum {
		if t < c {
			return ref.entry.Frames[i].Frame
		}
	}
	return ref.entry.Frames[len(ref.entry.Frames)-1].Frame
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
		for i := range g.Entries {
			entry := &g.Entries[i]
			if len(entry.Frames) == 0 || entry.Frames[0].Frame == nil {
				continue
			}
			ref := texRef{frame: entry.Frames[0].Frame, entry: entry}
			switch {
			case len(entry.Frames) == 10:
				ref.kind = texTeam // frame n = player n, never animated
			case len(entry.Frames) > 1:
				ref.kind = texAnimated
				total := 0
				for _, fr := range entry.Frames {
					d := int(fr.Value)
					if d < 1 {
						d = 1 // zero delay would flicker every tick
					}
					total += d
					ref.cum = append(ref.cum, total)
				}
				ref.total = total
			}
			c.texIndex[strings.ToLower(entry.Name)] = ref
		}
	}
}

// unitModelFor expands and caches a model; nil when the 3DO is unavailable
// (callers fall back to footprint bodies).
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
	c.models[name] = m // nil caches too: missing models stay fallback
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
// no backface cull exists in retail's composer [decompile wave_d §3.2].
// This version stores the hierarchy for dynamic piece transforms [03 §2.4] C21–C22.
func (c *Client) expandModel(name string) *unitModel {
	data, err := c.modelFS.ReadFile("objects3d/" + name + ".3do")
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: model %s: read: %v\n", name, err)
		return nil
	}
	m3, err := formats.LoadThreeDO(data)
	if err != nil || len(m3.Objects) == 0 {
		fmt.Fprintf(os.Stderr, "nanolathe: model %s: parse: %v\n", name, err)
		return nil
	}
	um := &unitModel{
		pieces:      make([]pieceModel, len(m3.Objects)),
		pieceByName: map[string]int{},
	}
	order := 0
	// Build per-piece authored data after half-turn [03 §2.4].
	for i, o := range m3.Objects {
		p := &um.pieces[i]
		p.name = o.Name
		p.parent = int(o.Parent)
		// half-turn translation [03 §2.4]
		tx := numeric.Fixed(o.Translation[0])
		ty := numeric.Fixed(o.Translation[1])
		tz := numeric.Fixed(o.Translation[2])
		tx = -tx
		tz = -tz
		p.translate = [3]numeric.Fixed{tx, ty, tz}
		// half-turn vertices [03 §2.4]
		p.vertices = make([][3]numeric.Fixed, len(o.Vertices))
		for j, v := range o.Vertices {
			x := numeric.Fixed(v.X)
			y := numeric.Fixed(v.Y)
			z := numeric.Fixed(v.Z)
			x = -x
			z = -z
			p.vertices[j] = [3]numeric.Fixed{x, y, z}
		}
		um.pieceByName[strings.ToLower(o.Name)] = i
		// Load-time primitive reorder [03 §2.4]: selection swap + bubble sort by mean Y
		prims := make([]*formats.ThreeDOPrimitive, len(o.Primitives))
		for j := range o.Primitives {
			prims[j] = &o.Primitives[j]
		}
		sel := int32(-1)
		if o.Selection >= 0 && o.Selection < int32(len(prims)) {
			prims[0], prims[o.Selection] = prims[o.Selection], prims[0]
			sel = 0
		}
		if len(prims) > 1 {
			// Bubble sort from 1 upward to preserve retail tie order [03 §2.4] (formats uses bubble)
			for end := len(prims) - 1; end > 1; end-- {
				swapped := false
				for j := 1; j < end; j++ {
					if primMeanY(&m3.Objects[i], prims[j+1]) < primMeanY(&m3.Objects[i], prims[j]) {
						prims[j], prims[j+1] = prims[j+1], prims[j]
						swapped = true
					}
				}
				if !swapped {
					break
				}
			}
		}
		if sel == 0 {
			p.hasSelection = true
		}
		for pi, pp := range prims {
			isSel := int32(pi) == sel
			hasTex := false
			var ref texRef
			color := uint8(pp.ColorIndex)
			if pp.TextureName != "" {
				if r, ok := c.texIndex[strings.ToLower(pp.TextureName)]; ok {
					hasTex = true
					ref = r
				} else {
					color = 0xd1 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
				isSelection: isSel,
			})
			order++
			_ = isSel
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
	// Legacy flat tris kept empty; new path uses pieces. Keep for fallback if needed.
	um.tris = nil
	if len(um.pieces) == 0 {
		return nil
	}
	return um
}

func primMeanY(o *formats.ThreeDOObject, p *formats.ThreeDOPrimitive) int64 {
	if len(p.VertexIndices) == 0 {
		return 0
	}
	var sum int64
	for _, vi := range p.VertexIndices {
		if int(vi) < len(o.Vertices) {
			sum += int64(o.Vertices[vi].Y)
		}
	}
	return sum / int64(len(p.VertexIndices))
}

type screenTri struct {
	x, y  [3]int32
	u, v  [3]float64
	row   [3]float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	color uint8
	frame *formats.GAFFrame
	entry *formats.GAFEntry
	team  bool
	order int
	depth int32 // mean screen y for painter order
}

// drawUnitModel projects and rasterizes the unit's model; false when no
// model is available (caller draws the footprint body fallback).
// It uses hierarchical piece transforms via VM.Pieces [03 §2.4] C21–C22, heading
// folded into root [03 §2.4] C24, SHD row = trunc(dot*5) mod 32 with
// per-vertex averaged normals and dont-shade pin 15 [03 §2.4.1], flat quads
// only and textured any count [03 §2.4.1], selection plate never draws
// (primitive loop starts at 1) [03 §2.4.1], team LOGOS 10 frames per-owner
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// nanoframe presentation [03 §5.7][05 "Construction target state"].
func (c *Client) drawUnitModelDirect(v snapshot.UnitView, sx, sy int32) bool {
	m := c.unitModelFor(v.Model)
	if m == nil || c.cam == nil || len(m.pieces) == 0 {
		// Model-load fallback diagnostic emitted once per unit slot (structured), not per frame spam [ON-08].
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
				fmt.Fprintf(os.Stderr, "{\"level\":\"warn\",\"msg\":\"model fallback\",\"slot\":%d,\"model\":%q,\"owner\":%d,\"error\":%q}\n", v.Slot, v.Model, v.Owner, err.Error())
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
	// Ground height for shadow projection [03 §5.3]; fallback to unit Y.
	groundY := uy
	_ = groundY // shadow ground height used for projection [03 §5.3]
	if c.terrain != nil {
		if h := c.terrain.HeightAt(v.X, v.Z); h != numeric.Fixed(-1) {
			groundY = int32(h >> 16)
		}
	}
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
			modelPos := c.applyChain(lv, chain)
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
			if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
				nx, ny, nz = nx/l, ny/l, nz/l
			} else {
				nx, ny, nz = 0, 1, 0
			}
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
					frame = animatedFrame(pr.ref, c.animClock)
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
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
				// Shadow handling: if not dontShadow and not nanoframe, also queue shadow tri
				// Shadow is projected onto groundY with same X/Z [03 §5.3]
				if !states[pi].dontShadow && !isNanoframe {
					// Shadow tri will be drawn before model with dark index; we emit shadow tris into separate list?
					// For simplicity, draw shadow immediately with dark color using same geometry but Y=groundY
					// We defer shadow drawing to after tris collection to ensure it is underneath.
				}
				tris = append(tris, st)
			}
		}
	}
	// No per-frame sort: load-fixed order is painter order [03 §2.4]; also Y-bucket already in composer.
	// Draw shadows first (dark, underneath) [03 §5.3]
	if !isNanoframe {
		for i := range tris {
			t := &tris[i]
			// Shadow is same triangle but projected onto ground: use groundY for wy
			// We approximate by offsetting py by (uy - groundY)>>1 shear difference?
			// Instead reproject shadow vertices: shadowY = groundY, so pyShadow = wz - (groundY>>1) - cam.Z
			// Our t already has py = wz - (wy>>1) - camZ. So shadow py = py + ((wy - groundY)>>1)
			// For flat ground, shadow is slightly below model. Use dark palette index via Shade row 0 or palette 0.
			shadow := *t
			// Darken: use flat color 0 for shadow if textured, else keep? For textured we will fill with dark via palette.
			// For shadow, we draw with palette index 0 (black) at low opacity approximated by stipple.
			// To avoid heavy per-pixel, just draw same tri with color 1 (near-black) if flat, or with Shade row 0 if textured.
			if shadow.frame != nil {
				// Textured shadow: use row 0 (dark) [03 §4.3] row 0 near-black
				shadow.row = [3]float64{0, 0, 0}
				// Keep frame but will be shaded dark via Shade[0]
			} else {
				shadow.color = 1 // near-black [03 §4.3] SHD row 0 approx
			}
			// Offset shadow slightly south-east to mimic light direction (-0.8,1,0.25) -> shadow offset? Simple offset (2,2)
			for k := 0; k < 3; k++ {
				shadow.x[k] += 2
				shadow.y[k] += 2
			}
			if shadow.frame != nil {
				sFrame := shadow.frame
				if shadow.team && shadow.entry != nil && owner < len(shadow.entry.Frames) {
					sFrame = shadow.entry.Frames[owner].Frame
				}
				if sFrame != nil {
					c.blitTexturedTri(&shadow, sFrame)
					continue
				}
			}
			c.fillTri(&shadow, shadow.color)
		}
	}
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// fixed buildings (IsBuilding) take the 2x supersampled path with per-piece
// dont-shade [03 §2.4.1][04 §4.3] 0x1000e000, mobile units take the 1x cached
// path with no shading and then both blit in Y-bucket order [03 §1].
// Offscreen caches are per-unit and then blitted, matching retail's
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Client) drawUnitModel(v snapshot.UnitView, sx, sy int32) bool {
	if v.IsBuilding {
		return c.drawBuildingModelOffscreen(v)
	}
	return c.drawMobileModelOffscreen(v)
}

// collectUnitTris builds the triangle list for v at 1x shell coords.
// useShade true respects per-piece DontShade (buildings); false forces row 15 (units) [03 §2.4.1].
func (c *Client) collectUnitTris(v snapshot.UnitView, useShade bool) ([]screenTri, int32, int32, int32, int32) {
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
				fmt.Fprintf(os.Stderr, "{\"level\":\"warn\",\"msg\":\"model fallback\",\"slot\":%d,\"model\":%q,\"owner\":%d,\"error\":%q}\n", v.Slot, v.Model, v.Owner, err.Error())
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
			mp := c.applyChain(lv, chain)
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
			if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
				nx, ny, nz = nx/l, ny/l, nz/l
			} else {
				nx, ny, nz = 0, 1, 0
			}
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
					frame = animatedFrame(pr.ref, c.animClock)
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// no SHD at all — decompile two-normal probe]. Transparent texels skip.
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
		chain := c.buildPieceChain(m, pi, states)
		worldVerts := make([][3]numeric.Fixed, len(piece.vertices))
		modelVertsF := make([][3]float64, len(piece.vertices))
		for vi, lv := range piece.vertices {
			mp := c.applyChain(lv, chain)
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
			if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
				nx, ny, nz = nx/l, ny/l, nz/l
			}
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
					frame = animatedFrame(pr.ref, c.animClock)
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
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
		chain := c.buildPieceChain(m, pi, states)
		worldVerts := make([][3]numeric.Fixed, len(piece.vertices))
		modelVertsF := make([][3]float64, len(piece.vertices))
		for vi, lv := range piece.vertices {
			mp := c.applyChain(lv, chain)
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
			if l := math.Sqrt(nx*nx + ny*ny + nz*nz); l > 1e-9 {
				nx, ny, nz = nx/l, ny/l, nz/l
			}
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
					frame = animatedFrame(pr.ref, c.animClock)
				case texTeam:
					frame = pr.ref.frame
					entry = pr.ref.entry
					isTeam = true
				default:
					frame = pr.ref.frame
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.

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
