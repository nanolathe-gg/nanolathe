package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Presentation resource limits, not retail constants (GPU design §18.5).
// A bounded cache also supports retained lists from a recent catalog generation.
const markerAtlasLimit = 2048

type markerAtlasUpload struct {
	source *drawlist.MarkerAtlas
	image  *ebiten.Image
	used   uint64
}

func validMarkerAtlas(a *drawlist.MarkerAtlas) bool {
	if a == nil || a.Width <= 0 || a.Height <= 0 || a.Width > markerAtlasLimit || a.Height > markerAtlasLimit || len(a.Pixels) != a.Width*a.Height*4 {
		return false
	}
	for i := 0; i < len(a.Pixels); i += 4 {
		if int(a.Pixels[i])+int(a.Pixels[i+1])+int(a.Pixels[i+2])+int(a.Pixels[i+3]) > 255 {
			return false
		}
	}
	return true
}

func validMarkerRect(m *drawlist.Marker) bool {
	a, q := m.IconAtlas, m.IconRect
	return a != nil && q.X >= 0 && q.Y >= 0 && q.W > 0 && q.H > 0 && int64(q.X)+int64(q.W) <= int64(a.Width) && int64(q.Y)+int64(q.H) <= int64(a.Height)
}

func (r *Renderer) markerImage(a *drawlist.MarkerAtlas) *ebiten.Image {
	r.markerClock++
	oldest := 0
	for i := range r.markerAtlases {
		c := &r.markerAtlases[i]
		if c.source == a {
			c.used = r.markerClock
			return c.image
		}
		if c.used < r.markerAtlases[oldest].used {
			oldest = i
		}
	}
	c := &r.markerAtlases[oldest]
	if c.image != nil {
		// Pending runs may still borrow the evicted source. Submit before retiring it.
		r.submitSchedule()
		c.image.Deallocate()
	}
	*c = markerAtlasUpload{source: a, used: r.markerClock}
	// Remember rejected immutable identities too, avoiding repeated pixel scans.
	if !validMarkerAtlas(a) {
		return nil
	}
	c.image = ebiten.NewImage(a.Width, a.Height)
	// Raw mask channels intentionally are not premultiplied colors. WritePixels
	// preserves their bytes; only the fragment below turns them into color.
	c.image.WritePixels(a.Pixels)
	r.markerWrites++
	return c.image
}

// iconBounds keeps UVs tied to the original square when viewport clipping cuts
// it. All arithmetic precedes narrowing, including malformed extreme records.
func iconBounds(m *drawlist.Marker, w, h int) (dst [4]int, src [4]float32, ok bool) {
	if m.Size <= 0 || m.Alpha == 0 || !validMarkerRect(m) {
		return
	}
	x, y, size := int64(m.X)-int64(m.Size/2), int64(m.Y)-int64(m.Size/2), int64(m.Size)
	x0, y0, x1, y1 := max(x, 0), max(y, 0), min(x+size, int64(w)), min(y+size, int64(h))
	if m.HasClip {
		if m.Clip.W <= 0 || m.Clip.H <= 0 {
			return
		}
		x0, y0 = max(x0, int64(m.Clip.X)), max(y0, int64(m.Clip.Y))
		x1, y1 = min(x1, int64(m.Clip.X)+int64(m.Clip.W)), min(y1, int64(m.Clip.Y)+int64(m.Clip.H))
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	q := m.IconRect
	sx, sy := float32(q.W)/float32(size), float32(q.H)/float32(size)
	dst = [4]int{int(x0), int(y0), int(x1), int(y1)}
	src = [4]float32{float32(q.X) + float32(x0-x)*sx, float32(q.Y) + float32(y0-y)*sy, float32(q.X) + float32(x1-x)*sx, float32(q.Y) + float32(y1-y)*sy}
	return dst, src, true
}

func (r *Renderer) strategicIcon(m *drawlist.Marker) {
	d, uv, ok := iconBounds(m, r.w, r.h)
	if !ok || r.markerShader == nil {
		return
	}
	img := r.markerImage(m.IconAtlas)
	if img == nil {
		return
	}
	imgs := [4]*ebiten.Image{img, r.tables.atlas}
	if !r.sched.beginBlended(schedDest, d[0], d[1], d[2], d[3], imgs, r.markerShader, blendHalfSource, schedReadNone) {
		return
	}
	selected := float32(0)
	if m.Selected {
		selected = 1
	}
	q := m.IconRect
	r.sched.quad(schedDest, float32(d[0]), float32(d[1]), float32(d[2]), float32(d[3]), uv[0], uv[1], uv[2], uv[3],
		[4]float32{float32(m.Index), float32(m.Outline), selected, float32(m.Alpha) / 255},
		[4]float32{float32(q.X) + 0.5, float32(q.Y) + 0.5, float32(q.X) + float32(q.W) - 0.5, float32(q.Y) + float32(q.H) - 0.5})
}

func newMarkerShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(markerShaderSource()))
}

func markerShaderSource() string {
	return `//kage:unit pixels
package main
const palRow = ` + fmt.Sprint(tableRowPAL) + `.0
func palAt(idx float) vec3 {
 return imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5,palRow+0.5)).rgb
}
func sampleMask(p vec2, bounds vec4) vec4 {
 return imageSrc0At(imageSrc0Origin()+clamp(p,bounds.xy,bounds.zw))
}
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
 // Generic contacts in a mixed layer share this shader and atlas binding.
 // Valid icon bounds start at a positive texel center, so a negative minimum
 // unambiguously selects the original flat-palette square/outline primitive.
 if custom.x < 0.0 {
  return vec4(palAt(floor(color.r+0.5))*color.a,color.a)
 }
 // Interpolate independent coverage weights, never palette indices. Tile-local
 // clamping prevents neighboring symbols from entering the filter footprint.
 p := srcPos-imageSrc0Origin()-vec2(0.5)
 base := floor(p)+vec2(0.5)
 f := fract(p)
 top := mix(sampleMask(base,custom),sampleMask(base+vec2(1,0),custom),f.x)
 bottom := mix(sampleMask(base+vec2(0,1),custom),sampleMask(base+vec2(1,1),custom),f.x)
 mask := mix(top,bottom,f.y)
 halo := mask.b*color.b
 alpha := mask.r+mask.g+halo+mask.a
 rgb := palAt(color.r)*mask.r+vec3(mask.g)+palAt(color.g)*halo
 // Disjoint coverage makes this sum premultiplied. Black backing contributes
 // opacity only. Apply fade to all channels together (GPU design §18.5).
 return vec4(rgb,alpha)*color.a
}`
}
