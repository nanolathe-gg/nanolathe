package gpurender

import (
	"container/list"
	"encoding/binary"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// This is a renderer payload budget, not a retail constant. Driver allocation,
// atlas padding and scratch surfaces are additional [DESIGN_GPU_RENDERER §11].
const modelCacheBudget = 128 << 20

type modelImageCache struct {
	entries    map[string]*list.Element
	lru        list.List
	textures   map[*formats.GAFFrame]uint32
	bytes      int
	budget     int // zero selects modelCacheBudget
	keyScratch []byte
}

type modelCacheEntry struct {
	page        *modelCachePage
	fresh       bool
	key         string
	image       *ebiten.Image // index in red, height key in green, opaque alpha
	bytes, pins int
}

type modelImage struct {
	entry  *modelCacheEntry
	bounds image.Rectangle // framebuffer placement; never part of cache identity
}

// modelLocalBounds retains the producer's composition box. Authored device
// fixtures may omit dimensions; derive a box including outline endpoints then.
func modelLocalBounds(g *drawlist.ModelGeometry) image.Rectangle {
	if g.Width > 0 && g.Height > 0 {
		return image.Rect(0, 0, int(g.Width), int(g.Height))
	}
	var b image.Rectangle
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.Outline} {
		for _, f := range faces {
			for _, v := range f.Vertices {
				b = b.Union(image.Rect(int(v.X), int(v.Y), int(v.X)+1, int(v.Y)+1))
			}
		}
	}
	return b
}

func modelWorldBounds(g *drawlist.ModelGeometry) image.Rectangle {
	return modelLocalBounds(g).Add(image.Pt(int(g.AnchorX-g.OriginX), int(g.AnchorY-g.OriginY)))
}

// identity compares complete resolved raster input, rather than inferring that
// a unit is idle from its orders. Immutable texture identity includes the chosen
// animation/team frame. Placement, shadow and children are independent passes.
// A string map compares exact bytes even if its internal hash collides.
func (c *modelImageCache) identity(g *drawlist.ModelGeometry) string {
	c.keyScratch = c.keyScratch[:0]
	c.appendGeometry(g)
	return string(c.keyScratch)
}

func (c *modelImageCache) word(v int32) {
	c.keyScratch = binary.LittleEndian.AppendUint32(c.keyScratch, uint32(v))
}
func (c *modelImageCache) flag(v bool) {
	if v {
		c.word(1)
	} else {
		c.word(0)
	}
}
func (c *modelImageCache) appendGeometry(g *drawlist.ModelGeometry) {
	c.word(g.Width)
	c.word(g.Height)
	c.word(g.Scale)
	c.flag(g.KeyPlane)
	c.word(int32(g.Waterline))
	c.word(int32(g.WaterlineKey))
	c.flag(g.Digger)
	c.word(int32(g.DiggerKey))
	c.flag(g.Reveal != nil)
	if v := g.Reveal; v != nil {
		c.word(int32(v.Line))
		c.word(int32(v.Floor))
		c.word(int32(v.Below))
		c.word(int32(v.Band))
		c.word(int32(v.Above))
	}
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.Outline} {
		c.word(int32(len(faces)))
		for _, f := range faces {
			var id uint32
			if f.Texture != nil {
				if c.textures == nil {
					c.textures = make(map[*formats.GAFFrame]uint32)
				}
				id = c.textures[f.Texture]
				if id == 0 {
					id = uint32(len(c.textures) + 1)
					c.textures[f.Texture] = id
				}
			}
			c.word(int32(id))
			c.word(int32(f.Color))
			c.flag(f.Shaded)
			c.word(int32(len(f.Vertices)))
			for _, v := range f.Vertices {
				c.word(v.X)
				c.word(v.Y)
				c.word(v.Key)
				c.word(v.U)
				c.word(v.V)
				c.word(int32(v.Shade))
			}
		}
	}
	c.flag(g.Supersample != nil)
	if ss := g.Supersample; ss != nil {
		// Supersample coordinates are local to the native body's resolve, not
		// framebuffer placement. Retain any explicit internal translation.
		c.word(ss.AnchorX - ss.OriginX)
		c.word(ss.AnchorY - ss.OriginY)
		c.appendGeometry(ss)
	}
}

func (r *Renderer) acquireModelImage(g *drawlist.ModelGeometry) *modelImage {
	// Attachment configuration is not cached body content. Validate it on hits
	// too, so a keyless body cannot bypass the keyed-group admission rule.
	if g == nil || !g.Eligible || !r.modelGeometryConfigSupported(g) {
		return nil
	}
	b := modelWorldBounds(g)
	if b.Empty() {
		return nil
	}
	c := &r.modelCache
	key := r.frameModelKey(g)
	if elem := c.entries[key]; elem != nil {
		e := elem.Value.(*modelCacheEntry)
		e.pins++
		c.lru.MoveToFront(elem)
		if e.fresh {
			r.modelStats.CacheMisses++
			e.fresh = false
		} else {
			r.modelStats.CacheHits++
		}
		return &modelImage{entry: e, bounds: b}
	}
	if !r.modelFacesSupported(g) || g.Supersample != nil && !r.modelFacesSupported(g.Supersample) {
		return nil
	}
	r.modelStats.CacheMisses++
	r.ensureModelScratch(b.Dx(), b.Dy())
	r.modelRasterOrigin = b.Min
	r.rasterModelGeometry(g)
	img := ebiten.NewImage(b.Dx(), b.Dy())
	r.resetGeometry()
	r.appendTexQuad(0, 0, float32(b.Dx()), float32(b.Dy()), 0, 0, float32(b.Dx()), float32(b.Dy()))
	img.DrawTrianglesShader(r.verts, r.idx, r.modelPack, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendCopy, Images: [4]*ebiten.Image{r.modelColor, r.modelKeyImage}})
	e := &modelCacheEntry{key: key, image: img, bytes: 4*b.Dx()*b.Dy() + len(key), pins: 1}
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}
	c.entries[key] = c.lru.PushFront(e)
	c.bytes += e.bytes
	r.trimModelCache()
	r.modelStats.RasterPixels += b.Dx() * b.Dy()
	return &modelImage{entry: e, bounds: b}
}

func (r *Renderer) releaseModelImage(m *modelImage) {
	if m == nil {
		return
	}
	m.entry.pins--
	r.trimModelCache()
}

func (r *Renderer) trimModelCache() {
	c := &r.modelCache
	budget := c.budget
	if budget == 0 {
		budget = modelCacheBudget
	}
	for elem := c.lru.Back(); elem != nil && c.bytes > budget; {
		prev := elem.Prev()
		e := elem.Value.(*modelCacheEntry)
		// A shadow/body/child may still be used by the current command. Pinned
		// working images can temporarily exceed the budget; release trims again.
		if e.pins == 0 {
			delete(c.entries, e.key)
			c.lru.Remove(elem)
			c.bytes -= e.bytes
			if p := e.page; p != nil {
				p.refs--
				if p.refs == 0 {
					p.image.Deallocate()
					c.bytes -= p.bytes
				}
			} else {
				e.image.Deallocate()
			}
			r.modelStats.CacheEvictions++
		}
		elem = prev
	}
	r.modelStats.CacheBytes = c.bytes
}

func (r *Renderer) ensureModelScratch(w, h int) {
	b := image.Rect(0, 0, w, h)
	if r.modelScratch[0] == nil || r.modelScratch[0].Bounds().Dx() < w || r.modelScratch[0].Bounds().Dy() < h {
		if old := r.modelScratch[0]; old != nil {
			w, h = maxInt(w, old.Bounds().Dx()), maxInt(h, old.Bounds().Dy())
		}
		for i, img := range r.modelScratch {
			if img != nil {
				img.Deallocate()
			}
			r.modelScratch[i] = ebiten.NewImage(w, h)
		}
	}
	r.modelColor = r.modelScratch[0].SubImage(b).(*ebiten.Image)
	r.modelKeyImage = r.modelScratch[1].SubImage(b).(*ebiten.Image)
	r.modelCoord = r.modelScratch[2].SubImage(b).(*ebiten.Image)
	r.modelProcessed = r.modelScratch[3].SubImage(b).(*ebiten.Image)
	// Keyless bodies still pack a well-defined zero key; scratch held another
	// subject last time. Keyed rasterization clears it again before reduction.
	r.modelKeyImage.Fill(index0Color)
}

func (r *Renderer) commitModelImage(img *ebiten.Image, b image.Rectangle) {
	r.resetGeometry()
	r.appendTexQuad(float32(b.Min.X), float32(b.Min.Y), float32(b.Max.X), float32(b.Max.Y), float32(img.Bounds().Min.X), float32(img.Bounds().Min.Y), float32(img.Bounds().Max.X), float32(img.Bounds().Max.Y))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.modelCommit, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Images: [4]*ebiten.Image{img}})
}

func (r *Renderer) commitModelShadow(shadow, body *modelImage) {
	b := shadow.bounds
	r.snapshotRect(b.Min.X, b.Min.Y, b.Max.X, b.Max.Y)
	r.resetGeometry()
	r.appendTexQuad(float32(b.Min.X), float32(b.Min.Y), float32(b.Max.X), float32(b.Max.Y), float32(shadow.entry.image.Bounds().Min.X), float32(shadow.entry.image.Bounds().Min.Y), float32(shadow.entry.image.Bounds().Max.X), float32(shadow.entry.image.Bounds().Max.Y))
	d := b.Min.Sub(body.bounds.Min)
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.modelShadowCommit, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendSourceOver, Images: [4]*ebiten.Image{shadow.entry.image, body.entry.image, r.destScratch, r.tables.alpha}, Uniforms: map[string]any{"BodyOffset": []float32{float32(d.X), float32(d.Y)}, "WorldOffset": []float32{float32(b.Min.X), float32(b.Min.Y)}}})
	r.modelStats.Shadows++
}

// Children merge into a local union box; retained cached keys include ownership
// under transparent pixels. The scene background is never cached or staged here.
func (r *Renderer) composeCachedModelChildren(g *drawlist.ModelGeometry, parent *modelImage) {
	b := parent.bounds
	for _, child := range g.Children {
		if cg := child.Geometry; cg != nil && cg.Eligible && cg.KeyPlane && len(cg.Children) == 0 {
			b = b.Union(modelWorldBounds(cg))
		}
	}
	if r.modelStage == nil || r.modelStage.Bounds().Dx() != b.Dx() || r.modelStage.Bounds().Dy() != b.Dy() {
		if r.modelStage != nil {
			r.modelStage.Deallocate()
			r.modelStageScratch.Deallocate()
		}
		r.modelStage, r.modelStageScratch = ebiten.NewImage(b.Dx(), b.Dy()), ebiten.NewImage(b.Dx(), b.Dy())
	}
	r.modelStage.Fill(color.RGBA{R: 1, A: 255})
	d := parent.bounds.Min.Sub(b.Min)
	op := &ebiten.DrawImageOptions{Blend: ebiten.BlendCopy}
	op.GeoM.Translate(float64(d.X), float64(d.Y))
	r.modelStage.DrawImage(parent.entry.image, op)
	for _, child := range g.Children {
		cg := child.Geometry
		if cg == nil || !cg.KeyPlane || len(cg.Children) != 0 {
			r.modelStats.Skipped++
			continue
		}
		m := r.acquireModelImage(cg)
		if m == nil {
			r.modelStats.Skipped++
			continue
		}
		r.modelStageScratch.DrawImage(r.modelStage, &ebiten.DrawImageOptions{Blend: ebiten.BlendCopy})
		d := m.bounds.Min.Sub(b.Min)
		r.resetGeometry()
		r.appendTexQuad(float32(d.X), float32(d.Y), float32(d.X+m.bounds.Dx()), float32(d.Y+m.bounds.Dy()), float32(m.entry.image.Bounds().Min.X), float32(m.entry.image.Bounds().Min.Y), float32(m.entry.image.Bounds().Max.X), float32(m.entry.image.Bounds().Max.Y))
		r.modelStageScratch.DrawTrianglesShader(r.verts, r.idx, r.modelChild, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendCopy, Images: [4]*ebiten.Image{m.entry.image, nil, r.modelStage}, Uniforms: map[string]any{"KeyDelta": float32(child.KeyDelta), "PriorOffset": []float32{float32(d.X), float32(d.Y)}}})
		r.modelStage, r.modelStageScratch = r.modelStageScratch, r.modelStage
		r.releaseModelImage(m)
		r.modelStats.GPU++
	}
	r.commitModelImage(r.modelStage, b)
	r.modelStats.ComposedGroups++
}
