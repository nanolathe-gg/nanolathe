package client

import (
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// SetPresentationCRT binds the session's presentation CRT handoff. DET-01:
// the client copies the stream STATE into a private presentation copy and
// never retains or draws the authoritative session stream — variant selection
// and segmented-projectile presentation advance only the copy, so render
// cadence cannot affect simulation [01 §7.2][I4]. AUDIT(parity-spine):
// approved divergence (retail presentation draws the live CRT stream); the
// signature is kept because cmd composes with *rng.CRT.
func (c *Client) SetPresentationCRT(crt *rng.CRT) {
	if c == nil {
		return
	}
	if crt == nil {
		return
	}
	v := *crt
	c.crt = &v
	c.crtBound = true
}

// SetAudioService binds the concrete retail audio owner. The client does not
// retain separate queue/cache/music authorities; it only drains this service
// at the rendered-frame edge [03 §8.2–§8.4] [I6].
func (c *Client) SetAudioService(a *audio.Service) {
	if c == nil {
		return
	}
	c.audioService = a
	c.ensureAudioBackend()
}

// SetAudioBackend installs the PCM backend directly [03 §8.3] [I6].
func (c *Client) SetAudioBackend(b *audio.Backend) {
	if c == nil {
		return
	}
	audio.SetGlobalBackend(b)
}

// AudioBackend returns the installed PCM backend (presentation-only) [I6].
func (c *Client) AudioBackend() *audio.Backend {
	return audio.GlobalBackend()
}

func (c *Client) ensureAudioBackend() {
	if c == nil {
		return
	}
	if audio.GlobalOutput() != nil {
		return
	}
	b := audio.NewBackend()
	audio.SetGlobalBackend(b)
}

// SetAudioViewport sets the presentation viewport for positional pan and
// attenuation [03 §8.3]. Stereo pan is dx = px-((w/2)<<4)-left and
// dy = top+((h/2)<<4)+(py>>1)-pz; mono fallback is -585 in-view vs -1585
// off-screen [03 §8.3]. Presentation-only [I6].
func (c *Client) SetAudioViewport(v audio.Viewport) {
	if c == nil || c.audioService == nil {
		return
	}
	c.audioService.SetViewport(v)
}

// AudioViewport returns the current viewport for tests.
func (c *Client) AudioViewport() audio.Viewport {
	if c == nil || c.audioService == nil {
		return audio.Viewport{}
	}
	return c.audioService.Viewport()
}

// TickAudio drains the queue once per rendered frame outside simulation
// [03 §8.3] C18 I6. At most one voice is audible per 30 frames; silent
// resolves within the window still print speech but play no sound; full-queue
// eviction resolves the last entry silently before inserting [03 §8.3] C16.
// It also ticks the music controller via the MCI poll [03 §8.4].
// The queue's OnPlay variant draw [03 §8.3] C17 is played via PCM with
// volume/pan from the positional math [03 §8.3].
func (c *Client) TickAudio() {
	if c == nil {
		return
	}
	c.ensureAudioBackend()
	if c.audioService != nil {
		var committedTick uint32
		var events []frame.EventView
		if c.buffer != nil {
			if current := c.buffer.Current(); current != nil {
				committedTick = current.Tick
				events = current.Events
			}
		}
		c.audioService.DrainEvents(c.audioService.Frame()+1, committedTick, events)
	}
}

// UpdateAudioViewportFromCamera builds the audio viewport from the current
// camera and world dimensions [03 §8.3]. Pan is viewport-relative with
// half-height shear; attenuation is the two-level -585/-1585 step.
// Presentation-only [I6].
func (c *Client) UpdateAudioViewportFromCamera() {
	if c == nil || c.cam == nil {
		return
	}
	var mapW, mapH int32
	if c.terrain != nil {
		// Map dimensions in pixels: tiles*16 (cellW*16?) Actually terrain CellW is in 16-pixel cells; map pixels = CellW*16.
		mapW = c.terrain.CellW * 16
		mapH = c.terrain.CellH * 16
	} else {
		mapW = int32(c.width)
		mapH = int32(c.height)
	}
	// Width and Height are tile counts because the pan formula expands their
	// half-size by 16 [03 §8.3]. Camera X/Z are already map pixels.
	v := audio.Viewport{
		Left:   c.cam.X,
		Top:    c.cam.Z,
		Width:  int32(c.width / 16), // approximate tiles; precise value is not critical for the fallback
		Height: int32(c.height / 16),
		MapW:   mapW,
		MapH:   mapH,
	}
	if be := audio.GlobalBackend(); be != nil {
		v.StereoCapable = be.Capabilities().Stereo
	}
	if c.audioService != nil {
		c.audioService.SetViewport(v)
	}
}

// HasPresentationCRT reports whether a presentation CRT copy has been bound
// (see SetPresentationCRT). A client without it degrades the
// presentation-only consumers of that copy, so composition paths are checked
// against it [01 §7.2][I4]. DET-01: this never reports on the session's
// authoritative stream — the client cannot draw it.
func (c *Client) HasPresentationCRT() bool { return c != nil && c.crtBound }
