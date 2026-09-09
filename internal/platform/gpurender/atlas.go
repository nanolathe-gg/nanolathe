package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
)

// The scene atlas: one texture per page holding every 2D source the scene shader
// samples — GAF frames, FNT glyph strips, PCX backgrounds and the per-frame
// indexed surface (docs/DESIGN_GPU_RENDERER.md §11.2 "One scene shader for the
// 2D families"). Ebitengine merges consecutive draws only when the source images
// match, so packing every sprite of a frame onto one page is what lets a whole
// phase's opaque writes leave as a single device draw.
//
// Storage matches the per-frame textures it replaces exactly, so the byte
// semantics of every family are unchanged (C-G4): the palette index rides the
// red channel, the source's opacity flag rides green (255 opaque, 0 transparent
// or absent), alpha is opaque so premultiplied sampling recovers both bytes. A
// PCX background is copied opaquely, so its green flag is set everywhere.
//
// Placement is a shelf packer: entries are laid left to right on a row whose
// height is the tallest entry so far, and a row that cannot take the next entry
// starts a new one. Entries are never freed — an entry is keyed by the immutable
// source's pointer identity and lives for the renderer's lifetime — so the packer
// needs no free list. An entry too large for a shared page gets a page of its
// own; the scene batch then splits at that command, which is the same rule a
// second shared page follows.

// sceneAtlasPageSize is the side of a shared scene atlas page in pixels
// (docs/DESIGN_GPU_RENDERER.md §11.2). One page is 16 MiB of RGBA8.
const sceneAtlasPageSize = 2048

// sceneEntry is one source's placement on a scene atlas page. x, y, w and h are
// page pixel coordinates, which are also the coordinates the scene shader
// samples, because the whole page is bound as the source image.
type sceneEntry struct {
	page int32
	x, y int32
	w, h int32
	ok   bool
}

// scenePage is one atlas texture and its shelf-packer cursor.
type scenePage struct {
	img    *ebiten.Image
	w, h   int
	shelfX int
	shelfY int
	shelfH int
	// shared is false for a page allocated to hold one oversized entry, so the
	// packer never tries to place anything else on it.
	shared bool
}

// sceneAtlas owns the pages and the identity→placement maps. The maps are keyed
// by pointer and never ranged in a way that reaches output, so they introduce no
// ordering [I1].
type sceneAtlas struct {
	pages  []*scenePage
	frames map[*formats.GAFFrame]sceneEntry
	pcx    map[*formats.PCX]sceneEntry
	fonts  map[*formats.FNT]*fntAtlas

	uploadBuf []byte
}

// pageImage returns the texture backing an entry's page, or nil for an entry
// that was never placed.
func (a *sceneAtlas) pageImage(e sceneEntry) *ebiten.Image {
	if !e.ok || e.page < 0 || int(e.page) >= len(a.pages) {
		return nil
	}
	return a.pages[e.page].img
}

// allocate reserves a w×h region and returns its placement. A region larger than
// a shared page gets a dedicated page. A degenerate size has no placement.
func (a *sceneAtlas) allocate(w, h int) sceneEntry {
	if w <= 0 || h <= 0 {
		return sceneEntry{}
	}
	if w > sceneAtlasPageSize || h > sceneAtlasPageSize {
		p := &scenePage{w: w, h: h}
		a.pages = append(a.pages, p)
		return sceneEntry{page: int32(len(a.pages) - 1), w: int32(w), h: int32(h), ok: true}
	}
	for i, p := range a.pages {
		if !p.shared {
			continue
		}
		if p.shelfX+w > p.w {
			// Start the next shelf; its top is the tallest entry of this one.
			p.shelfY += p.shelfH
			p.shelfX, p.shelfH = 0, 0
		}
		if p.shelfY+h > p.h {
			continue
		}
		e := sceneEntry{page: int32(i), x: int32(p.shelfX), y: int32(p.shelfY), w: int32(w), h: int32(h), ok: true}
		p.shelfX += w
		if h > p.shelfH {
			p.shelfH = h
		}
		return e
	}
	p := &scenePage{w: sceneAtlasPageSize, h: sceneAtlasPageSize, shared: true}
	a.pages = append(a.pages, p)
	p.shelfX, p.shelfH = w, h
	return sceneEntry{page: int32(len(a.pages) - 1), w: int32(w), h: int32(h), ok: true}
}

// ensurePage allocates an entry's texture on first upload. Pages are created
// lazily so a renderer built before a graphics device exists allocates nothing.
func (a *sceneAtlas) ensurePage(e sceneEntry) *ebiten.Image {
	if !e.ok || e.page < 0 || int(e.page) >= len(a.pages) {
		return nil
	}
	p := a.pages[e.page]
	if p.img == nil {
		p.img = ebiten.NewImage(p.w, p.h)
	}
	return p.img
}

// upload writes an entry's RGBA bytes into its page region. len(buf) must be
// 4*w*h. WritePixels works on a sub-image, so the region is replaced in place
// without disturbing its neighbours.
func (a *sceneAtlas) upload(e sceneEntry, buf []byte) {
	img := a.ensurePage(e)
	if img == nil {
		return
	}
	r := image.Rect(int(e.x), int(e.y), int(e.x+e.w), int(e.y+e.h))
	img.SubImage(r).(*ebiten.Image).WritePixels(buf)
}

// scratch returns a reusable byte buffer of at least n bytes, so a frame's
// surface upload allocates nothing after warm-up (§11.2 "Allocation policy").
func (a *sceneAtlas) scratch(n int) []byte {
	if cap(a.uploadBuf) < n {
		a.uploadBuf = make([]byte, n)
	}
	return a.uploadBuf[:n]
}

// sceneFrameFor returns the atlas placement of one GAF frame, packing and
// uploading it on first use and caching it by pointer identity. The stored bytes
// are buildGAFFrameImage's: index in red, the frame's own Transparent mask
// inverted into green, alpha opaque (C-G4)[fmt gaf][03 §4.4].
func (r *Renderer) sceneFrameFor(f *formats.GAFFrame) sceneEntry {
	if f == nil {
		return sceneEntry{}
	}
	if e, ok := r.scene.frames[f]; ok {
		return e
	}
	fw, fh := int(f.Width), int(f.Height)
	e := r.scene.allocate(fw, fh)
	if e.ok {
		buf := r.scene.scratch(fw * fh * 4)
		np, nt := len(f.Pixels), len(f.Transparent)
		for i := 0; i < fw*fh; i++ {
			p := i * 4
			buf[p+0], buf[p+1], buf[p+2] = 0, 0, 0
			if i < np {
				buf[p+0] = f.Pixels[i]
			}
			// Opaque iff the pixel exists and is not flagged transparent — the
			// same admission blitGAFFrame and uiBlitClippedRaw make per pixel
			// [03 §4.4].
			if i < np && (i >= nt || !f.Transparent[i]) {
				buf[p+1] = 255
			}
			buf[p+3] = 255
		}
		r.scene.upload(e, buf)
	}
	if r.scene.frames == nil {
		r.scene.frames = make(map[*formats.GAFFrame]sceneEntry)
	}
	r.scene.frames[f] = e
	return e
}

// scenePCXFor returns the atlas placement of one PCX frontend background, packed
// on first use. PCX pixels are copied opaquely — no key applies — so every texel
// is flagged opaque in green [fmt pcx][07 "Retail palette contract"].
func (r *Renderer) scenePCXFor(p *formats.PCX) sceneEntry {
	if p == nil {
		return sceneEntry{}
	}
	if e, ok := r.scene.pcx[p]; ok {
		return e
	}
	pw, ph := int(p.Width), int(p.Height)
	e := r.scene.allocate(pw, ph)
	if e.ok {
		buf := r.scene.scratch(pw * ph * 4)
		n := len(p.Pixels)
		for i := 0; i < pw*ph; i++ {
			q := i * 4
			buf[q+0], buf[q+2] = 0, 0
			if i < n {
				buf[q+0] = p.Pixels[i]
			}
			buf[q+1] = 255
			buf[q+3] = 255
		}
		r.scene.upload(e, buf)
	}
	if r.scene.pcx == nil {
		r.scene.pcx = make(map[*formats.PCX]sceneEntry)
	}
	r.scene.pcx[p] = e
	return e
}
