package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

type modelTextureSlot struct {
	img        *ebiten.Image
	x, y, w, h int
}
type modelTextureAtlas struct {
	slots     map[*formats.GAFFrame]modelTextureSlot
	page      *ebiten.Image
	x, y, row int
}

func (r *Renderer) modelTextureFor(f *formats.GAFFrame) modelTextureSlot {
	if f == nil {
		return modelTextureSlot{}
	}
	a := &r.textureAtlas
	if s, ok := a.slots[f]; ok {
		return s
	}
	if a.slots == nil {
		a.slots = make(map[*formats.GAFFrame]modelTextureSlot)
	}
	w, h := int(f.Width), int(f.Height)
	if w <= 0 || h <= 0 {
		return modelTextureSlot{}
	}
	if w > 2046 || h > 2046 {
		s := modelTextureSlot{img: r.gafImageFor(f), w: w, h: h}
		a.slots[f] = s
		return s
	}
	if a.x+w+2 > 2048 {
		a.x = 0
		a.y += a.row
		a.row = 0
	}
	if a.page == nil || a.y+h+2 > 2048 {
		a.page = ebiten.NewImage(2048, 2048)
		a.x = 0
		a.y = 0
		a.row = 0
	}
	s := modelTextureSlot{a.page, a.x + 1, a.y + 1, w, h}
	op := &ebiten.DrawImageOptions{Blend: ebiten.BlendCopy}
	op.GeoM.Translate(float64(s.x), float64(s.y))
	a.page.DrawImage(r.gafImageFor(f), op)
	a.x += w + 2
	if h+2 > a.row {
		a.row = h + 2
	}
	a.slots[f] = s
	return s
}

// Preserve source face and scanline order while sharing immutable texture pages.
func (r *Renderer) drawModelFacesBatched(g *drawlist.ModelGeometry, faces []preparedModelFace, key bool) {
	// The packed dimension lane is exact only for dimensions below 4096.
	// Keep the existing per-face GPU route for larger textures.
	for _, f := range faces {
		if t := f.face.Texture; t != nil && (int(t.Width) >= 4096 || int(t.Height) >= 4096) {
			for _, face := range faces {
				r.drawPreparedModelFace(g, face, key)
			}
			return
		}
	}

	for _, f := range faces {
		r.modelTextureFor(f.face.Texture)
	}
	r.resetGeometry()
	var page *ebiten.Image
	uniforms := modelBodyUniforms(g, g.KeyPlane)
	uniforms["TextureAtlas"] = true
	flush := func() {
		if len(r.idx) == 0 {
			return
		}
		if key {
			r.modelKeyImage.DrawTrianglesShader(r.verts, r.idx, r.modelKey, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.Blend{BlendOperationRGB: ebiten.BlendOperationMax, BlendOperationAlpha: ebiten.BlendOperationMax}, Images: [4]*ebiten.Image{r.modelCoord}})
		} else {
			r.modelColor.DrawTrianglesShader(r.verts, r.idx, r.modelBody, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Uniforms: uniforms, Images: [4]*ebiten.Image{r.modelCoord, r.modelKeyImage, r.tables.shade, page}})
		}
		r.resetGeometry()
	}
	dx, dy := g.AnchorX-g.OriginX-int32(r.modelRasterOrigin.X), g.AnchorY-g.OriginY-int32(r.modelRasterOrigin.Y)
	appendFace := func(v []modelGPUVertex, idx []uint16, tex *formats.GAFFrame, c uint8, shaded bool) {
		s := r.modelTextureFor(tex)
		if !key && s.img != nil && page != s.img {
			flush()
			page = s.img
		}
		if len(r.verts)+len(v) > 65535 {
			flush()
		}
		base := uint16(len(r.verts))
		for _, p := range v {
			q := ebiten.Vertex{DstX: float32(dx) + p.X, DstY: float32(dy) + p.Y, SrcX: float32(s.x + r.modelCoord.Bounds().Min.X), SrcY: float32(s.y + r.modelCoord.Bounds().Min.Y), ColorR: p.Key, ColorG: p.Shade, ColorB: float32(c), ColorA: 1, Custom0: p.U, Custom1: p.V, Custom3: boolFloat(shaded)}
			if tex != nil {
				q.Custom2 = float32(s.w*4096 + s.h)
			}
			r.verts = append(r.verts, q)
		}
		for _, i := range idx {
			r.idx = append(r.idx, base+i)
		}
	}
	for _, f := range faces {
		for _, s := range f.strips {
			appendFace(s.Vertices, []uint16{0, 1, 2, 0, 2, 3}, s.Texture, s.Color, s.Shaded)
		}
		if len(f.indices) > 0 {
			appendFace(f.vertices, f.indices, f.face.Texture, f.face.Color, f.face.Shaded)
		}
	}
	flush()
}
