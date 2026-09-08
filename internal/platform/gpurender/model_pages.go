package gpurender

import (
	"container/list"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"image"
	"image/color"
)

type modelCachePage struct {
	image       *ebiten.Image
	bytes, refs int
}

type modelStageJob struct {
	g     *drawlist.ModelGeometry
	key   string
	rect  image.Rectangle
	faces []preparedModelFace
}

func (r *Renderer) prepareModelPages(l *drawlist.List) []*modelCacheEntry {
	var pins []*modelCacheEntry
	var jobs []modelStageJob
	seen := map[string]bool{}
	x, y, row := 0, 0, 0
	flush := func() {
		if len(jobs) == 0 {
			return
		}
		if r.stageAtlas[0] == nil {
			for i := range r.stageAtlas {
				r.stageAtlas[i] = ebiten.NewImage(2048, 2048)
			}
		}
		r.stageAtlas[0].Fill(color.RGBA{R: 1, A: 255})
		r.stageAtlas[1].Fill(index0Color)
		set := func(j modelStageJob) {
			r.modelColor = r.stageAtlas[0]
			r.modelKeyImage = r.stageAtlas[1]
			r.modelCoord = r.stageAtlas[2]
			r.modelRasterOrigin = modelWorldBounds(j.g).Min.Sub(j.rect.Min)
		}
		for _, j := range jobs {
			set(j)
			if j.g.KeyPlane {
				r.drawModelFacesBatched(j.g, j.faces, true)
			}
		}
		for _, j := range jobs {
			set(j)
			r.drawModelFacesBatched(j.g, j.faces, false)
		}
		usedH := 0
		for _, j := range jobs {
			if j.rect.Max.Y > usedH {
				usedH = j.rect.Max.Y
			}
		}
		img := ebiten.NewImage(2048, usedH)
		page := &modelCachePage{image: img, bytes: 2048 * usedH * 4, refs: len(jobs)}
		r.resetGeometry()
		r.appendTexQuad(0, 0, 2048, float32(usedH), 0, 0, 2048, float32(usedH))
		img.DrawTrianglesShader(r.verts, r.idx, r.modelPack, &ebiten.DrawTrianglesShaderOptions{Blend: ebiten.BlendCopy, Images: [4]*ebiten.Image{r.stageAtlas[0], r.stageAtlas[1]}})
		r.modelCache.bytes += page.bytes
		for _, j := range jobs {
			e := &modelCacheEntry{fresh: true, page: page, key: j.key, image: img.SubImage(j.rect).(*ebiten.Image), bytes: len(j.key), pins: 1}
			if r.modelCache.entries == nil {
				r.modelCache.entries = make(map[string]*list.Element)
			}
			r.modelCache.entries[j.key] = r.modelCache.lru.PushFront(e)
			r.modelCache.bytes += e.bytes
			pins = append(pins, e)
			r.modelStats.RasterPixels += j.rect.Dx() * j.rect.Dy()
		}
		jobs = nil
		x, y, row = 0, 0, 0
	}
	var visit func(*drawlist.ModelGeometry)
	visit = func(g *drawlist.ModelGeometry) {
		if g == nil {
			return
		}
		visit(g.Shadow)
		for _, c := range g.Children {
			visit(c.Geometry)
		}
		if !g.Eligible || !r.modelGeometryConfigSupported(g) || g.Supersample != nil || len(g.Outline) > 0 || g.Waterline != drawlist.ModelWaterlineNone || g.Digger {
			return
		}
		b := modelLocalBounds(g)
		if b.Min != (image.Point{}) || b.Empty() || b.Dx() > 2048 || b.Dy() > 2048 {
			return
		}
		for _, f := range g.Faces {
			for _, v := range f.Vertices {
				if v.X < 0 || v.Y < 0 || v.X > int32(b.Dx()) || v.Y > int32(b.Dy()) {
					return
				}
			}
		}
		key := r.frameModelKey(g)
		if seen[key] {
			return
		}
		seen[key] = true
		if el := r.modelCache.entries[key]; el != nil {
			e := el.Value.(*modelCacheEntry)
			e.pins++
			pins = append(pins, e)
			return
		}
		if !r.modelFacesSupported(g) {
			return
		}
		if x+b.Dx() > 2048 {
			x = 0
			y += row
			row = 0
		}
		if y+b.Dy() > 2048 {
			flush()
		}
		rect := image.Rect(x, y, x+b.Dx(), y+b.Dy())
		x += b.Dx()
		if b.Dy() > row {
			row = b.Dy()
		}
		j := modelStageJob{g: g, key: key, rect: rect, faces: r.modelPrep.prepared.take(len(g.Faces))}
		for i, f := range g.Faces {
			j.faces[i] = r.prepareModelFace(f)
			r.modelTextureFor(f.Texture)
		}
		jobs = append(jobs, j)
	}
	l.VisitModels(func(m drawlist.Model) { visit(m.Geometry) })
	flush()
	return pins
}
