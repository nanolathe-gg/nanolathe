package client

// Software 3DO model presentation [fmt 3do][03 §2.5][decompile rendering §3/§5].
//
// Presentation only (I6): this file never touches sim state. It expands a
// loaded ThreeDO into world-space triangles once (fan triangulation per
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// trig is on the I2 float allowlist), projects through the orthographic
// half-shear [03 §2.5], and rasterizes with painter's order.
//
// Face shading follows retail exactly on the two points the decompile pins:
// flat-colored primitives keep their resolved palette color across
// orientations (no SHD), and only texture pixels would route through SHD —
// the SHD row-selection math is undocumented (TODO(question)), so textured
// faces currently sample unshaded.

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
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

type unitModel struct {
	tris []modelTri
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
	if c.modelFS == nil || name == "" {
		return nil
	}
	if m, ok := c.models[name]; ok {
		return m
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
	um := &unitModel{}
	order := 0
	// Per-vertex smooth normals: each face's normalized normal accumulates
	// onto its vertices; the SHD row derives from the averaged normal
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// touch count]. Keyed by piece+vertex index.
	type vkey struct {
		piece int32
		vi    int32
	}
	normAcc := map[vkey][3]float64{}
	normCnt := map[vkey]int{}
	var walk func(obj int32, tx, ty, tz int64)
	walk = func(obj int32, tx, ty, tz int64) {
		if obj < 0 || int(obj) >= len(m3.Objects) {
			return
		}
		o := &m3.Objects[obj]
		// Half-turn about the vertical axis [03 §2.4]: negate X and Z of the
		// parent translation (vertex coordinates negate at use below).
		tx -= int64(o.Translation[0])
		ty += int64(o.Translation[1])
		tz -= int64(o.Translation[2])
		wx, wy, wz := float64(tx)/65536, float64(ty)/65536, float64(tz)/65536

		// Load-time primitive reordering on a local copy [03 §2.4]: the
		// declared selection primitive swaps to index 0, the remainder sorts
		// ascending by integer mean vertex Y, and draw order is fixed here.
		prims := make([]*formats.ThreeDOPrimitive, len(o.Primitives))
		for i := range o.Primitives {
			prims[i] = &o.Primitives[i]
		}
		sel := int32(-1)
		if o.Selection >= 0 && o.Selection < int32(len(prims)) {
			prims[0], prims[o.Selection] = prims[o.Selection], prims[0]
			sel = 0 // after the swap the plate lives at index 0 [03 §2.4]
		}
		if len(prims) > 1 {
			rest := prims[1:]
			sort.SliceStable(rest, func(a, b int) bool {
				return primMeanY(o, rest[a]) < primMeanY(o, rest[b])
			})
		}

		// Retail unit scripts hide the muzzle-flash/flare pieces in Create()
		// [fmt 3do "Piece naming conventions": flare pieces are emit points
		// shown by Fire callbacks]; until the COB VM drives creation we keep
		// their quads out of the draw set. TODO(T23): drop when scripts run.
		if strings.Contains(strings.ToLower(o.Name), "flash") ||
			strings.Contains(strings.ToLower(o.Name), "flare") {
			return
		}
		for pi, p := range prims {
			n := len(p.VertexIndices)
			if n < 3 {
				continue // emit points and degenerate faces draw nothing
			}
			// The selection plate is not a model face: retail's selection
			// render/pick behavior beyond the load-time swap is an open gap
			// [03 "Residuals" selection-primitive rendering; picking is a 2D
			// bbox per decompile wave_d §3.2], and our chrome draws brackets.
			// TODO(question): confirm whether retail blits the plate in the
			// unit pass (it would read as the ground shadow).
			if int32(pi) == sel {
				continue
			}
			var tri modelTri
			tri.piece = o.Name
			tri.color = uint8(p.ColorIndex)
			tri.order = order
			order++
			// Retail resolves the texture at load; a miss REWRITES the
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// subject to the quads-only rule), not a skipped face.
			textured := false
			if p.TextureName != "" {
				ref, ok := c.texIndex[strings.ToLower(p.TextureName)]
				if !ok {
					tri.color = 0xd1
				} else {
					tri.hasTex = true
					tri.ref = ref
					textured = true
				}
			}
			// Flat-colored (untextured) faces draw ONLY as quads — the
			// retail rasterizer rejects non-quad untextured primitives
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// The textured test runs after resolution: a texture miss
			// rewrote the primitive to flat color 0xd1 above.
			if !textured && n != 4 {
				continue
			}
			// Quad corners map in index order to (0,0),(1,0),(1,1),(0,1)
			// [fmt 3do "Texturing"]. The retail rule for larger n-gons is
			// unspecified (TODO(question)); they are planar, so UVs come
			// from a bounding-box projection on the face's two largest
			// axes — the implied mapping TA authoring tools used.
			uvs := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
			ngonUV := n > 4
			ua, ub := 0, 2
			var aMin, aSpan, bMin, bSpan float64
			if ngonUV {
				var lo, hi [3]float64
				first := true
				for k := 0; k < n; k++ {
					vi := int(p.VertexIndices[k])
					if vi < 0 || vi >= len(o.Vertices) {
						continue
					}
					v := o.Vertices[vi]
					vv := [3]float64{
						-(float64(v.X) / 65536), // half-turn negates X [03 §2.4]
						float64(v.Y) / 65536,
						-(float64(v.Z) / 65536), // and Z
					}
					if first {
						lo, hi = vv, vv
						first = false
						continue
					}
					for a := 0; a < 3; a++ {
						if vv[a] < lo[a] {
							lo[a] = vv[a]
						}
						if vv[a] > hi[a] {
							hi[a] = vv[a]
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
			corner := func(k int) modelCorner {
				vi := int(p.VertexIndices[k])
				vx, vy, vz := float64(0), float64(0), float64(0)
				if vi >= 0 && vi < len(o.Vertices) {
					// Half-turn negates first and third coordinates [03 §2.4].
					vx = -(float64(o.Vertices[vi].X) / 65536)
					vy = float64(o.Vertices[vi].Y) / 65536
					vz = -(float64(o.Vertices[vi].Z) / 65536)
				}
				uv := [2]float64{0, 0}
				if ngonUV {
					pos := [3]float64{vx, vy, vz}
					uv[0] = (pos[ua] - aMin) / aSpan
					uv[1] = (pos[ub] - bMin) / bSpan
				} else {
					uv = uvs[k]
				}
				return modelCorner{x: wx + vx, y: wy + vy, z: wz + vz, u: uv[0], v: uv[1]}
			}
			// Face normal from the polygon's first three vertices; degenerate
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			nx, ny, nz := 0.0, 1.0, 0.0
			if n >= 3 {
				// Retail face normal: cross(v[b]-v[a], v[b]-v[c]) over the
				// TODO(question): Historical analysis omitted; independently worded behavior is needed.
				// TODO(question): Historical analysis omitted; independently worded behavior is needed.
				v0 := corner(0)
				v1 := corner(1)
				v2 := corner(2)
				ax, ay, az := v1.x-v0.x, v1.y-v0.y, v1.z-v0.z
				bx, by, bz := v1.x-v2.x, v1.y-v2.y, v1.z-v2.z
				nx = ay*bz - az*by
				ny = az*bx - ax*bz
				nz = ax*by - ay*bx
				if l := sqrt3(nx*nx + ny*ny + nz*nz); l > 1e-9 {
					nx, ny, nz = nx/l, ny/l, nz/l
				} else {
					nx, ny, nz = 0, 1, 0
				}
			}
			for k := 0; k < n; k++ {
				vi := int32(p.VertexIndices[k])
				key := vkey{piece: obj, vi: vi}
				acc := normAcc[key]
				normAcc[key] = [3]float64{acc[0] + nx, acc[1] + ny, acc[2] + nz}
				normCnt[key]++
			}
			c0 := corner(0)
			for k := 1; k+1 < n; k++ {
				tri.c[0] = c0
				tri.c[1] = corner(k)
				tri.c[2] = corner(k + 1)
				tri.vkey[0] = int64(obj)<<32 | int64(p.VertexIndices[0])
				tri.vkey[1] = int64(obj)<<32 | int64(p.VertexIndices[k])
				tri.vkey[2] = int64(obj)<<32 | int64(p.VertexIndices[k+1])
				um.tris = append(um.tris, tri)
			}
		}
		if o.FirstChild >= 0 {
			walk(o.FirstChild, tx, ty, tz)
		}
		if o.NextSibling >= 0 {
			walk(o.NextSibling, tx, ty, tz)
		}
	}
	walk(m3.Root, 0, 0, 0)
	// Row resolution runs after the full walk: a vertex's normal is the
	// average of every face touching it, so rows are order-independent
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	for ti := range um.tris {
		for kk := 0; kk < 3; kk++ {
			key := vkey{piece: int32(um.tris[ti].vkey[kk] >> 32), vi: int32(um.tris[ti].vkey[kk] & 0xffffffff)}
			acc := normAcc[key]
			cnt := normCnt[key]
			if cnt == 0 {
				cnt = 1
			}
			nx, ny, nz := acc[0]/float64(cnt), acc[1]/float64(cnt), acc[2]/float64(cnt)
			if l := sqrt3(nx*nx + ny*ny + nz*nz); l > 1e-9 {
				nx, ny, nz = nx/l, ny/l, nz/l
			}
			// row = ftol(dot(N, L) * 5) & 31 with the shipped default light
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// fmul 0x4fd4cc=5.0, __ftol, and 0x1f].
			d := nx*lightDir[0] + ny*lightDir[1] + nz*lightDir[2]
			um.tris[ti].row[kk] = int(d*5.0) & 31
		}
	}
	if len(um.tris) == 0 {
		return nil
	}
	return um
}

// primMeanY is the integer mean of a primitive's vertices' second coordinate
// in source 16.16 units, the load-time sort key [03 §2.4].
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
func (c *Client) drawUnitModel(v snapshot.UnitView, sx, sy int32) bool {
	m := c.unitModelFor(v.Model)
	if m == nil || c.cam == nil {
		return false
	}
	cos, sin := headingCosSin(v.Heading)
	ux, uy, uz := int32(v.X>>16), int32(v.Y>>16), int32(v.Z>>16)
	tris := make([]screenTri, 0, len(m.tris))
	for i := range m.tris {
		t := &m.tris[i]
		var st screenTri
		st.color = t.color
		st.order = t.order
		st.row = [3]float64{float64(t.row[0]), float64(t.row[1]), float64(t.row[2])}
		if t.hasTex {
			switch t.ref.kind {
			case texAnimated:
				st.frame = animatedFrame(t.ref, c.animClock)
			case texTeam:
				st.frame = t.ref.frame
				st.entry = t.ref.entry
				st.team = true
			default:
				st.frame = t.ref.frame
			}
		}
		for k := 0; k < 3; k++ {
			cn := &t.c[k]
			// Heading rotates the model X/Z plane; Y stays up. Same
			// convention as the footprint body path.
			rx := cos*cn.x - sin*cn.z
			rz := sin*cn.x + cos*cn.z
			wx := ux + int32(rx)
			wy := uy + int32(cn.y)
			wz := uz + int32(rz)
			// Orthographic half-shear [03 §2.5], same as Camera.WorldToScreen
			// but in integer pixels without fixed-point round trips.
			px := wx - c.cam.X + camera.OriginX
			py := wz - (wy >> 1) - c.cam.Z + camera.OriginY
			st.x[k], st.y[k] = px, py
			st.u[k], st.v[k] = cn.u, cn.v
			st.depth += py
		}
		st.depth /= 3
		tris = append(tris, st)
	}
	// No per-frame sort and no backface cull: retail fixes draw order at
	// load (per-piece Y-mean sort, tree traversal across pieces) and draws
	// faces double-sided in the 8-bit path [03 §2.4][decompile wave_d §3.2].
	owner := int(v.Owner) % 10
	for i := range tris {
		t := &tris[i]
		if t.frame != nil {
			frame := t.frame
			if t.team && t.entry != nil && owner < len(t.entry.Frames) {
				frame = t.entry.Frames[owner].Frame // frame n = player n
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

// fillTri rasterizes a flat-colored triangle with an even-odd sign test,
// clipped to the framebuffer.
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
