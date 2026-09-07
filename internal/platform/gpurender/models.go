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

// ModelStats is the per-Execute accounting for modern model execution.
// It is diagnostic data for captures, never simulation state.
type ModelStats struct {
	GPU, Skipped, Shadows, ShadowsOmitted, StagedGroups, NoBody int
	UnsupportedGeometry, MissingTexture, UnsupportedFace        int
	FoldedFaces, FoldedStrips                                   int
	TexturedQuadFaces, TexturedQuadStrips                       int
	ComposedGroups                                              int
	RevealOrOutlineOmitted                                      int
	WaterlineOrDiggerOmitted                                    int
	StagingCommandsOmitted                                      int
}

func (r *Renderer) ModelStats() ModelStats {
	if r == nil {
		return ModelStats{}
	}
	return r.modelStats
}

// Model replays geometry or records an explicit omission. Modern mode never
// resolves Ref or substitutes CPU images [DESIGN_GPU_RENDERER.md §9–§10].
func (r *Renderer) Model(cmd drawlist.Model) {
	if r == nil || r.offscreen == nil {
		return
	}
	if cmd.ShadowOmissions != 0 {
		r.modelStats.ShadowsOmitted += cmd.ShadowOmissions
	}
	if cmd.GroupOmission {
		r.modelStats.StagedGroups++
	}
	if cmd.Geometry != nil && cmd.Geometry.Fallback == drawlist.ModelFallbackNoBodyCommit {
		r.modelStats.NoBody++
		return
	}
	if cmd.ShadowOnly && cmd.Geometry != nil && cmd.Geometry.Shadow == nil {
		return
	}
	if g := cmd.Geometry; g != nil && g.Eligible && r.modelGeometrySupported(g) {
		r.drawModelGeometry(g, cmd.ShadowOnly)
		if !cmd.ShadowOnly {
			r.modelStats.GPU++
		}
		return
	}
	if cmd.Geometry != nil {
		if cmd.Geometry.Shadow != nil {
			r.modelStats.ShadowsOmitted++
		}
		switch cmd.Geometry.Fallback {
		case drawlist.ModelFallbackRevealOrOutline:
			r.modelStats.RevealOrOutlineOmitted++
		case drawlist.ModelFallbackWaterlineOrDigger:
			r.modelStats.WaterlineOrDiggerOmitted++
		case drawlist.ModelFallbackStaging:
			r.modelStats.StagingCommandsOmitted++
		case drawlist.ModelFallbackNoBodyCommit:
			r.modelStats.NoBody++
		}
	}
	r.modelStats.Skipped++
}

func (r *Renderer) modelGeometrySupported(g *drawlist.ModelGeometry) bool {
	if r == nil || g == nil || g.Scale != 1 || len(g.Faces) == 0 || r.modelKey == nil || r.modelBody == nil || r.modelCommit == nil || r.modelKeyImage == nil || r.modelColor == nil || r.modelCoord == nil {
		return false
	}
	if g.KeyPlane && (g.Waterline != drawlist.ModelWaterlineNone || g.Digger) && (r.modelClip == nil || r.modelProcessed == nil || g.Waterline == drawlist.ModelWaterlineBlue && r.tables.blue == nil) {
		return false
	}
	if len(g.Children) != 0 && (!g.KeyPlane || r.modelPack == nil || r.modelChild == nil || r.modelStage == nil || r.modelStageScratch == nil) {
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

func (r *Renderer) drawModelGeometry(g *drawlist.ModelGeometry, shadowOnly bool) {
	shadow := g.Shadow != nil && g.Shadow.Eligible && r.modelShadowCommit != nil && r.modelShadowImage != nil && r.tables.alpha != nil && r.modelGeometrySupported(g.Shadow)
	if shadow {
		r.rasterModelGeometry(g.Shadow)
		r.modelColor, r.modelShadowImage = r.modelShadowImage, r.modelColor
	} else if g.Shadow != nil {
		r.modelStats.ShadowsOmitted++
	}
	r.rasterModelGeometry(g)
	if shadow {
		r.snapshotRect(0, 0, r.w, r.h)
		r.resetGeometry()
		r.appendTexQuad(0, 0, float32(r.w), float32(r.h), 0, 0, float32(r.w), float32(r.h))
		r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.modelShadowCommit, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Images: [4]*ebiten.Image{r.modelShadowImage, r.modelColor, r.destScratch, r.tables.alpha}})
		r.modelStats.Shadows++
	}
	if shadowOnly {
		return
	}
	body := r.modelColor
	if len(g.Children) != 0 {
		body = r.composeModelChildren(g)
	}
	r.resetGeometry()
	r.appendTexQuad(0, 0, float32(r.w), float32(r.h), 0, 0, float32(r.w), float32(r.h))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.modelCommit, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Images: [4]*ebiten.Image{body, nil, nil, nil}})
}

func (r *Renderer) rasterModelGeometry(g *drawlist.ModelGeometry) {
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
	r.drawModelOutline(g)
	r.clipModel(g)
}

func (r *Renderer) drawPreparedFace(g *drawlist.ModelGeometry, f drawlist.ModelFace, key, useKey bool) {
	// The retail textured branch is a quad scanline mapper, not two affine
	// triangles: it interpolates U/V between the two active edge chains on each
	// row [03 R-REN-03A §5][03 R-RAST-01 §1].  Interpolating the four corners
	// through a GPU diagonal breaks that mapping into two planes, which makes a
	// visible texture seam on skewed solar panels.  Keep the row preparation on
	// the CPU, but leave coverage, key reduction, texture sampling and colour
	// writes to the device. This is GPU Classic implementation policy, not a
	// claim that this path duplicates every fixed-point pixel detail.
	if f.Texture != nil && len(f.Vertices) == 4 {
		strips := modelTextureStrips(f)
		if !key {
			r.modelStats.TexturedQuadFaces++
			r.modelStats.TexturedQuadStrips += len(strips)
		}
		r.drawModelStripBatch(g, strips, key, useKey)
		return
	}
	if polygonCrosses(f.Vertices) {
		strips := foldedStrips(f)
		if !key {
			r.modelStats.FoldedFaces++
			r.modelStats.FoldedStrips += len(strips)
		}
		r.drawModelStripBatch(g, strips, key, useKey)
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
	return modelSpanStrips(f)
}

// modelTextureStrips prepares a textured quad from the original two-chain
// scanline mapper. Unlike the conventional triangle path, no diagonal becomes
// an interpolation boundary: every device quad is one source scanline.
func modelTextureStrips(f drawlist.ModelFace) []modelGPUFace {
	return modelSpanStrips(f)
}

func modelSpanStrips(f drawlist.ModelFace) []modelGPUFace {
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
		// Device varyings are evaluated at a pixel centre, while the span writer
		// starts each lane at its integer left column. Shift both endpoint lanes
		// back by half a horizontal step so the first device sample is leftA and
		// the last is leftA+(width-1)*step [03 R-RAST-01 §1].
		l.Key, r.Key = centerSampledSpan(l.Key, r.Key, xr-xl)
		l.U, r.U = centerSampledSpan(l.U, r.U, xr-xl)
		l.V, r.V = centerSampledSpan(l.V, r.V, xr-xl)
		l.Shade, r.Shade = centerSampledSpan(l.Shade, r.Shade, xr-xl)
		l.X, r.X = xl, xr
		l.Y, r.Y = float32(y), float32(y)
		bottomL, bottomR := l, r
		bottomL.Y, bottomR.Y = float32(y+1), float32(y+1)
		out = append(out, modelGPUFace{Vertices: []modelGPUVertex{l, r, bottomR, bottomL}, Texture: f.Texture, Color: f.Color, Shaded: f.Shaded})
	}
	return out
}

func centerSampledSpan(left, right, width float32) (float32, float32) {
	step := (right - left) / width
	return left - step*0.5, right - step*0.5
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
	r.modelColor.DrawTrianglesShader(verts, idx, r.modelBody, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Uniforms: modelBodyUniforms(g, useKey), Images: [4]*ebiten.Image{r.modelCoord, r.modelKeyImage, r.tables.shade, texture}})
}

// drawModelStripBatch submits every prepared row from one source face together.
// The rows share texture, flat colour and shade state; batching them keeps the
// scanline mapping without turning one face into two device submissions per
// source row. The uint16 index transport bounds a submission, not model size.
func (r *Renderer) drawModelStripBatch(g *drawlist.ModelGeometry, strips []modelGPUFace, keyPass, useKey bool) {
	for start := 0; start < len(strips); {
		r.resetGeometry()
		end := start
		dx, dy := g.AnchorX-g.OriginX, g.AnchorY-g.OriginY
		for end < len(strips) && r.quadBatchHasRoom() {
			s := strips[end]
			base := uint16(len(r.verts))
			for _, v := range s.Vertices {
				vertex := ebiten.Vertex{DstX: float32(dx) + v.X, DstY: float32(dy) + v.Y, SrcX: float32(dx) + v.X, SrcY: float32(dy) + v.Y, ColorR: v.Key, ColorG: v.Shade, ColorB: float32(s.Color), ColorA: 1, Custom0: v.U, Custom1: v.V, Custom3: boolFloat(s.Shaded)}
				if s.Texture != nil {
					vertex.Custom2 = 1
				}
				r.verts = append(r.verts, vertex)
			}
			r.idx = append(r.idx, base, base+1, base+2, base, base+2, base+3)
			end++
		}
		if len(r.verts) == 0 {
			return
		}
		texture := r.gafImageFor(strips[start].Texture)
		if keyPass {
			r.modelKeyImage.DrawTrianglesShader(r.verts, r.idx, r.modelKey, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.Blend{BlendOperationRGB: ebiten.BlendOperationMax, BlendOperationAlpha: ebiten.BlendOperationMax}, Images: [4]*ebiten.Image{r.modelCoord, nil, nil, nil}})
		} else {
			r.modelColor.DrawTrianglesShader(r.verts, r.idx, r.modelBody, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Uniforms: modelBodyUniforms(g, useKey), Images: [4]*ebiten.Image{r.modelCoord, r.modelKeyImage, r.tables.shade, texture}})
		}
		start = end
	}
}

func boolFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

func modelBodyUniforms(g *drawlist.ModelGeometry, useKey bool) map[string]any {
	u := map[string]any{"UseKey": useKey, "UseReveal": g.Reveal != nil}
	if rev := g.Reveal; rev != nil {
		u["RevealBounds"] = []float32{float32(rev.Line), float32(rev.Floor)}
		u["RevealColors"] = []float32{float32(rev.Below), float32(rev.Band), float32(rev.Above)}
	}
	return u
}

func (r *Renderer) drawModelOutline(g *drawlist.ModelGeometry) {
	if len(g.Outline) == 0 {
		return
	}
	var points []modelGPUFace
	for _, f := range g.Outline {
		v := f.Vertices
		if len(v) < 2 {
			continue
		}
		top, bottom := 0, 0
		for i := range v {
			if v[i].Y < v[top].Y {
				top = i
			}
			if v[i].Y > v[bottom].Y {
				bottom = i
			}
		}
		for y := v[top].Y; y < v[bottom].Y; y++ {
			left, a := chainAt(v, top, bottom, -1, y)
			right, b := chainAt(v, top, bottom, 1, y)
			if !a || !b || right.X <= left.X {
				continue
			}
			for _, p := range []modelGPUVertex{left, right} {
				// Outlines also visit rings dropped by the body material dispatch;
				// retain the classic composition-box clip for those endpoints.
				if g.Width > 0 && (p.X < 0 || p.X >= float32(g.Width) || p.Y < 0 || p.Y >= float32(g.Height)) {
					continue
				}
				q, s, t := p, p, p
				q.X++
				s.X++
				s.Y++
				t.Y++
				points = append(points, modelGPUFace{Vertices: []modelGPUVertex{p, q, s, t}, Color: f.Color})
			}
		}
	}
	outline := *g
	outline.Reveal = nil
	if g.KeyPlane {
		r.drawModelStripBatch(&outline, points, true, true)
	}
	r.drawModelStripBatch(&outline, points, false, g.KeyPlane)
}

func (r *Renderer) clipModel(g *drawlist.ModelGeometry) {
	if !g.KeyPlane || g.Waterline == drawlist.ModelWaterlineNone && !g.Digger {
		return
	}
	r.resetGeometry()
	r.appendTexQuad(0, 0, float32(r.w), float32(r.h), 0, 0, float32(r.w), float32(r.h))
	r.modelProcessed.DrawTrianglesShader(r.verts, r.idx, r.modelClip, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendCopy, Images: [4]*ebiten.Image{r.modelColor, r.modelKeyImage, r.tables.blue, nil}, Uniforms: map[string]any{"WaterlineMode": float32(g.Waterline), "WaterlineKey": float32(g.WaterlineKey), "Digger": g.Digger, "DiggerKey": float32(g.DiggerKey)}})
	r.modelColor, r.modelProcessed = r.modelProcessed, r.modelColor
}

// composeModelChildren stores color in red and the current staging key in green.
// Each child reads the completed prior stage and writes a separate image, so the
// full signed comparison and wrapped store remain ordered [03 R-REN-03A §4].
func (r *Renderer) composeModelChildren(g *drawlist.ModelGeometry) *ebiten.Image {
	r.resetGeometry()
	r.appendTexQuad(0, 0, float32(r.w), float32(r.h), 0, 0, float32(r.w), float32(r.h))
	r.modelStage.DrawTrianglesShader(r.verts, r.idx, r.modelPack, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendCopy, Images: [4]*ebiten.Image{r.modelColor, r.modelKeyImage, nil, nil}})
	for _, child := range g.Children {
		if child.Geometry == nil || !child.Geometry.Eligible || !child.Geometry.KeyPlane || len(child.Geometry.Children) != 0 || !r.modelGeometrySupported(child.Geometry) {
			r.modelStats.Skipped++
			continue
		}
		r.rasterModelGeometry(child.Geometry)
		r.resetGeometry()
		r.appendTexQuad(0, 0, float32(r.w), float32(r.h), 0, 0, float32(r.w), float32(r.h))
		r.modelStageScratch.DrawTrianglesShader(r.verts, r.idx, r.modelChild, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendCopy, Images: [4]*ebiten.Image{r.modelColor, r.modelKeyImage, r.modelStage, nil}, Uniforms: map[string]any{"KeyDelta": float32(child.KeyDelta)}})
		r.modelStage, r.modelStageScratch = r.modelStageScratch, r.modelStage
		r.modelStats.GPU++
	}
	r.modelStats.ComposedGroups++
	return r.modelStage
}
