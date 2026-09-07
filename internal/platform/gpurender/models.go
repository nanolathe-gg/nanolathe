package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"image/color"
)

// modelGPUVertex is private preparation data for the device rasterizer. The
// public packet deliberately uses integer authored corners, but a folded ring
// needs its edge attributes carried as fractions until the fragment shader
// performs the retail byte/key narrowing [03 R-RAST-01 §1].
type modelGPUVertex struct {
	X, Y  float32
	Key   float32
	U, V  float32
	Shade float32
}

type modelGPUFace struct {
	Vertices []modelGPUVertex
	Texture  *formats.GAFFrame
	Color    uint8
	Shaded   bool
}

// ModelStats is the per-Execute accounting for the experimental body path.
// It is diagnostic data for captures, never simulation state.
type ModelStats struct {
	GPU, CPUFallback, Shadows, MissingSource, NoBody     int
	UnsupportedGeometry, MissingTexture, UnsupportedFace int
	FoldedFaces, FoldedStrips                            int
	Fallbacks                                            [7]int
}

func (r *Renderer) ModelStats() ModelStats {
	if r == nil {
		return ModelStats{}
	}
	return r.modelStats
}

// The composed-model family for the modern executor. Eligible P3 packets raster
// their bodies on the device; every other body keeps the reviewed CPU bridge.
// Shadows always retain the CPU composition image and ALP commit in this first
// prototype [03 R-REN-03A §1–§8].
//
// A Model command carries only a Ref into the client-side per-frame table
// (drawlist.Model.Ref). gpurender cannot import internal/client (the client must
// stay Ebitengine-free), so the client hands the finished body and shadow images
// to the wiring layer as plain data and the wiring layer adapts them to
// ModelImage and installs a ModelSource on the renderer before each Execute. The
// renderer resolves a Ref through that source; with no source installed, a Model
// command draws nothing.

// ModelImage is one finished model composition image — a body or a shadow — in
// the neutral, uploadable form the GPU executor blits: the colour plane
// (physical palette indices), the coverage mask that keys the blit (an uncovered
// pixel is the transparent key the commit skips), the image dimensions, the
// framebuffer top-left the image blits at, and the image's transparent index.
//
// DX,DY is the classic composition image's anchorX-originX / anchorY-originY: the
// framebuffer pixel the image's own (0,0) lands on, so a keyed or tinted blit at
// (DX,DY) reproduces modelTarget.commit / tintedCommit's screenX/screenY mapping
// pixel for pixel [R-REN-03A §1].
type ModelImage struct {
	Color       []uint8
	Covered     []bool
	W, H        int
	DX, DY      int
	Transparent uint8
}

// ModelSource resolves a drawlist.Model.Ref to its finished body and shadow
// images (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5). The wiring layer implements it
// over the client's per-frame model table; ok is false for a Ref that records no
// such step, matching the classic sink's own body/shadow guards so the modern
// executor draws each half in precisely the cases the classic sink does.
type ModelSource interface {
	ModelBody(ref int) (ModelImage, bool)
	ModelShadow(ref int) (ModelImage, bool)
}

// SetModelSource installs the per-frame model source. The battle app and the
// modern shot path call it after recording the frame and before Execute, because
// the client-side model table the source reads is populated during recording.
func (r *Renderer) SetModelSource(src ModelSource) {
	if r == nil {
		return
	}
	r.modelSrc = src
}

// Model replays one composed model subject: the shadow first, then the body, the
// classic sink's order for the same subject (docs/DESIGN_GPU_RENDERER.md §2.1
// C-G5)[03 §5.3]. The classic sink's third step, the parity trace, is diagnostic
// only — it reads the finished surface and writes event records, never a
// framebuffer pixel — so the modern executor skips it entirely; drawing it would
// be wrong.
func (r *Renderer) Model(cmd drawlist.Model) {
	if r == nil || r.offscreen == nil {
		return
	}
	if r.modelSrc != nil {
		if shadow, ok := r.modelSrc.ModelShadow(cmd.Ref); ok {
			r.blitModelShadow(shadow)
			r.modelStats.Shadows++
		}
	}
	if g := cmd.Geometry; g != nil && g.Eligible && r.modelGeometrySupported(g) {
		r.drawModelGeometry(g)
		r.modelStats.GPU++
		return
	}
	if cmd.Geometry != nil && int(cmd.Geometry.Fallback) < len(r.modelStats.Fallbacks) {
		r.modelStats.Fallbacks[cmd.Geometry.Fallback]++
	}
	if cmd.Geometry != nil && cmd.Geometry.Fallback == drawlist.ModelFallbackNoBodyCommit {
		r.modelStats.NoBody++
		return
	}
	r.modelStats.CPUFallback++
	if r.modelSrc == nil {
		r.modelStats.MissingSource++
		return
	}
	if body, ok := r.modelSrc.ModelBody(cmd.Ref); ok {
		r.blitModelBody(body)
	} else {
		r.modelStats.MissingSource++
	}
}

func (r *Renderer) modelGeometrySupported(g *drawlist.ModelGeometry) bool {
	if r == nil || g == nil || g.Scale != 1 || len(g.Faces) == 0 || r.modelKey == nil || r.modelBody == nil || r.modelCommit == nil || r.modelKeyImage == nil || r.modelColor == nil || r.modelCoord == nil {
		return false
	}
	r.modelStats.UnsupportedFace = -1
	for i := range g.Faces {
		f := g.Faces[i]
		if polygonCrosses(f.Vertices) {
			if len(foldedStrips(f)) == 0 {
				// The established winding/two-chain admission can retain a
				// projected ring with no positive row. It contributes no body
				// pixels, just like a back-facing ring, and is not unsupported.
				continue
			}
			if !modelFaceMaterialSupported(r, f) {
				r.modelStats.MissingTexture++
				return false
			}
			continue
		}
		_, _, supported := modelFaceTriangles(f)
		if !supported {
			r.modelStats.UnsupportedGeometry++
			r.modelStats.UnsupportedFace = i
			return false
		}
		if !modelFaceMaterialSupported(r, f) {
			r.modelStats.MissingTexture++
			return false
		}
	}
	return true
}

func modelFaceMaterialSupported(r *Renderer, f drawlist.ModelFace) bool {
	return r != nil && (!f.Shaded || r.tables.shade != nil) && (f.Texture == nil || r.gafImageFor(f.Texture) != nil)
}

// modelFaceTriangles retains counter-clockwise and degenerate rings as culled
// input. Visible rings are triangulated by ears in their authored clockwise
// order. This is a conventional approximation for the GPU prototype; a crossed
// ring is rejected rather than being fan-filled into invented coverage.
func modelFaceTriangles(f drawlist.ModelFace) ([]uint16, bool, bool) {
	n := len(f.Vertices)
	if n < 3 || n > 1<<16 {
		return nil, false, n < 3
	}
	var area int64
	for i := range f.Vertices {
		a, b := f.Vertices[i], f.Vertices[(i+1)%n]
		area += int64(a.X)*int64(b.Y) - int64(a.Y)*int64(b.X)
	}
	if area <= 0 {
		return nil, false, true
	}
	if polygonCrosses(f.Vertices) {
		return nil, true, false
	}
	if n == 4 && cross(f.Vertices[0], f.Vertices[1], f.Vertices[2]) > 0 && cross(f.Vertices[0], f.Vertices[2], f.Vertices[3]) > 0 {
		return []uint16{0, 1, 2, 0, 2, 3}, true, true
	}
	remaining := make([]int, n)
	for i := range remaining {
		remaining[i] = i
	}
	// Collinear corners do not form an ear but do occur in authored projected
	// rings. Removing only the middle point preserves the enclosing boundary.
	for changed := true; changed && len(remaining) > 3; {
		changed = false
		for i := range remaining {
			a, b, c := remaining[(i+len(remaining)-1)%len(remaining)], remaining[i], remaining[(i+1)%len(remaining)]
			if cross(f.Vertices[a], f.Vertices[b], f.Vertices[c]) == 0 {
				remaining = append(remaining[:i], remaining[i+1:]...)
				changed = true
				break
			}
		}
	}
	out := make([]uint16, 0, 3*(n-2))
	for len(remaining) > 3 {
		found := false
		for i := range remaining {
			a, b, c := remaining[(i+len(remaining)-1)%len(remaining)], remaining[i], remaining[(i+1)%len(remaining)]
			if cross(f.Vertices[a], f.Vertices[b], f.Vertices[c]) <= 0 {
				continue
			}
			ear := true
			for _, p := range remaining {
				if p != a && p != b && p != c && insideTriangle(f.Vertices[p], f.Vertices[a], f.Vertices[b], f.Vertices[c]) {
					ear = false
					break
				}
			}
			if !ear {
				continue
			}
			out = append(out, uint16(a), uint16(b), uint16(c))
			remaining = append(remaining[:i], remaining[i+1:]...)
			found = true
			break
		}
		if !found {
			return nil, true, false
		}
	}
	out = append(out, uint16(remaining[0]), uint16(remaining[1]), uint16(remaining[2]))
	return out, true, true
}

func cross(a, b, c drawlist.ModelVertex) int64 {
	return (int64(b.X)-int64(a.X))*(int64(c.Y)-int64(a.Y)) - (int64(b.Y)-int64(a.Y))*(int64(c.X)-int64(a.X))
}
func insideTriangle(p, a, b, c drawlist.ModelVertex) bool {
	x, y, z := cross(a, b, p), cross(b, c, p), cross(c, a, p)
	// A point on the candidate diagonal does not occupy the ear's interior.
	// Treating it as inside can leave a valid projected ring with no ear.
	return x > 0 && y > 0 && z > 0
}
func polygonCrosses(v []drawlist.ModelVertex) bool {
	n := len(v)
	for i := 0; i < n; i++ {
		a, b := v[i], v[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j == i || (j+1)%n == i || (i+1)%n == j {
				continue
			}
			if segmentsCross(a, b, v[j], v[(j+1)%n]) {
				return true
			}
		}
	}
	return false
}
func segmentsCross(a, b, c, d drawlist.ModelVertex) bool {
	ab1, ab2, cd1, cd2 := cross(a, b, c), cross(a, b, d), cross(c, d, a), cross(c, d, b)
	return (ab1 > 0 && ab2 < 0 || ab1 < 0 && ab2 > 0) && (cd1 > 0 && cd2 < 0 || cd1 < 0 && cd2 > 0)
}

func (r *Renderer) drawModelGeometry(g *drawlist.ModelGeometry) {
	r.modelColor.Fill(color.RGBA{R: 1, A: 255})
	if g.KeyPlane {
		r.modelKeyImage.Fill(index0Color)
		for i := range g.Faces {
			r.drawPreparedFace(g, g.Faces[i], true, true)
		}
	}
	for i := range g.Faces {
		r.drawPreparedFace(g, g.Faces[i], false, g.KeyPlane)
	}
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(0, 0, float32(r.w), float32(r.h), 0, 0, float32(r.w), float32(r.h))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.modelCommit, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Images: [4]*ebiten.Image{r.modelColor, nil, nil, nil}})
}

func (r *Renderer) drawPreparedFace(g *drawlist.ModelGeometry, f drawlist.ModelFace, key, useKey bool) {
	if polygonCrosses(f.Vertices) {
		strips := foldedStrips(f)
		if !key {
			r.modelStats.FoldedFaces++
			r.modelStats.FoldedStrips += len(strips)
		}
		for _, s := range strips {
			r.drawModelFace(g, s.Vertices, s.Texture, s.Color, s.Shaded, []uint16{0, 1, 2, 0, 2, 3}, key, useKey)
		}
		return
	}
	if tri, paints, _ := modelFaceTriangles(f); paints {
		r.drawModelFace(g, modelGPUVertices(f.Vertices), f.Texture, f.Color, f.Shaded, tri, key, useKey)
	}
}

func modelGPUVertices(v []drawlist.ModelVertex) []modelGPUVertex {
	out := make([]modelGPUVertex, len(v))
	for i, p := range v {
		out[i] = modelGPUVertex{X: float32(p.X), Y: float32(p.Y), Key: float32(p.Key), U: float32(p.U), V: float32(p.V), Shade: float32(p.Shade)}
	}
	return out
}

func foldedStrips(f drawlist.ModelFace) []modelGPUFace {
	v := f.Vertices
	n := len(v)
	if n < 3 {
		return nil
	}
	top, bot := 0, 0
	for i := 1; i < n; i++ {
		if v[i].Y < v[top].Y {
			top = i
		}
		if v[i].Y > v[bot].Y {
			bot = i
		}
	}
	var out []modelGPUFace
	for y := v[top].Y; y < v[bot].Y; y++ {
		l, a := chainAt(v, top, bot, -1, y)
		r, b := chainAt(v, top, bot, 1, y)
		if !a || !b || r.X <= l.X {
			continue
		}
		// A strip is one device quad for one positive CPU row. X follows the
		// CPU walk's biased 16.16 edge accumulator (computed by chainAt); all
		// other attributes stay fractional and constant through the one-pixel
		// vertical extent. Narrowing those lanes early changes key ownership.
		xl, xr := l.X, r.X
		if xr <= xl {
			continue
		}
		l.X, r.X = xl, xr
		l.Y, r.Y = float32(y), float32(y)
		bottomL, bottomR := l, r
		bottomL.Y, bottomR.Y = float32(y+1), float32(y+1)
		out = append(out, modelGPUFace{Vertices: []modelGPUVertex{l, r, bottomR, bottomL}, Texture: f.Texture, Color: f.Color, Shaded: f.Shaded})
	}
	return out
}
func chainAt(v []drawlist.ModelVertex, top, bot, step int, y int32) (modelGPUVertex, bool) {
	n := len(v)
	cur := top
	var candidate modelGPUVertex
	found := false
	for k := 0; k <= n; k++ {
		next := cur + step
		if next < 0 {
			next = n - 1
		}
		if next >= n {
			next = 0
		}
		a, b := v[cur], v[next]
		// The scan writer owns rows in the half-open edge interval. Admitting
		// the lower endpoint carries an edge into a row whose CPU polygon never
		// writes, which is visible at a folded junction [03 R-RAST-01 §1].
		if b.Y > a.Y && y >= a.Y && y < b.Y {
			d := float32(b.Y - a.Y)
			q := float32(y-a.Y) / d
			mix := func(x, z float32) float32 { return x + (z-x)*q }
			candidate = modelGPUVertex{
				X: float32(biasedEdgeX(a, b, y)), Y: float32(y),
				Key: mix(float32(a.Key), float32(b.Key)),
				U:   mix(float32(a.U), float32(b.U)), V: mix(float32(a.V), float32(b.V)),
				Shade: mix(float32(a.Shade), float32(b.Shade)),
			}
			found = true
		}
		if next == bot {
			break
		}
		cur = next
	}
	return candidate, found
}

// biasedEdgeX reproduces the span writer's fixed-point edge setup. The
// numerator slope is truncated once in signed 16.16, while the +65535 bias
// makes the row coordinate the authored ceiling after the arithmetic shift.
func biasedEdgeX(a, b drawlist.ModelVertex, y int32) int32 {
	dy := int64(b.Y - a.Y)
	if dy <= 0 {
		return a.X
	}
	xStart := (int64(a.X) << 16) + 65535
	xStep := (int64(b.X-a.X) << 16) / dy
	return int32((xStart + xStep*int64(y-a.Y)) >> 16)
}

func (r *Renderer) drawModelFace(g *drawlist.ModelGeometry, vertices []modelGPUVertex, textureFrame *formats.GAFFrame, flatColor uint8, shaded bool, idx []uint16, keyPass, useKey bool) {
	n := len(vertices)
	verts := make([]ebiten.Vertex, n)
	dx, dy := g.AnchorX-g.OriginX, g.AnchorY-g.OriginY
	for i, v := range vertices {
		verts[i] = ebiten.Vertex{DstX: float32(dx) + v.X, DstY: float32(dy) + v.Y, SrcX: float32(dx) + v.X, SrcY: float32(dy) + v.Y, ColorR: v.Key, ColorG: v.Shade, ColorB: float32(flatColor), ColorA: 1, Custom0: v.U, Custom1: v.V, Custom3: boolFloat(shaded)}
		if textureFrame != nil {
			verts[i].Custom2 = 1
		}
	}
	if keyPass {
		r.modelKeyImage.DrawTrianglesShader(verts, idx, r.modelKey, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.Blend{BlendOperationRGB: ebiten.BlendOperationMax, BlendOperationAlpha: ebiten.BlendOperationMax}, Images: [4]*ebiten.Image{r.modelCoord, nil, nil, nil}})
		return
	}
	var texture *ebiten.Image
	if textureFrame != nil {
		texture = r.gafImageFor(textureFrame)
	}
	r.modelColor.DrawTrianglesShader(verts, idx, r.modelBody, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Uniforms: map[string]any{"UseKey": useKey}, Images: [4]*ebiten.Image{r.modelCoord, r.modelKeyImage, r.tables.shade, texture}})
}

func boolFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

// uploadModelImage uploads one ModelImage as an index texture: index in red,
// coverage in green (255 covered, 0 uncovered), alpha opaque so premultiplied
// sampling recovers both bytes (C-G4). Coverage is the classic commit's key — a
// covered pixel is drawn, an uncovered one is the transparent key both the keyed
// body blit and the tinted shadow commit skip — so the green flag reproduces
// modelTarget.commit's `if !covered continue` and tintedCommit's `if !covered
// continue` exactly [R-REN-03A §1]. A model image changes every frame, so it is
// uploaded per draw (a per-frame upload is acceptable for parity,
// docs/DESIGN_GPU_RENDERER.md §2.3 note under caches).
func uploadModelImage(m ModelImage) *ebiten.Image {
	if m.W <= 0 || m.H <= 0 {
		return nil
	}
	buf := make([]byte, m.W*m.H*4)
	nc := len(m.Color)
	nv := len(m.Covered)
	for i := 0; i < m.W*m.H; i++ {
		if i < nv && m.Covered[i] {
			if i < nc {
				buf[i*4+0] = m.Color[i]
			}
			buf[i*4+1] = 255
		}
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(m.W, m.H)
	img.WritePixels(buf)
	return img
}

// blitModelBody reproduces modelTarget.commit: a keyed blit of the finished body
// image at its framebuffer top-left (DX,DY), clipped to the framebuffer, skipping
// uncovered (transparent-key) pixels (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5)
// [R-REN-03A §1]. It is the keyed-blit path of C-G4: the gafKeyed shader copies a
// covered texel's index under the source-over blend and leaves the destination
// untouched under an uncovered one, exactly the covered-pixel set and per-pixel
// value the byte writer's `dst[screenY*w+screenX] = color[i]` produces. The clip
// math is commit's own: screenX = DX+ix, clamped to [0,w), and likewise in Y.
func (r *Renderer) blitModelBody(m ModelImage) {
	if r == nil || r.offscreen == nil || r.gafKeyed == nil {
		return
	}
	img := uploadModelImage(m)
	if img == nil {
		return
	}
	x, y := m.DX, m.DY
	col0, col1 := maxInt(0, -x), minInt(m.W, r.w-x)
	row0, row1 := maxInt(0, -y), minInt(m.H, r.h-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	r.drawTexQuad(img, r.gafKeyed, ebiten.BlendSourceOver,
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(col0), float32(row0), float32(col1), float32(row1))
}

// blitModelShadow reproduces modelTarget.tintedCommit: for every covered shadow
// pixel it resolves the destination to ALP[color*256 + dst], the same ALP form
// the translucent strip blit (drawTint) already runs, sourced from the shadow
// image with NO anchor-offset subtraction because the shadow image's anchors are
// baked into DX,DY (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5)[R-REN-03D §4]. The
// shadow's colour plane is index 0 everywhere it is covered, so this darkens each
// ground pixel toward black; carrying the colour through the shader rather than
// assuming 0 keeps the commit identical to tintedCommit's `ALP[color*256+dst]`.
//
// It runs over a per-command snapshot of the covered rect (snapshotRect →
// destScratch), the dest-reading pattern WU-2.6 built, so the pass reads the
// pre-shadow destination exactly as tintedCommit reads c.indexed before the body
// overwrites it. The geometry — source rect in image-local pixels, dest in screen
// pixels — matches drawTint so the tint shader's destScratch addressing is the
// verified one.
func (r *Renderer) blitModelShadow(m ModelImage) {
	if r == nil || r.offscreen == nil || r.tint == nil || r.tables.alpha == nil || r.destScratch == nil {
		return
	}
	img := uploadModelImage(m)
	if img == nil {
		return
	}
	x, y := m.DX, m.DY
	col0, col1 := maxInt(0, -x), minInt(m.W, r.w-x)
	row0, row1 := maxInt(0, -y), minInt(m.H, r.h-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	dx0, dy0, dx1, dy1 := x+col0, y+row0, x+col1, y+row1
	r.snapshotRect(dx0, dy0, dx1, dy1)
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(
		float32(dx0), float32(dy0), float32(dx1), float32(dy1),
		float32(col0), float32(row0), float32(col1), float32(row1))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.tint, &ebiten.DrawTrianglesShaderOptions{
		Blend:  ebiten.BlendSourceOver,
		Images: [4]*ebiten.Image{img, r.destScratch, r.tables.alpha, nil},
	})
}
