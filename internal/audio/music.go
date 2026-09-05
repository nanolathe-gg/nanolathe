package audio

import "github.com/nanolathe/nanolathe/internal/sim/rng"

// presentationCRT is a presentation-only RNG with the same recurrence as the
// retail CRT stream (*214013+2531011) but is a distinct type so presentation
// does not consume the authoritative session CRT [DET-01][03 §8.4].
// AUDIT(parity-spine): music random is presentation-only; this is an approved
// divergence, not retail behavior — retail CD random shares the CRT stream, but
// Nanolathe isolates it to preserve sim determinism across render cadences.
type presentationCRT struct{ state uint32 }

func (p *presentationCRT) Rand() uint32 {
	p.state = p.state*214013 + 2531011
	return (p.state >> 16) & 0x7FFF
}

// Music uses WinMM MCI strings for cdaudio open/close/stop/status/play/pause
// [03 §8.4]. This file is presentation-only, uses a presentation-only CRT
// stream, and never touches the simulation RNG [I4].
//
// TODO(T23): two retail behaviors here are platform residuals, not unknowns —
// Nanolathe has neither an MCI `cdaudio` device nor a Windows registry to put
// them in. Both are Established and written up, so a port that acquires a CD
// backend implements them from research rather than re-tracing them (marker
// corrected 2026-09-04, WU-19-155; it previously said both were still open,
// which [03 §8.4] and [03 R-AUD-01 §4] had already closed):
//   - the missing-CD failure chain — a failed open retries once with an
//     enumerated window handle and then disables CD playback; a time-format
//     failure stops and closes the device and disables playback; a failed
//     track-count query leaves the count at zero and the tick idles [03 §8.4];
//   - history persistence — the per-disc category list is a 20-entry ring keyed
//     by the drive's volume serial, held in the registry binary value CDLISTS
//     (20 × 136 bytes), read at session init and rewritten on disc eject and at
//     shutdown. It is registry state, not save-game state: no save box carries
//     it [03 R-AUD-01 §4].

// PlayMode enumerates the five retail playback modes observed in
// the five-mode playback switch.
type PlayMode int

const (
	ModeIdle            PlayMode = 0 // stop / not playing, re-arm via stop
	ModeSequential      PlayMode = 1 // increment, wrap at numTracks
	ModeRandom          PlayMode = 2 // rand % numTracks +1 via CRT
	ModeSingle          PlayMode = 3 // repeat requested track
	ModeCategoryShuffle PlayMode = 4 // filtered shuffle by trackCategory
)

// StatusMode has the three media states idle, playing, and paused [03 §8.4].
type StatusMode int

const (
	StatusIdle    StatusMode = 0
	StatusPlaying StatusMode = 1
	StatusPaused  StatusMode = 2
)

// Controller is the presentation CD/MCI controller. It models the media state
// without requiring MCI. For Nanolathe the track list comes from a directory scan or is
// injected; for retail it would be Red Book tracks via mciSendStringA.
type Controller struct {
	initialized   bool
	musicEnabled  bool
	numTracks     int
	curTrack      int // 1-based, 0 = none
	nextTrack     int // requested / next to play (0 means none)
	status        StatusMode
	playMode      PlayMode
	desiredCat    int        // for mode 4: 0..4, Building|Battle|Victory|Defeat|Unused [03 R-AUD-01 §4]
	trackCategory [100]uint8 // (i%4)+1 cycle, retail builds 100 entries
	offset        int        // playhead offset used by a backend adapter
	crtState      uint32     // last drawn presentation-CRT state for isolated tests
	presCRT       *presentationCRT
	position      int
	volume        int
	volumeApplied int
	lastFrame     uint32
	hasFrame      bool
}

// NewMusicController creates a controller with retail's trackCategory cycle
// (i%4)+1 for 100 entries [03 §8.4] and sequential default.
func NewMusicController() *Controller {
	c := &Controller{
		playMode:     ModeSequential,
		musicEnabled: true,
		crtState:     1,
		presCRT:      &presentationCRT{state: 1},
		curTrack:     0,
		nextTrack:    0,
		status:       StatusIdle,
		numTracks:    0,
	}
	for i := 0; i < 100; i++ {
		// Categories repeat in the authored four-track cycle.
		c.trackCategory[i] = uint8(i%4 + 1)
	}
	return c
}

// Configure sets play mode and desired category for ModeCategoryShuffle.
// desiredCat is 0..4: 0 Building, 1 Battle, 2 Victory, 3 Defeat, 4 Unused
// (silence). 0 is a real, selectable category — not a "no category" sentinel
// — and the retail default per-disc category list is seven Battle bytes
// followed by Building zeros, so Building is the common case, not an edge
// case [03 R-AUD-01 §4].
func (c *Controller) Configure(mode PlayMode, desiredCat int) {
	if c == nil {
		return
	}
	c.playMode = mode
	if desiredCat >= 0 && desiredCat <= 4 {
		c.desiredCat = desiredCat
	}
}

// SetEnabled enables/disables music. When disabled,
// Play returns true without action and Tick does nothing, matching
// Play returns true without action.
func (c *Controller) SetEnabled(v bool) {
	if c != nil {
		c.musicEnabled = v
	}
}

// IsEnabled reports whether music is enabled.
func (c *Controller) IsEnabled() bool { return c != nil && c.musicEnabled }

// SetNumTracks sets the probed track count. 0 means no CD / missing media →
// silence but no error.
func (c *Controller) SetNumTracks(n int) {
	if c == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	if n > 99 {
		n = 99 // retail CD max 99, but internal 100 array allows 100
	}
	c.numTracks = n
	if c.nextTrack > n && n > 0 {
		c.nextTrack = n
	}
	if c.curTrack > n {
		c.curTrack = 0
		c.status = StatusIdle
	}
}

// NumTracks returns the current track count.
func (c *Controller) NumTracks() int {
	if c == nil {
		return 0
	}
	return c.numTracks
}

// CurTrack returns the current playing track (1-based, 0 none).
func (c *Controller) CurTrack() int {
	if c == nil {
		return 0
	}
	return c.curTrack
}

// NextTrack returns the next requested track.
func (c *Controller) NextTrack() int {
	if c == nil {
		return 0
	}
	return c.nextTrack
}

// Status returns the current status.
func (c *Controller) Status() StatusMode {
	if c == nil {
		return StatusIdle
	}
	return c.status
}

// Seed sets the presentation CRT seed for random/category-shuffle.
// It uses the CRT recurrence and exposes the seed for deterministic tests.
func (c *Controller) Seed(s uint32) {
	if c != nil {
		if s == 0 {
			s = 1
		}
		c.crtState = s
		if c.presCRT == nil {
			c.presCRT = &presentationCRT{}
		}
		c.presCRT.state = s
	}
}

// SetCRTRandom receives the session CRT handoff. DET-01: music must not draw
// the authoritative session stream, so only the stream STATE is copied into
// the private presentation-only source; the session stream itself is never
// retained or advanced here. AUDIT(parity-spine): approved divergence —
// retail's CD random shares the live CRT stream; Nanolathe isolates music
// draws so render cadence cannot affect simulation. The *rng.CRT parameter is
// the composition boundary type; the import is signature-only.
// Never label this stream retail behavior.
func (c *Controller) SetCRTRandom(r *rng.CRT) {
	if c != nil && r != nil {
		if c.presCRT == nil {
			c.presCRT = &presentationCRT{}
		}
		c.presCRT.state = r.State
		c.crtState = r.State
	}
}

func (c *Controller) drawCRT() uint32 {
	if c == nil {
		return 0
	}
	if c.presCRT != nil {
		v := c.presCRT.Rand()
		c.crtState = c.presCRT.state
		return v
	}
	c.crtState = c.crtState*214013 + 2531011
	return (c.crtState >> 16) & 0x7FFF
}

// TickFrame runs at most once for a presentation frame. This keeps media
// polling in the committed frame identity domain [03 §8.4]. A failed poll is
// passed as playing=false and follows the normal transition path.
func (c *Controller) TickFrame(frame uint32, playing bool) {
	if c == nil {
		return
	}
	if c.hasFrame && c.lastFrame == frame {
		return
	}
	c.hasFrame, c.lastFrame = true, frame
	c.Tick(playing)
}

// Open marks the media initialized without requiring a platform MCI adapter and
// probes numTracks via SetNumTracks externally; here we just mark ready.
// Failure fallback is to leave numTracks 0 and not initialized, which Tick
// treats as no-op (silence) matching retail open failure returning 0.
func (c *Controller) Open(numTracks int) bool {
	if c == nil {
		return false
	}
	c.initialized = true
	c.SetNumTracks(numTracks)
	c.status = StatusIdle
	c.curTrack = 0
	if numTracks > 0 {
		c.nextTrack = 0
	} else {
		c.nextTrack = 0
	}
	return true
}

// Close stops and clears media state.
func (c *Controller) Close() {
	if c == nil {
		return
	}
	c.status = StatusIdle
	c.curTrack = 0
	c.nextTrack = 0
}

// Stop halts playback and resets nextTrack:
// next = 0 when media is present, status=idle.
func (c *Controller) Stop() {
	if c == nil {
		return
	}
	c.status = StatusIdle
	if c.numTracks == 0 {
		c.nextTrack = 0
	} else {
		c.nextTrack = 0
	}
	c.curTrack = 0
}

// Pause toggles pause/play. When paused==true it sets
// status Paused; when false it resumes to Playing (if was paused) or no-op if
// idle. Retail actually builds mci pause cdaudio vs play cdaudio notify with hwndCallback.
func (c *Controller) Pause(paused bool) {
	if c == nil {
		return
	}
	if paused {
		if c.status == StatusPlaying {
			c.status = StatusPaused
		}
	} else {
		if c.status == StatusPaused {
			c.applyVolume()
			c.status = StatusPlaying
		}
	}
}

// SetPosition records the media playhead reported by a backend. It is kept
// across pause and is presentation state only; history persistence is still
// intentionally unresolved [03 §8.4].
func (c *Controller) SetPosition(milliseconds int) {
	if c != nil && milliseconds >= 0 {
		c.position = milliseconds
	}
}

func (c *Controller) Position() int {
	if c == nil {
		return 0
	}
	return c.position
}

// SetVolume stores the authored music volume and reapplies it on play or
// resume transitions [03 §8.4]. The backend owns its scale.
func (c *Controller) SetVolume(v int) {
	if c != nil {
		c.volume = v
	}
}

func (c *Controller) Volume() int {
	if c == nil {
		return 0
	}
	return c.volume
}

// VolumeApplications reports transition-time volume restoration for adapters
// and deterministic tests.
func (c *Controller) VolumeApplications() int {
	if c == nil {
		return 0
	}
	return c.volumeApplied
}

func (c *Controller) applyVolume() { c.volumeApplied++ }

// IsPlaying reports true iff status==playing.
// Retail polls via mciSendStringA status cdaudio mode vs "playing".
func (c *Controller) IsPlaying() bool { return c != nil && c.status == StatusPlaying }

// Play attempts to play a specific track 1..numTracks. It deduplicates if
// already playing same track. If music
// disabled it returns true (no-op). If track==0 it stops. Volume and MCI
// strings are presentation-only here.
func (c *Controller) Play(track int) bool {
	if c == nil {
		return false
	}
	if !c.musicEnabled {
		return true
	}
	if track == 0 {
		c.Stop()
		return true
	}
	if c.numTracks == 0 {
		return false
	}
	if track < 1 {
		track = 1
	}
	if track > c.numTracks {
		track = c.numTracks
	}
	if c.status == StatusPlaying && track == c.curTrack {
		return true // dedup
	}
	// A backend adapter may translate this state into its media command.
	c.curTrack = track
	c.nextTrack = track
	c.status = StatusPlaying
	c.position = 0
	c.applyVolume()
	return true
}

// playSequential implements mode 1: next++ wrap.
func (c *Controller) playSequential() {
	if c.numTracks == 0 {
		return
	}
	nxt := c.nextTrack
	if nxt < 0 {
		nxt = 0
	}
	// The transition pre-advances next, then wraps to track one.
	nxt = nxt%c.numTracks + 1
	c.nextTrack = nxt
	c.Play(nxt)
}

// playRandom implements mode 2: rand%numTracks+1.
func (c *Controller) playRandom() {
	if c.numTracks == 0 {
		return
	}
	r := c.drawCRT()
	track := int(r)%c.numTracks + 1
	c.Play(track)
}

// playSingle implements mode 3: cur != requested or not playing → jump to requested.
func (c *Controller) playSingle() {
	if c.numTracks == 0 {
		return
	}
	req := c.nextTrack
	if req == 0 {
		c.Stop()
		return
	}
	if req > c.numTracks {
		req = c.numTracks
	}
	if c.curTrack != req || c.status != StatusPlaying {
		c.Play(req)
	}
}

// categoryBranch is the tick's category branch [03 R-AUD-01 §4 step 6]
// [03 R-AUD-01 §8]: the draw `u = rand & 15` is taken FIRST, every time the
// branch runs, even while a matching track plays; then, if the drive reports
// playing and the next track already carries the desired category, nothing
// changes. Otherwise the scan walks forward from `next` with wrap, for at
// most (u+1)·count steps, and plays the max(1,u)-th track whose category is
// the desired one (u = 0 and u = 1 both select the first match — a retail
// quirk, reproduced); a scan that finds nothing stops and resets.
//
// desiredCat 0 (Building) is a real, selectable category, not "no category" —
// the tick's "new category ≠ 0" clause that the research once flagged as a
// possible sentinel belongs to SetDesired's fade-completion handoff (which
// this controller does not model: it delays running the tick by a one-shot
// pause when the *transition* lands on Building, so calm music doesn't cut in
// immediately after battle), not to the shuffle scan itself
// [03 R-AUD-01 §4, "Established fact — changing the desired category"].
//
// Correction (RWU-19-198): this used to stop and reset before scanning, scan
// from track 1, and play the first match. Retail neither stops first nor
// re-anchors the scan, and the pick index is max(1,u), not the first match.
func (c *Controller) categoryBranch(isPlaying bool) {
	if c.numTracks == 0 {
		return
	}
	if c.desiredCat < 0 || c.desiredCat > 4 {
		c.Stop()
		return
	}
	u := int(c.drawCRT() & 0xF) // 0..15, drawn before the poll is consulted
	if isPlaying && c.nextTrack >= 0 && c.nextTrack < len(c.trackCategory) && int(c.trackCategory[c.nextTrack]) == c.desiredCat {
		return // the matching track keeps playing; only the tail runs
	}
	budget := (u + 1) * c.numTracks
	cur := c.nextTrack
	if cur < 0 {
		cur = 0
	}
	for i := 0; i < budget; i++ {
		cur++
		if cur > c.numTracks {
			cur = 1
		}
		if int(c.trackCategory[cur]) == c.desiredCat {
			u--
			if u <= 0 {
				c.Play(cur)
				return
			}
		}
	}
	// no eligible track found within the budget → stop and reset
	c.Stop()
}

// Tick advances media state once per presentation frame. The boolean isPlaying
// reflects the mci poll `status cdaudio mode` vs "playing". The dispatch is
// retail's, in retail's order [03 R-AUD-01 §4 steps 1-7] [03 R-AUD-01 §8]:
//
//  1. no tracks → nothing;
//  2. desired category 4 (Unused = silence) → stop and reset, whatever the
//     play mode and even while PAUSED — this test precedes the paused test,
//     so a screen that requests silence stops a paused disc too;
//  3. paused → nothing;
//  4. desired category 2 or 3 (Victory/Defeat) → the category branch,
//     whatever the play mode (idle included);
//  5. otherwise by play mode, Custom (category shuffle) taking the same
//     category branch.
//
// The two overrides were an open question here until RWU-19-198 read the tick:
// they are Established, and load-bearing only once a caller drives desiredCat
// independently of the play mode (Configure sets both together today).
//
// The controller must be polled each rendered frame (presentation), not each
// presentation loop polling.
func (c *Controller) Tick(isPlaying bool) {
	if c == nil || c.numTracks == 0 {
		return
	}
	if c.desiredCat == 4 {
		c.Stop() // silence overrides everything, the paused state included
		return
	}
	if c.status == StatusPaused {
		return
	}
	if c.desiredCat == 2 || c.desiredCat == 3 || c.playMode == ModeCategoryShuffle {
		c.categoryBranch(isPlaying)
		return
	}
	// Handle the mode transition only after a not-playing poll.
	if c.playMode == ModeIdle {
		// if status !=0 and not playing -> stop and reset
		if c.status != StatusIdle && !isPlaying {
			c.Stop()
		}
		return
	}
	if isPlaying {
		// retail polls and if playing does nothing except ensure nextTrack in bounds for seq etc.
		// For seq, it ensures nextTrack >=1 etc but not advance until next not-playing tick.
		return
	}
	// not playing → advance per mode
	switch c.playMode {
	case ModeSequential:
		c.playSequential()
	case ModeRandom:
		c.playRandom()
	case ModeSingle:
		c.playSingle()
	}
}

// NotifyTrackEnd marks the current track as ended; the next presentation poll
// performs the configured transition.
// Under our model, the next Tick with isPlaying==false will advance, so Notify just marks not playing.
func (c *Controller) NotifyTrackEnd() {
	if c == nil {
		return
	}
	if c.status == StatusPlaying {
		c.status = StatusIdle
		// keep curTrack for sequential increment logic; next already advanced in sequential case
	}
}
