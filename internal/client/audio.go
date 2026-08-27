package client

import (
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// SetPresentationClock binds the shared presentation clock used by audio
// draining and music polling. A nil clock restores the legacy explicit-frame
// fallback for isolated callers and tests.
func (c *Client) SetPresentationClock(clock *presentation.Clock) {
	if c == nil {
		return
	}
	c.audioClock = clock
	c.clock = clock
}

// SetPresentationCRT binds the session-owned presentation CRT stream. Shake,
// audio, music, and projectile presentation must share this stream; a client
// never seeds a private stream for live play [01 §7.2][03 §5.6].
func (c *Client) SetPresentationCRT(crt *presentation.CRTRandom) {
	if c != nil {
		c.crt = crt
	}
}

func (c *Client) presentationClock() *presentation.Clock {
	if c == nil {
		return nil
	}
	return c.audioClock
}

// SetAudioQueue attaches the eight-slot queue [03 §8.3] C16 C18. The queue is
// presentation-owned; Drain runs once per rendered frame outside simulation
// [03 §8.3] C18 I6. Variant selection uses the CRT stream [03 §8.3] C17 C19.
func (c *Client) SetAudioQueue(q *audio.Queue) {
	if c == nil {
		return
	}
	c.audioQueue = q
	c.ensureAudioBackend()
}

// SetAudioCache attaches the sample cache [03 §8.2] C20. Load resolves aliases
// through VFS and caches decoded PCM; miss degrades silently [P1-02 §2.2].
func (c *Client) SetAudioCache(cache *audio.SampleCache) {
	if c == nil {
		return
	}
	c.audioCache = cache
	c.ensureAudioBackend()
}

// SetMusicController attaches the CD/MCI controller [03 §8.4]. Tick is
// presentation-only and polls isPlaying via mciSendStringA status [03 §8.4].
func (c *Client) SetMusicController(m *audio.Controller) {
	if c == nil {
		return
	}
	c.audioMusic = m
}

// SetAudioBackend installs the PCM backend directly [03 §8.3] [I6]. Headless
// never installs a device; windowed callers pass a non-headless backend.
func (c *Client) SetAudioBackend(b *audio.Backend) {
	if c == nil {
		return
	}
	if b != nil && !b.IsHeadless() && c.opts.Headless {
		// Headless client must never hold a windowed backend [I5][I6].
		return
	}
	audio.SetGlobalBackend(b)
}

// AudioBackend returns the installed PCM backend (presentation-only) [I6].
func (c *Client) AudioBackend() *audio.Backend {
	return audio.GlobalBackend()
}

func (c *Client) ensureAudioBackend() {
	if c == nil || c.opts.Headless {
		return
	}
	if audio.GlobalBackend() != nil {
		return
	}
	// Guard ALL device construction behind windowed backend detection [I5][I6].
	// Headless never constructs an audio context; hashes stay byte-identical.
	b := audio.NewBackend(false)
	audio.SetGlobalBackend(b)
}

// SetAudioViewport sets the presentation viewport for positional pan and
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// dy = top+((h/2)<<4)+(py>>1)-pz; mono fallback is -585 in-view vs -1585
// off-screen [03 §8.3]. Presentation-only [I6].
func (c *Client) SetAudioViewport(v audio.Viewport) {
	if c == nil {
		return
	}
	c.audioViewport = v
}

// AudioViewport returns the current viewport for tests.
func (c *Client) AudioViewport() audio.Viewport {
	if c == nil {
		return audio.Viewport{}
	}
	return c.audioViewport
}

// AudioQueue returns the attached queue for diagnostics.
func (c *Client) AudioQueue() *audio.Queue {
	if c == nil {
		return nil
	}
	return c.audioQueue
}

// AudioCache returns the attached cache.
func (c *Client) AudioCache() *audio.SampleCache {
	if c == nil {
		return nil
	}
	return c.audioCache
}

// AudioMusic returns the attached music controller.
func (c *Client) AudioMusic() *audio.Controller {
	if c == nil {
		return nil
	}
	return c.audioMusic
}

// TickAudio drains the queue once per rendered frame outside simulation
// [03 §8.3] C18 I6. At most one voice is audible per 30 frames; silent
// resolves within the window still print speech but play no sound; full-queue
// eviction resolves the last entry silently before inserting [03 §8.3] C16.
// It also ticks the music controller via the MCI poll [03 §8.4].
// When a windowed backend is present the queue's OnPlay variant draw [03 §8.3]
// C17 is played via PCM with volume/pan from the positional math [03 §8.3]
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Client) TickAudio() {
	if c == nil {
		return
	}
	c.ensureAudioBackend()
	// Direct TickAudio callers (diagnostics/tests) are presentation frames too.
	// The normal Frame path begins the shared clock before this method, so it
	// does not double-advance the frame serial [03 §8.3][I6].
	if c.audioClock != nil && !c.frameBegun {
		c.audioClock.BeginFrame(c.audioClock.SimTick, 0)
	}
	frame := c.audioFrame + 1
	clock := c.presentationClock()
	if clock != nil {
		frame = clock.FrameSerial
	} else {
		c.audioFrame = frame
	}
	if c.audioQueue != nil {
		c.audioQueue.Drain(frame)
	}
	if c.audioMusic != nil {
		if clock != nil {
			c.audioMusic.TickFrame(clock, c.audioMusic.IsPlaying())
		} else {
			c.audioMusic.Tick(c.audioMusic.IsPlaying())
		}
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
		Width:  int32(c.width / 16), // approximate tiles; precise value not critical for headless fallback
		Height: int32(c.height / 16),
		MapW:   mapW,
		MapH:   mapH,
	}
	if be := audio.GlobalBackend(); be != nil {
		v.StereoCapable = be.Capabilities().Stereo
	}
	c.audioViewport = v
}

// PlayPositional plays a world-space alias with audience gating and
// viewport-relative placement [03 §8.3]. It is presentation-only [I4][I6].
// Caller supplies the alias and world pos in 16.16; this helper checks the
// audience cell via the supplied IsAudible func (which should wrap
// audio.IsAudible with the session's vis grids and mode &2), computes pan or
// attenuation, and loads via cache. Missing alias degrades silently.
// When a windowed backend is present the alias is also played via PCM with
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// constructs a device [I5][I6].
func (c *Client) PlayPositional(alias string, pos [3]numeric.Fixed, isAudible func([3]numeric.Fixed) bool) (audio.Pan, int32, bool) {
	if c == nil || alias == "" {
		return audio.Pan{}, 0, false
	}
	if isAudible != nil && !isAudible(pos) {
		return audio.Pan{}, 0, false
	}
	var pan audio.Pan
	var vol int32
	if c.audioViewport.StereoCapable {
		pan = audio.ComputePan(pos, c.audioViewport)
		vol = audio.VolInView
	} else {
		vol = audio.Attenuate(pos, c.audioViewport)
		pan = audio.Pan{}
	}
	if c.audioCache != nil {
		_, _ = c.audioCache.Load(alias)
	}
	if be := audio.GlobalBackend(); be != nil {
		volF := audio.VolumeFromAttenuation(vol)
		panF := audio.PanFloat(pan, c.audioViewport)
		_ = be.PlayAlias(alias, c.audioCache, volF, panF)
	}
	return pan, vol, true
}

// PlayUICue plays a non-positional interface sound by its authored alias
// [03 §8.3]. Interface cues — the build-placement confirmations, the order
// acknowledgements, the menu clicks — are ordinary sound aliases played at full
// volume with no pan, because they have no world position to attenuate or
// place. A missing alias degrades silently, the same way the positional path
// treats one.
// When a backend is present the cue is also played via PCM at unity
// volume [03 §8.3] [I6]; headless backends record without constructing a
// device [I5].
func (c *Client) PlayUICue(alias string) bool {
	if c == nil || alias == "" || c.audioCache == nil {
		return false
	}
	_, err := c.audioCache.Load(alias)
	if err == nil {
		if be := audio.GlobalBackend(); be != nil {
			_ = be.PlayAlias(alias, c.audioCache, 1.0, 0)
		}
	}
	return err == nil
}
