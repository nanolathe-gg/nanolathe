// Package client is the Kaiju window and frame loop.
//
// Presentation is the one deliberate divergence from retail's draw path, which
// samples committed state with no interpolation [03 §2.4]. The sim never reads
// wall-clock time, input state, camera, or renderer state (I6). Client owns the
// Kaiju update boundary but never advances a clock or mutates sim state itself;
// the injected Step callback owns those effects (PLAN_03 C15/C16, PLAN_04A C9).
//
// Exactly one Updater registration exists in this package (C12).
// Kaiju's Updater is concurrent and unordered, so extra registrations would
// reintroduce nondeterministic ordering and violate I1/I6.
//
// Headless mode skips window creation entirely and must remain fully functional
// (C11). The Kaiju import is confined to this package; no sim package imports
// this package (I6).
// TODO(T23): window style bits and SystemParametersInfoA semantics untraced.
// Kaiju creates the window via windowing.New; retail's exact style bits are
// not reproduced, but the negotiated size concept survives.
package client

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"kaijuengine.com/bootstrap"
	"kaijuengine.com/engine"
	"kaijuengine.com/engine/assets"
	"kaijuengine.com/matrix"
	"kaijuengine.com/platform/hid"
	"kaijuengine.com/registry/shader_data_registry"
	"kaijuengine.com/rendering"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/world"
)

// EventKind classifies a drained input event for the session.
type EventKind int

const (
	EventKey   EventKind = iota // keyboard token
	EventMouse                  // mouse record
	EventClose                  // window close
)

// Event is a translated input record. Translation happens at the boundary so
// nothing downstream sees Kaiju types. The retail token vocabulary drives
// this shape [07 §2]; later WUs extend it with the ring/latch semantics.
type Event struct {
	Kind             EventKind
	Key              int
	State            hid.KeyState
	X, Y             float32 // mouse position
	Button           int
	ScrollX, ScrollY float32
}

// Options configures a Client. Step is the injected owner of clock, sub-ticks,
// and snapshot publish; it is called first inside the sole Updater (C9). The
// window, palette, camera, and snapshot dependencies are carried here.
type Options struct {
	Step func(delta float64) // injected owner of clock/sub-ticks/snapshot publish (C9)

	// Presentation snapshot source. If nil, an empty buffer is used.
	Buffer *snapshot.Buffer

	// Negotiated window size: the HUD panel arithmetic in phase 12 depends on
	// it, so it is not invented per frame. Zero means the Kaiju default.
	Width, Height int
	Title         string

	// Headless skips window creation entirely (C11). Nothing above becomes
	// reachable from --headless; the binary still links Kaiju but does not
	// create an engine.Host.
	Headless bool
}

// Client is the Kaiju window, palette, camera, and snapshot reader. It keeps
// retail's 8-bit indexed software renderer and hands Kaiju one texture per
// frame. Rendering interpolates Previous→Current at render fraction (I6).
type Client struct {
	opts   Options
	host   *engine.Host
	buffer *snapshot.Buffer

	width, height int
	indexed       []uint8
	rgba          []byte

	// Palette fallback (WU-04A-2 replaces this with full tables). The conversion
	// goes logical → physical through the 256-byte table at present time only
	// (C7). Until real PALETTE.PAL is loaded we use a grayscale ramp so the
	// alpha ramp remains visibly correct.
	base    [256][4]byte
	logical [256]byte
	pal     *palette.Tables

	// World / camera for Gate 1 terrain viewer [PLAN_04A]. When set, Frame
	// draws real TNT terrain instead of the placeholder gradient.
	terrain *world.Terrain
	cam     *camera.Camera
	fnt     *formats.FNT

	texture    *rendering.Texture
	mesh       *rendering.Mesh
	transform  matrix.Transform
	shaderData rendering.DrawInstance

	mu     sync.Mutex
	events []Event
}

// New creates a client. It allocates the indexed framebuffer at the negotiated
// logical size and prepares fallback palette tables. Window creation is deferred
// to Launch via bootstrap.Main; in headless mode no window is ever created.
func New(opts Options) (*Client, error) {
	w := opts.Width
	h := opts.Height
	if w <= 0 {
		w = engine.DefaultWindowWidth
	}
	if h <= 0 {
		h = engine.DefaultWindowHeight
	}
	if w <= 0 || h <= 0 {
		w = 640
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
		events:  make([]Event, 0, 32),
	}
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
	c.transform.SetupRawTransform()
	c.transform.SetPosition(matrix.NewVec3(0, 0, 0))
	c.transform.SetScale(matrix.NewVec3(float32(w), float32(h), 1))

	// Headless returns immediately with no window and no display (C11).
	if opts.Headless {
		return c, nil
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
	}
}

// SetFNT sets the debug font [03 §7.1] C8.
func (c *Client) SetFNT(fnt *formats.FNT) { c.fnt = fnt }

// Host returns the underlying engine.Host after Launch, or nil if headless or
// not yet launched.
func (c *Client) Host() *engine.Host { return c.host }

// Launch implements bootstrap.GameInterface. Kaiju calls it once after the
// window and renderer are initialized; we build everything that needs a Host
// here.
//
// Exactly one Updater registration exists in the whole program
// (C12). The updater is concurrent and unordered, so a single registration
// that drives clock→sub-ticks→snapshot publish→compose→upload→draw sequentially
// is required to preserve determinism (I1/I6).
func (c *Client) Launch(host *engine.Host) {
	c.host = host
	// Re-initialize transform with the host workgroup so dirty tracking uses
	// the correct concurrent workgroup.
	c.transform.Initialize(host.WorkGroup())
	c.transform.SetPosition(matrix.NewVec3(0, 0, 0))
	c.transform.SetScale(matrix.NewVec3(float32(c.width), float32(c.height), 1))

	// Ensure an orthographic UI camera covers the negotiated size. The host
	// already creates one at the negotiated dimensions, but we keep the size
	// in sync if the window was negotiated differently.
	if c.host.Window != nil {
		w := float32(c.host.Window.Width())
		h := float32(c.host.Window.Height())
		if w > 0 && h > 0 {
			// Keep client's logical size in step with the actual window size
			// (the negotiated size concept survives the single Kaiju path).
			if int(w) != c.width || int(h) != c.height {
				c.width = int(w)
				c.height = int(h)
				c.indexed = make([]uint8, c.width*c.height)
				c.rgba = make([]byte, c.width*c.height*4)
				c.texture = nil
				c.mesh = nil
				c.transform.SetScale(matrix.NewVec3(w, h, 1))
			}
		}
		if c.opts.Title != "" {
			c.host.Window.SetTitle(c.opts.Title)
		}
	}

	// Prepare the single framebuffer texture and quad. Creation is done here
	// rather than in Frame so the per-frame path only does compose→convert→
	// upload→draw. If creation fails the per-frame upload will recreate it.
	c.ensureResources()

	// The sole updater (C12). It owns the Kaiju update boundary but never
	// mutates sim state itself; Step does (C9).
	host.Updater.AddUpdate(func(delta float64) {
		// C9: Step first, then snapshot read and presentation. Client never
		// writes sim state, calls a sim mutator, or advances the clock.
		if c.opts.Step != nil {
			c.opts.Step(delta)
		}
		// Translate Kaiju hid types at the boundary into retail vocabulary
		// (C5) so downstream code never sees Kaiju types.
		c.pollInput()
		alpha := c.computeAlpha()
		c.Frame(alpha)
	})
}

// computeAlpha derives the render interpolation fraction. In the full engine
// it is clamp((nowNanos-tickStartNanos)/tickPeriodNanos,0,1) (PLAN_03 C16).
// For the 60 fps / 30 Hz stub we derive it from host runtime so the ramp is
// visibly correct: frac = fract(runtime*30).
func (c *Client) computeAlpha() float32 {
	if c.host == nil {
		return 1
	}
	rt := c.host.Runtime()
	ticks := rt * 30.0
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

// pollInput translates host.Window.Keyboard and host.Window.Mouse (platform/hid)
// into the retail token vocabulary at the boundary.
func (c *Client) pollInput() {
	if c.host == nil || c.host.Window == nil {
		return
	}
	kbd := &c.host.Window.Keyboard
	mouse := &c.host.Window.Mouse

	var evs []Event

	// Keyboard: sample the 30-entry ring vocabulary (29 usable, refusal when
	// full [07 §2]) at the boundary. Later WUs implement the exact ring; for
	// 04A-1 we drain discrete down/held presses.
	for k := 0; k < hid.KeyboardKeyMaximum; k++ {
		if kbd.KeyDown(hid.KeyboardKey(k)) {
			evs = append(evs, Event{Kind: EventKey, Key: k, State: hid.KeyStateDown})
		} else if kbd.KeyHeld(hid.KeyboardKey(k)) {
			// Held is also reported so edge scroll and camera remain responsive;
			// the session deduplicates per its input phase.
			evs = append(evs, Event{Kind: EventKey, Key: k, State: hid.KeyStateHeld})
		}
	}
	if mouse.Moved() || mouse.ButtonChanged() || mouse.Scrolled() || mouse.Held(hid.MouseButtonLeft) || mouse.Held(hid.MouseButtonRight) {
		evs = append(evs, Event{
			Kind:    EventMouse,
			X:       mouse.X,
			Y:       mouse.Y,
			Button:  mouse.ButtonState(hid.MouseButtonLeft),
			ScrollX: mouse.ScrollX,
			ScrollY: mouse.ScrollY,
		})
	}
	if len(evs) == 0 {
		return
	}
	c.mu.Lock()
	c.events = append(c.events, evs...)
	c.mu.Unlock()
}

// DrainInput returns input events for the session to apply. It drains the
// internal queue so the same event is not applied twice.
func (c *Client) DrainInput() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return nil
	}
	out := make([]Event, len(c.events))
	copy(out, c.events)
	c.events = c.events[:0]
	return out
}

// PluginRegistry implements bootstrap.GameInterface. No Lua types are exposed.
func (c *Client) PluginRegistry() []reflect.Type { return nil }

// ContentDatabase implements bootstrap.GameInterface. It returns Kaiju's stock
// content database which is a hard prerequisite for
// host.MaterialCache().Material(...) — failure is misleading without it. The
// content directory is populated via `make kaiju-content` (cp -R
// ../kaiju/src/editor/editor_embedded_content/editor_content ./content).
func (c *Client) ContentDatabase() (assets.Database, error) {
	// Try several locations for the Kaiju stock content. When run via
	// `go run ./cmd/nanolathe` the working directory is the repo root,
	// but the built binary may be executed from a temp directory.
	candidates := []string{
		"content",
		"./content",
		"../content",
		"../../content",
	}
	// Also try relative to the executable.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "content"),
			filepath.Join(dir, "../content"),
			filepath.Join(dir, "../../content"),
		)
		// Walk up from cwd as well.
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates,
				filepath.Join(cwd, "content"),
				filepath.Join(cwd, "../content"),
			)
		}
		_ = dir
	}
	for _, cand := range candidates {
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			// Verify it looks like Kaiju content (has renderer/materials).
			if _, err := os.Stat(filepath.Join(cand, "renderer")); err == nil {
				return assets.NewFileDatabase(cand)
			}
			// Still try even if renderer not found — let assets report.
			return assets.NewFileDatabase(cand)
		}
	}
	// Fallback to the original relative path so the error message from
	// assets.NewFileDatabase is preserved, but include diagnostics.
	return assets.NewFileDatabase("content")
}

// Run is a convenience helper that starts the Kaiju main loop with this client
// as the GameInterface. It blocks until the window closes. In headless mode it
// returns immediately. Most callers use bootstrap.Main directly:
//
//	client, _ := client.New(opts)
//	bootstrap.Main(client, platformState)
func (c *Client) Run(platformState any) {
	if c.opts.Headless {
		return
	}
	bootstrap.Main(c, platformState)
}

// ensureResources creates the framebuffer texture, quad mesh, and shader data
// when not yet present and the host is ready. It is called once from Launch
// and lazily from Frame if a resize discarded the texture.
func (c *Client) ensureResources() {
	if c.host == nil || c.host.Window == nil {
		return
	}
	device := c.host.Window.GpuInstance.PrimaryDevice()
	if device == nil {
		return
	}
	if c.texture == nil {
		tex, err := rendering.NewTextureFromMemory("framebuffer", c.rgba, c.width, c.height, rendering.TextureFilterNearest)
		if err != nil {
			return
		}
		// Immediate upload of the initial clear; DelayedCreate moves pendingData to GPU.
		tex.DelayedCreate(device)
		c.texture = tex
	}
	if c.mesh == nil {
		mc := c.host.MeshCache()
		if mc != nil {
			c.mesh = rendering.NewMeshQuad(mc)
		}
	}
	if c.shaderData == nil {
		c.shaderData = shader_data_registry.Create("basic")
	}
}
