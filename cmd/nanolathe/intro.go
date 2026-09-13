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

// introPlayback belongs to the shell, outside the authoritative tick. The
// decoder owns indexed frames; the device owns a finite soundtrack [fmt zrb].
type introPlayback struct {
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

// startIntro resolves the original resource through the normal mounted VFS.
// Windowed playback is a host adaptation: the original fullscreen/CD checks
// are obsolete here; sequencer/input behavior follows [08 R-OOS-01 §4].
func (g *gameShell) startIntro(cl *client.Client) error {
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
	data, err := g.cs.fs.ReadFileLimit(introPath, 256<<20)
	if errors.Is(err, vfs.ErrNotFound) {
		g.armMenuBGM()
		return nil // Missing cinematics are silently skipped [08 R-OOS-01 §4].
	}
	if err != nil {
		return g.introFailure(err)
	}
	d, err := zrb.New(data)
	if err != nil {
		return g.introFailure(err)
	}
	m := &introPlayback{data: data, decoder: d, cursors: cl.Cursors(), repeat: cl.Input().Kbd.HasShift()}
	// A single complete PCM track is small compared with decoded movie pixels.
	// Decode audio alone up front; video remains one mutable indexed frame.
	if _, available := audio.GlobalOutput().(movieOutput); available {
		for track, info := range d.Tracks {
			if !info.Present {
				continue
			}
			pcm, decodeErr := d.DecodeAudio(track)
			if decodeErr != nil {
				return g.introFailure(decodeErr)
			}
			m.sample = &audio.Sample{Alias: introPath, Container: "raw", AudioFormat: 1,
				SampleRate: uint32(info.SampleRate), Channels: uint16(info.Channels),
				BitsPerSample: 16, BlockAlign: uint16(info.Channels * 2),
				ByteRate: uint32(info.SampleRate * info.Channels * 2), Data: pcm}
			break
		}
	}
	if err := m.begin(); err != nil {
		return g.introFailure(err)
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

func (g *gameShell) introFailure(cause error) error {
	g.armMenuBGM()
	err := retailFrontendAssetError(g.cs, "play intro", introPath, "Smacker 2 movie and PCM soundtrack", cause)
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
		g.closeIntro(cl)
		if quit {
			cl.RequestExit()
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
			g.closeIntro(cl)
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
			reportRetailMessageError(g.introFailure(err))
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
			reportRetailMessageError(g.introFailure(err))
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
		reportRetailMessageError(g.introFailure(err))
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
	if g.intro == nil {
		return
	}
	m := g.intro
	if m.player != nil {
		if err := m.player.Close(); err != nil {
			reportRetailMessageError(fmt.Errorf("nanolathe: close intro audio: logical path %s, providers searched [PCM device], expected stopped soundtrack: %w", introPath, err))
		}
	}
	g.intro = nil
	cl.SetCursors(m.cursors)
	cl.SetMoviePalette(nil)
	cl.Input().DrainTokens()
	g.openMenu(modeMenuMain)
}
