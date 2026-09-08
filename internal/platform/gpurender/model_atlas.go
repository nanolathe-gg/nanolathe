package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
)

// modelTextureAtlas packs every resolved 3DO texture frame into shared pages, so
// one batched body pass can carry faces of many subjects and many textures
// (C-G9). Frames are packed once per identity and reused for their lifetime.
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
