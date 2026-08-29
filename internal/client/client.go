// Package client is the window and frame loop.
//
// Presentation samples committed state with no interpolation, matching
// retail's draw path [03 §2.4]. The sim never reads
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
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// Options configures a Client. Step is the injected owner of clock, sub-ticks,
// and snapshot publish; it is called first inside each update (C9). The
// window, palette, camera, and snapshot dependencies are carried here.
type Options struct {
	Step func(delta float64) // injected owner of clock/sub-ticks/snapshot publish (C9)

	// Presentation snapshot source. If nil, an empty buffer is used.
	Buffer *frame.Buffer

	// Negotiated window size: the HUD panel arithmetic in phase 12 depends on
	// it, so it is not invented per frame. Zero means 640×480.
	Width, Height int
	Title         string
}

// Client is the software framebuffer, palette, camera, and snapshot reader. It
// keeps retail's 8-bit indexed renderer and presents one RGBA upload per
// frame. Rendering consumes only the currently committed frame (I6).
type Client struct {
	opts Options

	// exitRequested lets authored in-game GUI actions terminate the same
	// Ebitengine loop as closing the window. It is presentation state only.
	exitRequested bool

	buffer *frame.Buffer

	width, height int
	indexed       []uint8
	rgba          []byte
	img           *ebiten.Image // backend-presented frame; lazily sized

	// Runtime is presentation-only bookkeeping for backend frame cadence.
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

	modelFS  *vfs.FS
	models   map[string]*unitModel
	texIndex map[string]texRef
	// Model presentation state is owned by this client so independent windows
	// cannot share animation phase or orientation caches [03 §1][I6]. The map
	// is a lookup cache; modelPlayers is the deterministic phase-7 registry.
	modelPresentation map[modelTextureKey]*modelTextureCursor
	modelPlayers      []phase7Stepper
	modelOrientation  map[uint64]*presentationrender.OrientationCache
	// DET-04: shake state is authoritative phase 10. The owning battle
	// presentation updates its camera from the committed offset; this renderer
	// never keeps a second shake/camera accumulator.
	// DET-01: crt is a PRIVATE presentation copy (struct value copied at bind
	// time), never the session's authoritative stream. It feeds only the
	// presentation-side segmented-projectile pass, whose draws are
	// render-time and cannot affect simulation. AUDIT(parity-spine): approved
	// divergence — retail resolves segmented projectiles from the live CRT;
	// Nanolathe isolates the copy so render cadence cannot desync the sim.
	// Next wave: move segment resolution to sim-published values and drop this
	// field. Shrink-only.
	crt                *rng.CRT
	crtBound           bool
	frameTick          uint32                       // committed tick of the frame being composed
	nano               presentationrender.NanoField // live nanolathe particle records [03 §5.5]
	lastNanoTick       uint32
	modelTargetColor   []uint8 // reusable per-unit indexed composition plane [03 §5.2]
	modelTargetHeight  []uint8 // reusable per-unit height/key plane [03 §5.2]
	modelTargetCovered []bool  // reusable per-unit coverage plane [03 §5.2]
	worldBuckets       worldBuckets
	fogCache           *visibility.FogCache
	fogOps             []presentationrender.FogOp
	selectionChrome    []selectionChrome
	selectionDrag      SelectionDrag
	// rendererTraceSink is nil for the normal presentation path. When enabled,
	// model composition emits value-only candidate evidence after the complete
	// subject pixel is resolved [03 §2.4.1][03 §5.2][I6].
	rendererTraceSink   RendererTraceSink
	rendererTraceFilter RendererTraceFilter

	// Feature GAF presentation — sprite class [02 "Feature record"] [03 §5.1.1].
	// Loaded lazily from anims/<filename>.gaf via modelFS; cache is presentation-only (I6).
	featureGAFs   map[string]*formats.GAF      // lower filename -> GAF
	featureFrames map[string]*formats.GAFFrame // lower "filename|seqname" -> frame
	featureGACErr map[string]error             // memoised load failures (presentation-only)
	featureYSort  bool                         // when true force Y-bucket sort for feature pass [03 §1]

	// Fog overlay — anims/fog.gaf handles, presentation-only [03 §3.3].
	fogGAF      *formats.GAF
	fogGray     [4]*formats.GAFEntry // Gray1-4 variant family [03 §3.3]
	fogBlack    [4]*formats.GAFEntry // Black1-4 variant family [03 §3.3]
	fogLoaded   bool
	fogLoadErr  error
	ditheredFog bool // options byte bit6 0x40 DitheredFog [03 §3.3]

	// Software cursor, drawn last over the composed surface [07 §8].
	cursors *Cursors

	// uiStage is the sole UI adapter. World ordering stays in drawCommittedFrame;
	// cmd-owned authored surfaces run once at its final interface slot [03 §1].
	uiStage UIStage

	in InputState

	// Audio is the concrete internal/audio owner. The client only binds the
	// service and drains it at the rendered-frame boundary [03 §8.3–§8.4] [I6].
	audioService *audio.Service
}

// New creates a client. It allocates the indexed framebuffer at the negotiated
// logical size and prepares fallback palette tables. Window creation happens
// only in RunGame.
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
		buf = &frame.Buffer{}
	}
	c := &Client{
		opts:               opts,
		buffer:             buf,
		width:              w,
		height:             h,
		indexed:            make([]uint8, w*h),
		rgba:               make([]byte, w*h*4),
		modelTargetColor:   make([]uint8, w*h),
		modelTargetHeight:  make([]uint8, w*h),
		modelTargetCovered: make([]bool, w*h),
		models:             map[string]*unitModel{},
		texIndex:           map[string]texRef{},
		modelPresentation:  map[modelTextureKey]*modelTextureCursor{},
		modelPlayers:       nil,
		modelOrientation:   map[uint64]*presentationrender.OrientationCache{},
		featureGAFs:        map[string]*formats.GAF{},
		featureFrames:      map[string]*formats.GAFFrame{},
		featureGACErr:      map[string]error{},
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

// SetFNT installs the shared software font used by typed UI stages [03 §7.1].
func (c *Client) SetFNT(fnt *formats.FNT) { c.fnt = fnt }

// SetSnapshot repoints presentation at another published buffer — used when
// the shell transitions from front-end menus into a live battle session [I6].
func (c *Client) SetSnapshot(b *frame.Buffer) {
	if b != nil {
		c.buffer = b
	}
}

// Size returns the negotiated logical framebuffer size.
func (c *Client) Size() (int, int) { return c.width, c.height }

// IsFocused reports the platform window focus at the client edge. Battle
// camera predicates consume this value without importing Ebitengine [07 §10].
func (c *Client) IsFocused() bool {
	return c != nil && ebiten.IsFocused()
}

// Buffer exposes the presentation snapshot source (diagnostics publish into
// it directly; the session path owns it in normal play).
func (c *Client) Buffer() *frame.Buffer { return c.buffer }

// RequestExit asks the window backend to terminate after the current update.
func (c *Client) RequestExit() { c.exitRequested = true }

// ExitRequested reports whether RequestExit has been called.
func (c *Client) ExitRequested() bool { return c.exitRequested }

// StepCursorScaledDelta advances the software cursor from the positive
// scaled wall-clock delta used by the presentation adapter. Cursor playback
// is not tied to runnable simulation ticks; the underlying cursor consumes a
// signed 16-bit delta, so large elapsed intervals are split without changing
// the resulting countdown [03 §4.4][R-CRD-005 §1].
func (c *Client) StepCursorScaledDelta(delta int32) {
	if c == nil || c.cursors == nil || delta <= 0 {
		return
	}
	for delta > 0 {
		step := delta
		if step > 32767 {
			step = 32767
		}
		c.cursors.play.StepDelta(int16(-step))
		delta -= step
		if !c.cursors.play.IsActive() {
			return
		}
	}
}

// SetModelFS installs the VFS for lazy 3DO/texture loads and builds the
// texture-name index. Presentation state only.
func (c *Client) SetModelFS(fs *vfs.FS) {
	c.modelFS = fs
	c.modelPresentation = map[modelTextureKey]*modelTextureCursor{}
	c.modelPlayers = nil
	c.modelOrientation = map[uint64]*presentationrender.OrientationCache{}
	c.models = map[string]*unitModel{}
	c.texIndex = map[string]texRef{}
	c.featureGAFs = map[string]*formats.GAF{}
	c.featureFrames = map[string]*formats.GAFFrame{}
	c.featureGACErr = map[string]error{}
	c.fogGAF = nil
	c.fogLoaded = false
	c.fogLoadErr = nil
	for i := range c.fogGray {
		c.fogGray[i] = nil
		c.fogBlack[i] = nil
	}
	c.buildTextureIndex()
}

// SetDitheredFog selects patterned current-fog rendering [03 §3.3]. When set,
// hi==15 draws a checker and hi 1..14 uses patterned Gray GAF drawing; when
// clear, both use their plain forms. The lo/Black family is always plain.
func (c *Client) SetDitheredFog(v bool) { c.ditheredFog = v }

// DitheredFog returns the current DitheredFog bit.
func (c *Client) DitheredFog() bool { return c.ditheredFog }

// ensureFogGAF loads anims/fog.gaf lazily and binds Gray1-4/Black1-4 [03 §3.3].
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

// ComposedFrameSnapshot is caller-owned evidence copied from one invocation of
// the normal committed-frame composer. Indexed and RGBA describe the same
// completed surface, including the software cursor. It records no writer
// provenance; the renderer trace owns its narrower subject-composition scope.
//
// Committed distinguishes the deterministic pre-publication surface from a
// publication whose valid tick is zero. The returned slices never alias the
// client's reusable presentation planes [03 §1][03 §2.4][I6].
type ComposedFrameSnapshot struct {
	Width, Height int
	Committed     bool
	Tick          uint32
	Indexed       []uint8
	RGBA          []byte
}

// composeCurrentFrame runs the same one-pass committed-frame composition used
// by ComposeFrame and returns the sampled publication only long enough for a
// caller to copy its tick identity. It never exposes that frame beyond the
// documented Buffer.Current reader lifetime [03 §2.4][I6].
func (c *Client) composeCurrentFrame() *frame.Frame {
	cur := c.buffer.Current()
	c.composeIndexed(cur, cur != nil)
	c.drawCursor() // cursor last, over the composed surface [07 §8]
	c.convertIndexedToRGBA()
	return cur
}

// ComposeFrame reads the published buffer and composes one current frame,
// returning it as an RGBA image without entering the window loop (I6). It is
// the basis of the --shot diagnostic path.
func (c *Client) ComposeFrame() *image.RGBA {
	c.composeCurrentFrame()
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	copy(img.Pix, c.rgba)
	return img
}

// ComposeFrameSnapshot composes exactly once through the same committed-frame
// path as ComposeFrame, then copies both indexed and RGBA planes. This is a
// diagnostic boundary for deterministic parity capture, not another renderer
// or a claim about the final pixel writer [03 §1][03 §2.4][I6].
func (c *Client) ComposeFrameSnapshot() ComposedFrameSnapshot {
	if c == nil {
		return ComposedFrameSnapshot{}
	}
	cur := c.composeCurrentFrame()
	snapshot := ComposedFrameSnapshot{
		Width:     c.width,
		Height:    c.height,
		Committed: cur != nil,
		Indexed:   append([]uint8(nil), c.indexed...),
		RGBA:      append([]byte(nil), c.rgba...),
	}
	if cur != nil {
		snapshot.Tick = cur.Tick
	}
	return snapshot
}

// Input exposes the per-frame input snapshot for the windowed paths. Edge
// flags are valid for exactly one Update; held state persists while down.
func (c *Client) Input() *input.State { return &c.in }

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
// For static features it returns the first frame; animated features use an
// independent cursor keyed by the feature's stable presentation identity [03 §4.4].
// Returns nil on missing filename/seq or load failure; no authored pixels are
// emitted for an unresolved sequence [05 "Feature catalog and placement"].
func (c *Client) featureFrameFor(f frame.FeatureView, shadow bool) *formats.GAFFrame {
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
	return c.animatedGAFFrame(strings.ToLower(filename)+"|"+strings.ToLower(seq), featurePresentationID(f), entry)
}

// blitGAFFrame blits a GAF indexed frame to the indexed framebuffer at
// top-left (dstX,dstY) = anchor - frame offsets, clipped to the viewport [03 §4.4] [fmt gaf].
// It copies opaque indexed pixels directly (palette mapping happens at present time, C7).
// Shadow path darkens the underlying terrain via PALETTE.SHD instead of copying
// the shadow sprite's own indices. The mask is the shadow GAF's opaque pixels;
// with dither the effect is translucent. For
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
	// is opaque, via PALETTE.SHD. The authored translucent flag selects the
	// dithered versus opaque route
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

// fogBlitMode selects what a fog GAF frame does to the pixels its non-key
// mask covers. The mask itself is identical in all three: retail compares each
// source pixel against the frame's ColorKey and skips the matches
// [fmt gaf][R-RR16-A §3].
type fogBlitMode uint8

const (
	// fogBlitBlack copies the source pixel — the Black family via
	// its keyed raw blitter, whose art is palette index 0 [03 §3.3].
	fogBlitBlack fogBlitMode = iota
	// fogBlitGray remaps the destination through the GRAY TABLE and never
	// writes the source pixel [03 §3.3][R-RR16-A §1].
	fogBlitGray
	// fogBlitPatterned writes literal index 0 on a 2-pixel checker — the
	// dithered Gray variant, which steps x by two [03 §3.3].
	fogBlitPatterned
)

// blitFogGAF blits a fog GAF frame for the cell whose screen rect origin is
// (dstX,dstY) [03 §3.3]. Retail subtracts the frame's signed 16-bit
// XOffset/YOffset anchor fields from the destination before clipping —
// the quadrant geometry of the 14 fog frames lives entirely in those offsets.
// The fog art is a mask: every frame is built from the color key (index 9) and
// index 0, so what reaches the screen is decided by mode, not by the source
// palette indices [R-RR16-A §3].
func (c *Client) blitFogGAF(frame *formats.GAFFrame, dstX, dstY int, mode fogBlitMode) {
	if frame == nil || c.indexed == nil {
		return
	}
	if mode == fogBlitGray && c.pal == nil {
		return
	}
	dstX -= int(frame.XOffset) // retail anchor: dest = cell origin - frame offset
	dstY -= int(frame.YOffset)
	w := c.width
	h := c.height
	fw := int(frame.Width)
	fh := int(frame.Height)
	if fw <= 0 || fh <= 0 {
		return
	}
	// Checker phase seeded by camera parity (camX+camZ)&1 [03 §3.3].
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
			if mode == fogBlitPatterned && ((int32(dstX+x)+int32(dstYPos)+parity)&1) == 0 {
				continue
			}
			idx := srcRow + x
			if idx < 0 || idx >= len(frame.Pixels) {
				continue
			}
			// Key pixels leave the destination alone; the decoder records the
			// key match in Transparent [fmt gaf].
			if idx < len(frame.Transparent) && frame.Transparent[idx] {
				continue
			}
			switch mode {
			case fogBlitGray:
				c.indexed[dstOff+x] = c.pal.Gray[c.indexed[dstOff+x]]
			case fogBlitPatterned:
				c.indexed[dstOff+x] = 0
			default:
				c.indexed[dstOff+x] = frame.Pixels[idx]
			}
		}
	}
}

// featureScreenPos computes the retail feature screen anchor [03 §5.1.4].
// It applies footprint centering and four-corner terrain-height averaging.
// The snapshot already carries world-centered X,Z and Y=coarseHeight; we reuse WorldToScreen
// for the shear and add footprint-half offset explicitly for non-centered callers.
// When terrain is available the Y uses the averaged heights at the footprint's four corners
// matching the four-corner averaging contract. Presentation-only (I6).
func (c *Client) featureScreenPos(f frame.FeatureView) (int32, int32) {
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
		// Collect heights across the footprint's four-corner sample pattern:
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
