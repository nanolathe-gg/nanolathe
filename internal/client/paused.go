package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Paused presentation retains only the completed world composite. Everything
// after world/fog (including the UI's world overlays) is recorded each present,
// preserving its input, animation and destination-read ordering [03 §1][07 §11].
// This is an Enhanced optimization, described in DESIGN_GPU_RENDERER §13.10.
type pausedRecordLayer uint8

const (
	pausedWholeFrame pausedRecordLayer = iota
	pausedWorldLayer
	pausedForegroundLayer
)

// SetPresentationPaused mirrors the scheduler's actual pause result. A paused
// scheduler publishes no new tick, so Frame.Paused cannot supply live truth.
// Attach/restore must install this after SetSnapshot; detach clears it [I6].
func (c *Client) SetPresentationPaused(paused bool) {
	if c != nil {
		c.presentationPaused = paused
		if !paused {
			c.interp.pausedValid = false
		}
	}
}

func (c *Client) PresentationPaused() bool {
	return c != nil && c.presentationPaused
}

// PausedWorldInputs is an exact key for the world raster, independent of the
// host mutation epoch: that epoch also includes cursor and UI changes, which
// are freshly recorded in the foreground. Camera coordinates are the actual
// projected origin, so a stationary camera does not invalidate on each fraction.
// Assets are immutable after binding; world binding setters advance revision.
// Unit selection and group labels come from the committed frame, never input.
type PausedWorldInputs struct {
	committed               *frame.Frame
	tick                    uint32
	fraction                int32
	interpolation, enhanced bool
	revision                uint64

	width, height                        int
	camX, camZ, viewW, viewH, mapW, mapH int32
	scale                                camera.ViewScale
	zoom                                 camera.Zoom
	strategic                            bool

	terrain *world.Terrain
	palette *palette.Tables
	display [256][4]byte
	detail  *DetailArt
	font    *formats.FNT

	antiAlias, ditheredFog, damageBars               bool
	shadows, vehicleShadows, featureShadows, shading bool
}

// PausedWorldDigest returns false for a world with render-time randomness.
// Segmented projectiles retain their ordinary per-present CRT draws and full
// painter order [03 §5.4][I4]; conservatively reject even offscreen members.
// A diagnostic trace also requires real recording rather than cached pixels.
func (c *Client) PausedWorldDigest() (PausedWorldInputs, bool) {
	if c == nil || !c.presentationPaused || c.buffer == nil || c.cam == nil || c.rendererTraceSink != nil {
		return PausedWorldInputs{}, false
	}
	cur := c.buffer.Current()
	if cur == nil {
		return PausedWorldInputs{}, false
	}
	for _, p := range cur.Projectiles {
		if p.RenderType == render.RenderTypeSegmented {
			return PausedWorldInputs{}, false
		}
	}
	d := PausedWorldInputs{
		committed: cur, tick: cur.Tick, fraction: c.tickFraction16,
		interpolation: c.interpolation, enhanced: c.enhanced,
		revision: c.pausedWorldRevision, width: c.width, height: c.height,
		camX: c.cam.X, camZ: c.cam.Z, viewW: c.cam.ViewW, viewH: c.cam.ViewH,
		mapW: c.cam.MapW, mapH: c.cam.MapH, scale: c.cam.Scale, zoom: c.cam.Zoom,
		terrain: c.terrain, palette: c.pal, display: c.base, detail: c.detailArt, font: c.fnt,
		antiAlias: c.antiAlias, shadows: c.shadows, vehicleShadows: c.vehicleShadows,
		featureShadows: c.featureShadows, shading: c.shading, ditheredFog: c.ditheredFog,
		damageBars: damageBars, strategic: c.strategicView(),
	}
	// Use precisely the same previous-frame admission and camera arithmetic as
	// recordFrameNoAudio, including the viewport-sized teleport snap (§13.5).
	if c.hasCameraBlend() {
		w, h := c.cam.EffectiveView()
		d.camX = lerpOrigin(c.camPrevX, c.camCurX, int64(c.cameraFraction16), w)
		d.camZ = lerpOrigin(c.camPrevZ, c.camCurZ, int64(c.cameraFraction16), h)
	}
	return d, true
}

// RecordPausedWorld and RecordPausedForeground return ephemeral lists, just
// like RecordModernFrame. Execute each before recording the other. The host
// still calls BeginPresentationFrame once, and never advances it for a retry.
func (c *Client) RecordPausedWorld() *drawlist.List {
	return c.recordPausedLayer(pausedWorldLayer)
}

func (c *Client) RecordPausedForeground() *drawlist.List {
	return c.recordPausedLayer(pausedForegroundLayer)
}

func (c *Client) recordPausedLayer(layer pausedRecordLayer) *drawlist.List {
	if c == nil {
		return nil
	}
	before := c.pausedLayer
	c.pausedLayer = layer
	list := c.RecordModernFrame()
	c.pausedLayer = before
	return list
}
