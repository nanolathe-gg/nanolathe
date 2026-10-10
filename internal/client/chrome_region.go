package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Magnified chrome (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
//
// A chrome region is laid out on a smaller virtual surface and replayed by the
// modern executor through the same affine transform the world zoom uses:
// framebuffer = virtual*Scale + Offset. Integer scales with nearest sampling
// are plain pixel magnification. The classic executor ignores the markers, so
// the host opens a region only when Enhanced presentation is active.

type chromeRegion struct {
	open     bool
	scale    int32
	offX     int32
	offY     int32
	w, h     int
	recorded bool
}

// ChromeRegion is a magnified chrome transform: a point (x, y) on the virtual
// surface lands at (x*Scale+OffsetX, y*Scale+OffsetY) in the framebuffer.
type ChromeRegion struct {
	Scale            int32
	OffsetX, OffsetY int32
}

// Identity reports whether the region would draw exactly as unmagnified chrome.
func (r ChromeRegion) Identity() bool {
	return r.Scale <= 1 && r.OffsetX == 0 && r.OffsetY == 0
}

// VirtualSize returns the virtual surface that covers a w×h framebuffer from
// the region's origin. The width rounds up, so a strip stamped to the virtual
// edge reaches the framebuffer's; the height rounds down, so a layout that
// fits the virtual height fits the framebuffer and a bottom-aligned region
// ends on its last row.
func (r ChromeRegion) VirtualSize(w, h int) (int, int) {
	k := int(max(r.Scale, 1))
	return max((w-int(r.OffsetX)+k-1)/k, 0), max((h-int(r.OffsetY))/k, 0)
}

// ToVirtual maps a framebuffer point onto the virtual surface. Division floors,
// so every framebuffer pixel of a magnified virtual pixel maps back to it.
func (r ChromeRegion) ToVirtual(x, y int32) (int32, int32) {
	k := max(r.Scale, 1)
	return floorDiv32(x-r.OffsetX, k), floorDiv32(y-r.OffsetY, k)
}

func floorDiv32(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// BeginChromeRegion opens a magnified chrome region. Commands recorded until
// EndChromeRegion are in virtual coordinates; inside it the UI helpers clip
// against the virtual surface. An identity region records nothing, so
// unmagnified chrome keeps its exact recording.
func (c *Client) BeginChromeRegion(r ChromeRegion) {
	if c == nil || c.chrome.open || c.worldOverlay {
		return
	}
	w, h := r.VirtualSize(c.width, c.height)
	c.chrome = chromeRegion{open: true, scale: max(r.Scale, 1), offX: r.OffsetX, offY: r.OffsetY, w: w, h: h}
	if r.Identity() {
		return
	}
	c.chrome.recorded = true
	c.list.RecordWorld(c.chromeSpace(true))
}

// EndChromeRegion closes the region BeginChromeRegion opened.
func (c *Client) EndChromeRegion() {
	if c == nil || !c.chrome.open {
		return
	}
	if c.chrome.recorded {
		c.list.RecordWorld(c.chromeSpace(false))
	}
	c.chrome = chromeRegion{}
}

// ChromeSize is the surface the UI helpers draw on: the open region's virtual
// surface, otherwise the framebuffer.
func (c *Client) ChromeSize() (int, int) {
	if c == nil {
		return 0, 0
	}
	if c.chrome.open {
		return c.chrome.w, c.chrome.h
	}
	return c.width, c.height
}

// chromeSpace reuses the world-space marker: the executor applies its Factor
// and offsets to every command between the pair. The record extent is the
// virtual surface, which bounds the region's text.
func (c *Client) chromeSpace(begin bool) drawlist.WorldSpace {
	return drawlist.WorldSpace{
		Begin:    begin,
		Chrome:   true,
		Zoom:     camera.ZoomUnit,
		Step:     camera.ViewScaleNative,
		Factor:   float32(c.chrome.scale),
		OffsetX:  float32(c.chrome.offX),
		OffsetY:  float32(c.chrome.offY),
		RecordW:  int32(c.chrome.w),
		RecordH:  int32(c.chrome.h),
		Viewport: c.battleViewportRect(),
	}
}
