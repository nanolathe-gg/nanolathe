package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats/zrb"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const introPath = "data/2.zrb"
const startupMoviePath = "data/1.zrb"
const creditsMoviePath = "data/5.zrb"

// introPlayback belongs to the shell, outside the authoritative tick. The
// decoder owns indexed frames; the device owns a finite soundtrack [fmt zrb].
type introPlayback struct {
	logical        string
	data           []byte
	decoder        *zrb.Decoder
	frame          *zrb.Frame
	sample         *audio.Sample
	player         audio.MusicPlayer
	elapsed        time.Duration
	paused, repeat bool
	cursors        *client.Cursors
	pixels         []byte
	palette        [256][4]byte
}

type movieOutput interface {
	NewMoviePlayer(*audio.Sample) (audio.MusicPlayer, error)
}

// queueStartupMovie arms only the normal interactive entry. It runs after
// the platform installs its audio output, before menu music or input service.
// The logo is Data/1.zrb [07 R-FE-01 §3]; playing it every normal launch in
// windowed mode as well is the requested portable startup policy.
func (g *gameShell) queueStartupMovie() {
	g.startupMoviePending = g.opts.Map == "" && g.opts.LoadSave == ""
}

func (g *gameShell) startIntro(cl *client.Client) error {
	repeat := cl != nil && cl.Input().Kbd.HasShift()
	return g.startMovie(cl, introPath, repeat)
}

// startMovieSequence preserves the router's ordered reels [08 R-OOS-01 §4].
func (g *gameShell) startMovieSequence(cl *client.Client, paths ...string) error {
	if cl == nil || g.intro != nil {
		return nil
	}
	g.movieQueue = append([]string(nil), paths...)
	return g.startNextMovie(cl)
}

func (g *gameShell) startNextMovie(cl *client.Client) error {
	for len(g.movieQueue) > 0 {
		logical := g.movieQueue[0]
		g.movieQueue = g.movieQueue[1:]
		if err := g.openMovie(cl, logical, false); err != nil {
			g.movieQueue = nil
			return g.movieFailure(logical, err)
		}
		if g.intro != nil {
			return nil
		}
		// Missing files skip individually, including a missing ending before
		// the common credits reel [08 R-OOS-01 §4].
	}
	g.movieQueue = nil
	g.openMenu(modeMenuMain)
	return nil
}

func (g *gameShell) startMovie(cl *client.Client, logical string, repeat bool) error {
	if err := g.openMovie(cl, logical, repeat); err != nil {
		return g.movieFailure(logical, err)
	}
	return nil
}

// openMovie resolves a cinematic through the normal mounted VFS.
// Windowed playback is a host adaptation: the original fullscreen/CD checks
// are obsolete here; sequencer/input behavior follows [08 R-OOS-01 §4].
func (g *gameShell) openMovie(cl *client.Client, logical string, repeat bool) error {
	if g.intro != nil || cl == nil {
		return nil
	}
	g.stopOrdinaryAudio()
	g.menuBGMPending = false
	if g.audioOwner != nil {
		g.audioOwner.StopStream()
		if g.audioOwner.Music != nil {
			g.audioOwner.Music.Stop()
		}
	}
	cl.Input().DrainTokens()
	data, err := g.cs.fs.ReadFileLimit(logical, 256<<20)
	if errors.Is(err, vfs.ErrNotFound) {
		g.armMenuBGM()
		return nil // Missing cinematics are silently skipped [08 R-OOS-01 §4].
	}
	if err != nil {
		return err
	}
	d, err := zrb.New(data)
	if err != nil {
		return err
	}
	m := &introPlayback{logical: logical, data: data, decoder: d, cursors: cl.Cursors(), repeat: repeat}
	// A single complete PCM track is small compared with decoded movie pixels.
	// Decode audio alone up front; video remains one mutable indexed frame.
	if _, available := audio.GlobalOutput().(movieOutput); available {
		for track, info := range d.Tracks {
			if !info.Present {
				continue
			}
			pcm, decodeErr := d.DecodeAudio(track)
			if decodeErr != nil {
				return decodeErr
			}
			m.sample = &audio.Sample{Alias: logical, Container: "raw", AudioFormat: 1,
				SampleRate: uint32(info.SampleRate), Channels: uint16(info.Channels),
				BitsPerSample: 16, BlockAlign: uint16(info.Channels * 2),
				ByteRate: uint32(info.SampleRate * info.Channels * 2), Data: pcm}
			break
		}
	}
	if err := m.begin(); err != nil {
		return err
	}
	g.intro = m
	if p := g.activePanel(); p != nil {
		p.ResetPress()
		p.CancelScrollDrag()
	}
	cl.SetCursors(nil)
	m.installFrame(cl)
	return nil
}

func (m *introPlayback) begin() error {
	f, err := m.decoder.Next()
	if err != nil {
		return err
	}
	m.frame, m.elapsed, m.paused = f, 0, false
	if m.sample != nil {
		if output, ok := audio.GlobalOutput().(movieOutput); ok {
			m.player, err = output.NewMoviePlayer(m.sample)
			if err != nil {
				return err
			}
			m.player.Play()
		}
	}
	return nil
}

func (g *gameShell) movieFailure(logical string, cause error) error {
	g.armMenuBGM()
	err := retailFrontendAssetError(g.cs, "play movie", logical, "Smacker 2 movie and PCM soundtrack", cause)
	return g.showRetailMessage(err.Error())
}

// Character messages skip; navigation/function keys and mouse clicks do not.
// Editing tokens below represent the control characters emitted by WM_CHAR
// [08 R-OOS-01 §4]; Alt+F4 also requests application shutdown.
func introSkip(in *input.State) (skip, quit bool) {
	quit = in.Kbd.KeyHeld(input.KeyAlt) && in.Kbd.KeyDown(input.KeyF4)
	skip = quit
	for _, token := range in.DrainTokens() {
		if token.Kind == input.TokenText {
			skip = true
		}
		if token.Kind == input.TokenEdit {
			switch token.Key {
			case input.KeyEscape, input.KeyEnter, input.KeyTab, input.KeyBackspace:
				skip = true
			}
		}
	}
	return skip, quit
}

func (g *gameShell) stepIntro(delta float64, cl *client.Client) {
	m := g.intro
	if skip, quit := introSkip(cl.Input()); skip {
		if quit {
			g.closeIntro(cl)
			cl.RequestExit()
		} else {
			g.finishMovie(cl)
		}
		return
	}
	// TODO(question): recover proprietary audio-cursor calibration and focus-loss
	// sound behavior from the remaining Smacker backend trace [03 §9]. Until
	// settled, use the device playback cursor and pause both clocks together.
	// Pause both clocks while unfocused. This is portable presentation policy,
	// preserving the retail focused-only frame pump [08 R-OOS-01 §4].
	if !cl.IsFocused() {
		if !m.paused && m.player != nil {
			m.player.Pause()
		}
		m.paused = true
		return
	}
	if m.paused {
		if m.player != nil {
			m.player.Play()
		}
		m.paused = false
		delta = 0
	}
	// The last frame was installed by the previous service pass. Retail ends
	// after decoding it, without waiting for a further interval or sound EOF;
	// a rounded device cursor may never reach that extra deadline [08 R-OOS-01 §4].
	if m.frame.Index+1 == m.decoder.FrameCount {
		if !m.repeat {
			g.finishMovie(cl)
			return
		}
		if m.player != nil {
			_ = m.player.Close()
			m.player = nil
		}
		d, err := zrb.New(m.data)
		if err == nil {
			m.decoder = d
			err = m.begin()
		}
		if err != nil {
			g.closeIntro(cl)
			reportRetailMessageError(g.movieFailure(m.logical, err))
			return
		}
		m.installFrame(cl)
		return
	}
	if delta > 0 {
		m.elapsed += time.Duration(delta * float64(time.Second))
	}
	if m.player != nil {
		if err := m.player.Err(); err != nil {
			g.closeIntro(cl)
			reportRetailMessageError(g.movieFailure(m.logical, err))
			return
		}
		if !m.player.Completed() {
			m.elapsed = time.Duration(m.player.PositionMillis()) * time.Millisecond
		}
	}
	due := time.Duration(m.frame.Index+1) * m.decoder.FrameDuration
	if m.elapsed < due {
		return
	}
	// One sequential decode per focused service pass. Never jump over delta
	// frames; they depend on the prior image and palette [fmt zrb].
	f, err := m.decoder.Next()
	if err != nil {
		g.closeIntro(cl)
		reportRetailMessageError(g.movieFailure(m.logical, err))
		return
	}
	m.frame = f
	if m.player == nil || m.player.Completed() {
		// With no audio cursor, reschedule a late frame instead of retaining an
		// arbitrarily large catch-up debt after a stalled host.
		if m.elapsed >= due+m.decoder.FrameDuration {
			m.elapsed = due
		}
	}
	m.installFrame(cl)
}

func (m *introPlayback) installFrame(cl *client.Client) {
	for i, c := range m.frame.Palette {
		m.palette[i] = [4]byte{c.R, c.G, c.B, 255}
	}
	cl.SetMoviePalette(&m.palette)
	d := m.decoder
	if !d.Interlaced {
		m.pixels = m.frame.Pixels
		return
	}
	// Flag 2 advances two destination rows per encoded row, preserving the
	// black intervening scanlines [08 R-OOS-01 §4][fmt zrb].
	size := d.Width * d.DisplayHeight
	if cap(m.pixels) < size {
		m.pixels = make([]byte, size)
	}
	m.pixels = m.pixels[:size]
	for i := range m.pixels {
		m.pixels[i] = 0
	}
	for y := 0; y < d.Height; y++ {
		copy(m.pixels[y*2*d.Width:], m.frame.Pixels[y*d.Width:(y+1)*d.Width])
	}
}

func (g *gameShell) drawIntro(cl *client.Client) {
	m := g.intro
	d := m.decoder
	w, h := cl.Size()
	cl.UIFillRect(0, 0, w, h, 0)
	sourceHeight := d.Height
	if d.Interlaced {
		sourceHeight = d.DisplayHeight
	}
	cl.UIBlitIndexed(m.pixels, d.Width, sourceHeight, 0, (h-d.DisplayHeight)>>1, d.Width, d.DisplayHeight)
}

func (g *gameShell) closeIntro(cl *client.Client) {
	g.movieQueue = nil
	if g.intro == nil {
		return
	}
	g.releaseMovie(cl)
	g.openMenu(modeMenuMain)
}

func (g *gameShell) finishMovie(cl *client.Client) {
	g.releaseMovie(cl)
	reportRetailMessageError(g.startNextMovie(cl))
}

// Release one reel without rebuilding the menu or starting BGM between reels.
func (g *gameShell) releaseMovie(cl *client.Client) {
	if g.intro == nil {
		return
	}
	m := g.intro
	if m.player != nil {
		if err := m.player.Close(); err != nil {
			reportRetailMessageError(fmt.Errorf("nanolathe: close movie audio: logical path %s, providers searched [PCM device], expected stopped soundtrack: %w", m.logical, err))
		}
	}
	g.intro = nil
	cl.SetCursors(m.cursors)
	cl.SetMoviePalette(nil)
	cl.Input().DrainTokens()
}
