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
	"strings"

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

	// exitRequested lets authored in-game GUI actions terminate the same
	// Ebitengine loop as closing the window. It is presentation state only.
	exitRequested bool

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
	// model diagnostics: structured fallback emitted once per unit, not per frame [ON-08].
	modelErrors    map[string]error    // model name -> last load error (presentation-only)
	modelFallbacks map[uint16]struct{} // unit Slot -> logged fallback diagnostic

	// Feature GAF presentation — sprite class [02 "Feature record"] [03 §5.1] [research/features/feature_rendering.md §2].
	// Loaded lazily from anims/<filename>.gaf via modelFS; cache is presentation-only (I6).
	featureGAFs   map[string]*formats.GAF      // lower filename -> GAF
	featureFrames map[string]*formats.GAFFrame // lower "filename|seqname" -> frame
	featureGACErr map[string]error             // memoised load failures (presentation-only)
	featureCursor map[string]int               // lower "filename|seqname" -> anim cursor index for animating=1 [05]
	featureYSort  bool                         // when true force Y-bucket sort for feature pass [03 §1]

	// Fog overlay — anims/fog.gaf handles, presentation-only [03 §3.3] [rr-16 §9.1].
	fogGAF      *formats.GAF         // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	fogGray     [4]*formats.GAFEntry // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	fogBlack    [4]*formats.GAFEntry // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	fogLoaded   bool
	fogLoadErr  error
	ditheredFog bool // options byte bit6 0x40 DitheredFog [rr-16 §8]

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
		opts:           opts,
		buffer:         buf,
		width:          w,
		height:         h,
		indexed:        make([]uint8, w*h),
		rgba:           make([]byte, w*h*4),
		models:         map[string]*unitModel{},
		modelErrors:    map[string]error{},
		modelFallbacks: map[uint16]struct{}{},
		texIndex:       map[string]texRef{},
		featureGAFs:    map[string]*formats.GAF{},
		featureFrames:  map[string]*formats.GAFFrame{},
		featureGACErr:  map[string]error{},
		featureCursor:  map[string]int{},
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

// RequestExit asks the window backend to terminate after the current update.
// Headless callers can inspect the request without creating a window.
func (c *Client) RequestExit() { c.exitRequested = true }

// ExitRequested reports whether RequestExit has been called.
func (c *Client) ExitRequested() bool { return c.exitRequested }

// SetModelFS installs the VFS for lazy 3DO/texture loads and builds the
// texture-name index. Presentation state only.
func (c *Client) SetModelFS(fs *vfs.FS) {
	c.modelFS = fs
	c.models = map[string]*unitModel{}
	c.texIndex = map[string]texRef{}
	c.modelErrors = map[string]error{}
	c.modelFallbacks = map[uint16]struct{}{}
	c.featureGAFs = map[string]*formats.GAF{}
	c.featureFrames = map[string]*formats.GAFFrame{}
	c.featureGACErr = map[string]error{}
	c.featureCursor = map[string]int{}
	c.fogGAF = nil
	c.fogLoaded = false
	c.fogLoadErr = nil
	for i := range c.fogGray {
		c.fogGray[i] = nil
		c.fogBlack[i] = nil
	}
	c.buildTextureIndex()
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Client) SetDitheredFog(v bool) { c.ditheredFog = v }

// DitheredFog returns the current DitheredFog bit.
func (c *Client) DitheredFog() bool { return c.ditheredFog }

// ensureFogGAF loads anims/fog.gaf lazily and binds Gray1-4/Black1-4 [rr-16 §9.1].
// It is presentation-only and never touches sim state (I6).
func (c *Client) ensureFogGAF() {
	if c.fogLoaded || c.modelFS == nil {
		return
	}
	c.fogLoaded = true
	gaf, err := formats.LoadGAFFile(c.modelFS, "anims/fog.gaf")
	if err != nil {
		c.fogLoadErr = err
		return
	}
	c.fogGAF = gaf
	namesGray := [4]string{"Gray1", "Gray2", "Gray3", "Gray4"}
	namesBlack := [4]string{"Black1", "Black2", "Black3", "Black4"}
	for i, n := range namesGray {
		if e, ok := gaf.Find(n); ok {
			c.fogGray[i] = e
		}
	}
	for i, n := range namesBlack {
		if e, ok := gaf.Find(n); ok {
			c.fogBlack[i] = e
		}
	}
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

// featureGAFFor loads anims/<filename>.gaf lazily and returns the GAF.
// Filename is the TDF `filename` stem (e.g. "trees") without extension [02 "Feature record"].
// The path is lower-cased per VFS canonical rules (I1). Presentation-only (I6).
func (c *Client) featureGAFFor(filename string) (*formats.GAF, error) {
	if filename == "" || c.modelFS == nil {
		return nil, nil
	}
	key := strings.ToLower(strings.TrimSpace(filename))
	if key == "" {
		return nil, nil
	}
	if gaf, ok := c.featureGAFs[key]; ok {
		return gaf, nil
	}
	if err, ok := c.featureGACErr[key]; ok {
		return nil, err
	}
	path := "anims/" + key + ".gaf"
	gaf, err := formats.LoadGAFFile(c.modelFS, path)
	if err != nil {
		c.featureGACErr[key] = err
		return nil, err
	}
	c.featureGAFs[key] = gaf
	return gaf, nil
}

// featureFrameFor resolves seqName inside filename's GAF, with per-frame durations.
// For static features it returns the first frame; for animated features the cursor
// is advanced via c.animClock using the entry's per-frame Value ticks [03 §4.4].
// Returns nil on missing filename/seq or load failure (caller falls back to rect) [05 "Feature catalog and placement"].
func (c *Client) featureFrameFor(f snapshot.FeatureView, shadow bool) *formats.GAFFrame {
	filename := f.Filename
	seq := f.SeqName
	if shadow {
		seq = f.SeqNameShad
	}
	if filename == "" || seq == "" {
		return nil
	}
	gaf, err := c.featureGAFFor(filename)
	if err != nil || gaf == nil {
		return nil
	}
	entry, ok := gaf.Find(seq)
	if !ok || entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	if !f.Animating {
		if entry.Frames[0].Frame != nil {
			return entry.Frames[0].Frame
		}
		return nil
	}
	// Animated: cycle through frames using per-frame Value ticks (whole ticks) [03 §4.4].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// sprites from the global animClock (presentation-only, I6).
	total := 0
	for _, fr := range entry.Frames {
		d := int(fr.Value)
		if d < 1 {
			d = 1
		}
		total += d
	}
	if total <= 0 {
		if entry.Frames[0].Frame != nil {
			return entry.Frames[0].Frame
		}
		return nil
	}
	t := ((c.animClock % total) + total) % total
	acc := 0
	for _, fr := range entry.Frames {
		d := int(fr.Value)
		if d < 1 {
			d = 1
		}
		acc += d
		if t < acc {
			return fr.Frame
		}
	}
	if entry.Frames[len(entry.Frames)-1].Frame != nil {
		return entry.Frames[len(entry.Frames)-1].Frame
	}
	return nil
}

// blitGAFFrame blits a GAF indexed frame to the indexed framebuffer at
// top-left (dstX,dstY) = anchor - frame offsets, clipped to the viewport [03 §4.4] [fmt gaf].
// It copies opaque indexed pixels directly (palette mapping happens at present time, C7).
// Shadow path darkens the underlying terrain via PALETTE.SHD instead of copying
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// `SHD[row*256 + dstPix]` where row 0 is near-black (mean -97) and the mask is
// the shadow GAF's opaque pixels; with dither the effect is translucent. For
// feature shadows `shadTrans` selects the dithered translucent path (checker)
// vs solid darken. Row 8 is a mid-dark row that is visible but not pure black.
func (c *Client) blitGAFFrame(frame *formats.GAFFrame, dstX, dstY int, isShadow bool, shadTrans bool) {
	if frame == nil || c.indexed == nil {
		return
	}
	w := c.width
	h := c.height
	// Top-left already adjusted for XOffset/YOffset by caller; frame origin is at dstX,dstY.
	fw := int(frame.Width)
	fh := int(frame.Height)
	if fw <= 0 || fh <= 0 {
		return
	}
	// Clip against viewport.
	srcX0 := 0
	srcY0 := 0
	if dstX < 0 {
		srcX0 = -dstX
		dstX = 0
	}
	if dstY < 0 {
		srcY0 = -dstY
		dstY = 0
	}
	if dstX >= w || dstY >= h {
		return
	}
	copyW := fw - srcX0
	copyH := fh - srcY0
	if dstX+copyW > w {
		copyW = w - dstX
	}
	if dstY+copyH > h {
		copyH = h - dstY
	}
	if copyW <= 0 || copyH <= 0 {
		return
	}
	// Shadow stencil darkens the destination (ground) where the shadow GAF
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// where the mask is the shadow GAF's opaque pixels; the feature path at
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// but both are stencil darkens, not sprite copies. Row 0 is near-black,
	// row 8 is mid-dark; retail's exact row for feature shadows is not fully
	// established, but the effect is a solid darkening, not a checker dither.
	// User checked retail and saw no dithering, so we use solid rows.
	for y := 0; y < copyH; y++ {
		srcY := srcY0 + y
		dstYPos := dstY + y
		dstOff := dstYPos*w + dstX
		srcRow := srcY*fw + srcX0
		for x := 0; x < copyW; x++ {
			idx := srcRow + x
			if idx < 0 || idx >= len(frame.Pixels) {
				continue
			}
			if idx < len(frame.Transparent) && frame.Transparent[idx] {
				continue
			}
			if isShadow {
				// Use the shadow GAF as a mask: darken the underlying terrain pixel.
				dstIdx := dstOff + x
				if dstIdx < 0 || dstIdx >= len(c.indexed) {
					continue
				}
				destPix := c.indexed[dstIdx]
				var darkPix byte
				if c.pal != nil {
					// shadTrans selects translucent (lighter) vs opaque (darker) row.
					// Row 16 is near-identity for brightening, rows 0-14 darken.
					// Use row 8 for translucent, row 4 for opaque as a plausible
					// retail split; both are solid, no checker.
					row := 8
					if !shadTrans {
						row = 4
					}
					darkPix = c.pal.Shade[row][destPix]
				} else {
					darkPix = 0
				}
				c.indexed[dstIdx] = darkPix
				continue
			}
			pix := frame.Pixels[idx]
			c.indexed[dstOff+x] = pix
		}
	}
}

// blitFogGAF blits a fog GAF frame for the cell whose screen rect origin is
// (dstX,dstY) [rr-16 §6.2]. Retail subtracts the frame's signed 16-bit
// XOffset/YOffset anchor fields from the destination before clipping
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// the quadrant geometry of the 14 fog frames lives entirely in those offsets.
// It copies opaque indexed pixels directly (palette mapping at present time
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// no checker.
func (c *Client) blitFogGAF(frame *formats.GAFFrame, dstX, dstY int) {
	if frame == nil || c.indexed == nil {
		return
	}
	dstX -= int(frame.XOffset) // retail anchor: dest = cell origin − frame offset
	dstY -= int(frame.YOffset)
	w := c.width
	h := c.height
	fw := int(frame.Width)
	fh := int(frame.Height)
	if fw <= 0 || fh <= 0 {
		return
	}
	srcX0, srcY0 := 0, 0
	if dstX < 0 {
		srcX0 = -dstX
		dstX = 0
	}
	if dstY < 0 {
		srcY0 = -dstY
		dstY = 0
	}
	if dstX >= w || dstY >= h {
		return
	}
	copyW := fw - srcX0
	copyH := fh - srcY0
	if dstX+copyW > w {
		copyW = w - dstX
	}
	if dstY+copyH > h {
		copyH = h - dstY
	}
	if copyW <= 0 || copyH <= 0 {
		return
	}
	for y := 0; y < copyH; y++ {
		srcY := srcY0 + y
		dstYPos := dstY + y
		dstOff := dstYPos*w + dstX
		srcRow := srcY*fw + srcX0
		for x := 0; x < copyW; x++ {
			idx := srcRow + x
			if idx < 0 || idx >= len(frame.Pixels) {
				continue
			}
			if idx < len(frame.Transparent) && frame.Transparent[idx] {
				continue
			}
			pix := frame.Pixels[idx]
			c.indexed[dstOff+x] = pix
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Same frame-offset anchoring as blitFogGAF; it skips every other pixel in a
// 2×2 checker seeded by camera parity (camX+camZ)&1 [rr-16 §6.2].
func (c *Client) blitFogGAFPatterned(frame *formats.GAFFrame, dstX, dstY int) {
	if frame == nil || c.indexed == nil {
		return
	}
	dstX -= int(frame.XOffset) // retail anchor: dest = cell origin − frame offset
	dstY -= int(frame.YOffset)
	w := c.width
	h := c.height
	fw := int(frame.Width)
	fh := int(frame.Height)
	if fw <= 0 || fh <= 0 {
		return
	}
	parity := int32(0)
	if c.cam != nil {
		parity = (c.cam.X + c.cam.Z) & 1
	}
	srcX0, srcY0 := 0, 0
	if dstX < 0 {
		srcX0 = -dstX
		dstX = 0
	}
	if dstY < 0 {
		srcY0 = -dstY
		dstY = 0
	}
	if dstX >= w || dstY >= h {
		return
	}
	copyW := fw - srcX0
	copyH := fh - srcY0
	if dstX+copyW > w {
		copyW = w - dstX
	}
	if dstY+copyH > h {
		copyH = h - dstY
	}
	if copyW <= 0 || copyH <= 0 {
		return
	}
	for y := 0; y < copyH; y++ {
		srcY := srcY0 + y
		dstYPos := dstY + y
		dstOff := dstYPos*w + dstX
		srcRow := srcY*fw + srcX0
		for x := 0; x < copyW; x++ {
			// Checker: skip where (screenX+screenY+parity)&1==0 approximates retail's 2×2 block via AND 1 / ADD 2.
			if ((int32(dstX+x) + int32(dstYPos) + parity) & 1) == 0 {
				continue
			}
			idx := srcRow + x
			if idx < 0 || idx >= len(frame.Pixels) {
				continue
			}
			if idx < len(frame.Transparent) && frame.Transparent[idx] {
				continue
			}
			pix := frame.Pixels[idx]
			c.indexed[dstOff+x] = pix
		}
	}
}

// featureScreenPos computes the retail feature screen anchor [research/features/feature_rendering.md §3.2].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// The snapshot already carries world-centered X,Z and Y=coarseHeight; we reuse WorldToScreen
// for the shear and add footprint-half offset explicitly for non-centered callers.
// When terrain is available the Y uses the averaged heights at the footprint's four corners
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Client) featureScreenPos(f snapshot.FeatureView) (int32, int32) {
	if c.cam == nil {
		// Fallback deterministic when no camera: use world high word directly [03 §2.5].
		wx := int32(int64(f.X) >> 16)
		wz := int32(int64(f.Z) >> 16)
		wy := int32(int64(f.Y) >> 16)
		return wx, wz - (wy >> 1)
	}
	// Prefer terrain height averaging when terrain is bound [03 §2.2][03 §2.3].
	if c.terrain != nil && f.FootX > 0 && f.FootZ > 0 {
		cx := f.CX
		cz := f.CZ
		footX := int32(f.FootX)
		footZ := int32(f.FootZ)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// h0 = cell, h1 = cell+W*0xD+4 next-X, h2 = next-Z, h3 = diag.
		// For foot >1 the footprint center is used, but height avg still over covered cells' heights.
		// Use coarse average over footprint via CoarseHeightAt as approximation for multi-cell.
		// For single-cell features this reduces to the four-corner average around anchor.
		// For now use snapshot Y (coarse) via WorldToScreen which applies shear.
		_ = footX
		_ = footZ
		_ = cx
		_ = cz
	}
	sx, sy := c.cam.WorldToScreen(f.X, f.Y, f.Z)
	// Rebase from the observed beam origin (128,32) to the shell viewport origin
	// (0,0) used by BlitTerrainOrigin for full-window draws [03 §2.5] C1 [PLAN_04A C1].
	// Without this, features would be 128,32 southeast of their terrain tiles.
	sx -= camera.OriginX
	sy -= camera.OriginY
	return sx, sy
}

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
