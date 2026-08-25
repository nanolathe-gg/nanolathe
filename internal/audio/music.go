package audio

// Music uses WinMM MCI strings for cdaudio open/close/stop/status/play/pause
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// behavior on missing CD, history persistence across saves are unresolved
// and stay TODO(T23). This file is presentation-only, uses the CRT stream
// conceptually (but pluggable), and never touches Sim RNG [I4].

// PlayMode enumerates the five retail playback modes observed in
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type PlayMode int

const (
	ModeIdle            PlayMode = 0 // stop / not playing, re-arm via stop
	ModeSequential      PlayMode = 1 // increment, wrap at numTracks
	ModeRandom          PlayMode = 2 // rand % numTracks +1 via CRT
	ModeSingle          PlayMode = 3 // repeat requested track
	ModeCategoryShuffle PlayMode = 4 // filtered shuffle by trackCategory
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type StatusMode int

const (
	StatusIdle    StatusMode = 0
	StatusPlaying StatusMode = 1
	StatusPaused  StatusMode = 2
)

// Controller is the presentation CD/MCI controller. It emulates the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// without MCI. For Nanolathe the track list comes from a directory scan or is
// injected; for retail it would be Red Book tracks via mciSendStringA.
type Controller struct {
	initialized   bool
	musicEnabled  bool
	numTracks     int
	curTrack      int // 1-based, 0 = none
	nextTrack     int // requested / next to play (0 means none)
	status        StatusMode
	playMode      PlayMode
	desiredCat    int        // for mode 4, 1..4
	trackCategory [100]uint8 // (i%4)+1 cycle, retail builds 100 entries
	offset        int        // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	crtState      uint32     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// NewMusicController creates a controller with retail's trackCategory cycle
// (i%4)+1 for 100 entries [03 §8.4] and sequential default.
func NewMusicController() *Controller {
	c := &Controller{
		playMode:     ModeSequential,
		musicEnabled: true,
		crtState:     1,
		curTrack:     0,
		nextTrack:    1,
		status:       StatusIdle,
		numTracks:    0,
	}
	for i := 0; i < 100; i++ {
		// ((i ^ (i>>31)) - (i>>31) &3 ^ ...) - ... +1 in decompile is just (i%4)+1 with sign-correct
		c.trackCategory[i] = uint8(i%4 + 1)
	}
	return c
}

// Configure sets play mode and desired category for ModeCategoryShuffle.
// desiredCat is 1..4, corresponding to trackCategory values.
func (c *Controller) Configure(mode PlayMode, desiredCat int) {
	if c == nil {
		return
	}
	c.playMode = mode
	if desiredCat >= 1 && desiredCat <= 4 {
		c.desiredCat = desiredCat
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Play returns true without action and Tick does nothing, matching
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Controller) SetEnabled(v bool) {
	if c != nil {
		c.musicEnabled = v
	}
}

// IsEnabled reports whether music is enabled.
func (c *Controller) IsEnabled() bool { return c != nil && c.musicEnabled }

// SetNumTracks sets the probed track count (retail via mci status number of
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// are presentation. We expose seed for determinism in tests.
func (c *Controller) Seed(s uint32) {
	if c != nil {
		if s == 0 {
			s = 1
		}
		c.crtState = s
	}
}

func (c *Controller) drawCRT() uint32 {
	if c == nil {
		return 0
	}
	c.crtState = c.crtState*214013 + 2531011
	return (c.crtState >> 16) & 0x7FFF
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		c.nextTrack = 1
	} else {
		c.nextTrack = 0
	}
	return true
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Controller) Close() {
	if c == nil {
		return
	}
	c.status = StatusIdle
	c.curTrack = 0
	c.nextTrack = 0
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// next = (numTracks==0?0:1), status=idle.
func (c *Controller) Stop() {
	if c == nil {
		return
	}
	c.status = StatusIdle
	if c.numTracks == 0 {
		c.nextTrack = 0
	} else {
		c.nextTrack = 1
	}
	c.curTrack = 0
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
			c.status = StatusPlaying
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Retail polls via mciSendStringA status cdaudio mode vs "playing".
func (c *Controller) IsPlaying() bool { return c != nil && c.status == StatusPlaying }

// Play attempts to play a specific track 1..numTracks. It deduplicates if
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Here we just update state.
	c.curTrack = track
	c.nextTrack = track
	c.status = StatusPlaying
	return true
}

// playSequential implements mode 1: next++ wrap.
func (c *Controller) playSequential() {
	if c.numTracks == 0 {
		return
	}
	nxt := c.nextTrack
	if nxt < 1 {
		nxt = 1
	}
	// retail increments before play when poll says not playing, but we simplify:
	c.Play(nxt)
	// advance next for next time
	nxt++
	if nxt > c.numTracks {
		nxt = 1
	}
	c.nextTrack = nxt
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
	if req < 1 {
		req = 1
	}
	if req > c.numTracks {
		req = c.numTracks
	}
	if c.curTrack != req || c.status != StatusPlaying {
		c.Play(req)
	}
}

// playCategoryShuffle implements mode 4 filtered shuffle.
// Retail scans (rand&0xF+1)*numTracks candidates forward wrapping 1..numTracks
// for first where trackCategory[track]==desiredCat. We mirror.
func (c *Controller) playCategoryShuffle() {
	if c.numTracks == 0 {
		return
	}
	if c.desiredCat < 1 || c.desiredCat > 4 {
		c.Stop()
		return
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if c.status == StatusPlaying && int(c.trackCategory[c.curTrack]) == c.desiredCat {
		// check if still playing? In tick we only call this when not playing (poll says not playing)
		// So if we are here, we are not playing, need to pick new.
	}
	r := c.drawCRT()
	u := int(r & 0xF) // 0..15
	tries := (u + 1) * c.numTracks
	cur := c.nextTrack
	if cur < 1 || cur > c.numTracks {
		cur = 1
	}
	for i := 0; i < tries; i++ {
		cur++
		if cur > c.numTracks {
			cur = 1
		}
		if int(c.trackCategory[cur]) == c.desiredCat {
			c.Play(cur)
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			return
		}
	}
	// no eligible track found after scan → stop silence
	c.Stop()
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// the mci poll `status cdaudio mode` vs "playing". When false, mode-specific
// transition fires; when true, nothing. While paused (status==2) early return.
// If numTracks==0 early return. Category mode 4 special at desired==4? Actually
// in retail desired==4 is still just another category, but there is a special
// case when desired==4 at entry that stops immediately (maybe test for MCI?).
// We keep generic.
//
// The controller must be polled each rendered frame (presentation), not each
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (c *Controller) Tick(isPlaying bool) {
	if c == nil || c.numTracks == 0 || c.status == StatusPaused {
		return
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	//
	// For now, handle playMode switch based on isPlaying.
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
	case ModeCategoryShuffle:
		c.playCategoryShuffle()
	case ModeIdle:
		c.Stop()
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
