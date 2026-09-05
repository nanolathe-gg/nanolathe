package client

import (
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
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
	if a != nil {
		a.Init(nil)
	}
	if a != nil && a.Queue != nil {
		a.Queue.OnCaption(func(line string, _ audio.Slot, unit pool.Handle) {
			// The queue has already applied liveness, caption/default-text,
			// UNITCHAT priority and cooldown gates when this callback runs.
			c.messages.Append(line, 1, unit, 10, c.messageEventsTick)
		})
	}
}

// stereoOutput is the one capability the positional-pan viewport reads from
// the installed playback boundary. Declaring it here keeps the client on
// audio.Output and off the concrete device package: the platform adapter
// installs the device, and an output without stereo support simply reports
// none [03 §8.3][I6].
type stereoOutput interface{ StereoCapable() bool }

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
//
// The committed events it applies are the RETAINED ones, not the current
// slot's. Session.Step publishes one frame per sub-tick and can run several
// before a rendered frame arrives — a frame hitch, or the 2x/3x speed setting
// — and reading only the current slot dropped every superseded tick's cues and
// status requests, making delivery depend on render cadence. [03 R-AUD-01 §7]
// allows the raise to cross the publication boundary only when each committed
// tick's events are applied exactly once in raise order, which is what the
// buffer's retained queue provides [03 §2.4][I6].
func (c *Client) TickAudio() {
	if c == nil {
		return
	}
	var committedTick uint32
	if c.buffer != nil {
		if current := c.buffer.Current(); current != nil {
			committedTick = current.Tick
		}
		// Drain even with no audio owner bound: those events have no consumer
		// either way, and leaving them queued would only grow the retention.
		c.committedEvents = c.buffer.DrainCommittedEvents(c.committedEvents)
	}
	if c.audioService != nil {
		c.enqueueStatusEvents(committedTick, c.committedEvents)
		// One drain per rendered frame keeps the queue's single pop and the MCI
		// poll on the presentation cadence [03 §8.3] C18 [03 §8.4]; the
		// accumulated positional cues are played inside it, in raise order.
		c.audioService.DrainEvents(c.audioService.Frame()+1, committedTick, c.committedEvents)
		c.messages.Expire(committedTick)
	}
}

// enqueueStatusEvents submits committed status requests to the existing audio
// queue. Caption composition and all UNITCHAT/cooldown gates therefore remain
// in one resolver; only its OnCaption callback writes the client ring [03
// §8.3][07 R-HUD-03 §14.1–§14.3].
//
// Each request is inserted against ITS OWN raise tick, which is the global
// tick counter the queue's cooldown and duplicate-slot rules are expressed in
// [03 §8.3]. Stamping a whole accumulated batch with the drain's tick would
// move an earlier tick's cue forward past its slot's next-allowed frame and
// silently rewrite the arbitration [03 R-AUD-01 §7]. ringTick is a separate
// thing: the ageing origin for the caption line the resolve produces, which
// starts when the line is drawn, not when the request was raised
// [07 R-HUD-03 §14.3].
func (c *Client) enqueueStatusEvents(ringTick uint32, events []frame.EventView) {
	if c == nil {
		return
	}
	for _, event := range events {
		if event.Kind != frame.EventKindStatus {
			continue
		}
		// A unit torn down in its raise tick had its slot zeroed before
		// publication; Emit refuses slot 0, which is that purge [03 R-AUD-01 §7].
		_ = c.audioService.Emit(event.Tick, audio.Slot(event.StatusKind), event.Source, event.StatusText)
	}
	c.messageEventsTick = ringTick
}

// ConfigureMessageLines installs the authored ring controls. textlines is the
// ring modulus (the drawer shows textlines-1 lines); textscroll is in seconds
// and ageing uses simulation ticks [07 R-HUD-03 §14.3].
func (c *Client) ConfigureMessageLines(textLines, textScroll uint16) {
	if c == nil {
		return
	}
	c.messages.Configure(textLines, textScroll)
}

// SetScreenChat sets the message-column class filter. Nonzero modes draw all
// classes; zero retains only classes 1, 4 and 8 [07 R-HUD-03 §14.4].
func (c *Client) SetScreenChat(mode uint8) {
	if c != nil {
		c.screenChat = mode
	}
}

// MessageLines returns the currently visible message-column lines after the
// screenchat class filter. The returned slice is detached from the ring.
func (c *Client) MessageLines() []frame.MessageLine {
	if c == nil {
		return nil
	}
	lines := c.messages.Visible()
	if c.screenChat != 0 {
		return lines
	}
	out := lines[:0]
	for _, line := range lines {
		if line.Class == 1 || line.Class == 4 || line.Class == 8 {
			out = append(out, line)
		}
	}
	return out
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
	if out, ok := audio.GlobalOutput().(stereoOutput); ok {
		v.StereoCapable = out.StereoCapable()
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
