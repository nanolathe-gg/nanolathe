// Package client is the window and frame loop.
//
// Presentation is the one deliberate divergence from retail's draw path, which
// samples committed state with no interpolation [03 §2.4]. The sim never reads
// wall-clock time, input state, camera, or renderer state (I6). Client owns the
// update boundary but never advances a clock or mutates sim state itself; the
// injected Step callback owns those effects (PLAN_03 C15/C16, PLAN_04A C9).
//
// The presentation backend is a pure-Go software framebuffer: composeIndexed
// produces the indexed image, the palette expands it to RGBA once per frame,
// and Ebitengine uploads and presents it. No sim package imports this package
// (I6).
package client

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// Options configures a Client. Step is the injected owner of clock, sub-ticks,
// and snapshot publish; it is called first inside each update (C9). The
// window, palette, camera, and snapshot dependencies are carried here.
type Options struct {
	Step func(delta float64) // injected owner of clock/sub-ticks/snapshot publish (C9)

	// Presentation snapshot source. If nil, an empty buffer is used.
	Buffer *snapshot.Buffer

	// Negotiated window size: the HUD panel arithmetic in phase 12 depends on
	// it, so it is not invented per frame. Zero means 640×480.
	Width, Height int
	Title         string

	// DebugOverlay is an explicit developer diagnostic switch. Retail battle
	// frames do not contain the Nanolathe tick/camera text or verification bar;
	// keep those diagnostics opt-in so attaching a font never changes the
	// retail surface [03 §7.1] C8.
	DebugOverlay bool

	// Headless skips window creation entirely (C11). Nothing above becomes
	// reachable from --headless; RunGame returns immediately.
	Headless bool
}

// Client is the software framebuffer, palette, camera, and snapshot reader. It
// keeps retail's 8-bit indexed renderer and presents one RGBA upload per
// frame. Rendering interpolates Previous→Current at render fraction (I6).
type Client struct {
	opts Options

	// Overlay draws battle-view chrome after world units. Presentation only
	// [I6]. Debug text is separately opt-in through Options.DebugOverlay.
	Overlay func(c *Client)
	buffer  *snapshot.Buffer

	width, height int
	indexed       []uint8
	rgba          []byte
	img           *ebiten.Image // backend-presented frame; lazily sized

	// Accumulated presented seconds drive the render interpolation fraction;
	// wall-clock time never reaches the sim (I6) — only this presentation
	// fraction derives from it.
	runtime float64

	// Palette fallback (WU-04A-2 replaces this with full tables). Every indexed
	// pixel goes logical → physical through the 256-byte table at present time
	// only (C7). GUIPAL.PAL is retained by palette.Tables only for GUI semantic
	// color fields; it is never an alternate pixel route.
	base    [256][4]byte
	logical [256]byte
	pal     *palette.Tables

	// World / camera for Gate 1 terrain viewer [PLAN_04A]. When set, Frame
	// draws real TNT terrain instead of the placeholder gradient.
	terrain *world.Terrain
	cam     *camera.Camera
	fnt     *formats.FNT

	modelFS   *vfs.FS
	models    map[string]*unitModel
	texIndex  map[string]texRef
	animClock int // texture-animation clock in sim frames

	// Software cursor, drawn last over the composed surface [07 §8].
	cursors *Cursors

	in InputState

	// Audio is presentation-only and never feeds simulation [03 §8.3] C19 I6.
	// Queue drain runs once per rendered frame outside simulation C18;
	// variant selection uses CRT stream [03 §8.3] C17; positional helper uses
	// audience gating (mode &2) and viewport pan/attenuation [03 §8.3].
	audioQueue    *audio.Queue
	audioCache    *audio.SampleCache
	audioMusic    *audio.Controller
	audioViewport audio.Viewport
	audioFrame    uint32
}

// New creates a client. It allocates the indexed framebuffer at the negotiated
// logical size and prepares fallback palette tables. Window creation happens
// in RunGame; in headless mode no window is ever created.
func New(opts Options) (*Client, error) {
	w := opts.Width
	h := opts.Height
	if w <= 0 {
		w = 640
	}
	if h <= 0 {
		h = 480
	}
	buf := opts.Buffer
	if buf == nil {
		buf = &snapshot.Buffer{}
	}
	c := &Client{
		opts:    opts,
		buffer:  buf,
		width:   w,
		height:  h,
		indexed: make([]uint8, w*h),
		rgba:    make([]byte, w*h*4),
	}
	c.in = *newInputState()
	// Fallback palette: grayscale base and identity logical table. This keeps
	// the framebuffer path valid before WU-04A-2 loads PALETTE.PAL/ALP/LHT/SHD
	// and the 256-byte logical→physical lookup [03 §4.3] (C7).
	for i := 0; i < 256; i++ {
		c.base[i][0] = byte(i)
		c.base[i][1] = byte(i)
		c.base[i][2] = byte(i)
		c.base[i][3] = 255
		c.logical[i] = byte(i)
	}
	return c, nil
}

// SetTerrain sets the world terrain for Gate 1 drawing [PLAN_04A C1].
func (c *Client) SetTerrain(t *world.Terrain) { c.terrain = t }

// SetCamera sets the camera for Gate 1 pan [07 §10].
func (c *Client) SetCamera(cam *camera.Camera) { c.cam = cam }

// SetPalette installs real palette tables [03 §4.3] C7.
func (c *Client) SetPalette(p *palette.Tables) {
	c.pal = p
	if p != nil {
		c.base = p.Base
		c.logical = p.Logical
		c.ResolveUnitStyle()
	}
}

// SetFNT sets the debug font [03 §7.1] C8.
func (c *Client) SetFNT(fnt *formats.FNT) { c.fnt = fnt }

// SetSnapshot repoints presentation at another published buffer — used when
// the shell transitions from front-end menus into a live battle session [I6].
func (c *Client) SetSnapshot(b *snapshot.Buffer) {
	if b != nil {
		c.buffer = b
	}
}

// Size returns the negotiated logical framebuffer size.
func (c *Client) Size() (int, int) { return c.width, c.height }

// Buffer exposes the presentation snapshot source (diagnostics publish into
// it directly; the session path owns it in normal play).
func (c *Client) Buffer() *snapshot.Buffer { return c.buffer }

// SetModelFS installs the VFS for lazy 3DO/texture loads and builds the
// texture-name index. Presentation state only.
func (c *Client) SetModelFS(fs *vfs.FS) {
	c.modelFS = fs
	c.models = map[string]*unitModel{}
	c.texIndex = map[string]texRef{}
	c.buildTextureIndex()
}

// ComposeFrame reads the published buffer, composes one frame at alpha 1.0,
// and returns it as an RGBA image. It works headless — presentation never
// requires a window (I6) — and is the basis of the --shot diagnostic path.
func (c *Client) ComposeFrame() *image.RGBA {
	prev, cur, ok := c.buffer.Read()
	c.composeIndexed(1.0, prev, cur, ok)
	c.drawCursor() // cursor last, over the composed surface [07 §8]
	c.convertIndexedToRGBA()
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	copy(img.Pix, c.rgba)
	return img
}

// Input exposes the per-frame input snapshot for the windowed paths. Edge
// flags are valid for exactly one Update; held state persists while down.
func (c *Client) Input() *InputState { return &c.in }

// computeAlpha derives the render interpolation fraction:
// frac(runtime*30), clamped to [0,1] (PLAN_03 C16).
func (c *Client) computeAlpha() float32 {
	ticks := c.runtime * 30.0
	frac := ticks - float64(int64(ticks))
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}
	if frac != frac { // NaN
		return 0
	}
	return float32(frac)
}
