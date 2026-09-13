package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats/zrb"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestIntroCharacterSkip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token input.Token
		want  bool
	}{
		{"letter", input.Token{Kind: input.TokenText, Rune: 'x'}, true},
		{"escape", input.Token{Kind: input.TokenEdit, Key: input.KeyEscape}, true},
		{"enter", input.Token{Kind: input.TokenEdit, Key: input.KeyEnter}, true},
		{"tab", input.Token{Kind: input.TokenEdit, Key: input.KeyTab}, true},
		{"backspace", input.Token{Kind: input.TokenEdit, Key: input.KeyBackspace}, true},
		{"arrow", input.Token{Kind: input.TokenEdit, Key: input.KeyLeft}, false},
		{"function", input.Token{Kind: input.TokenEdit, Key: input.KeyF1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input.NewState()
			in.EnqueueToken(tc.token)
			skip, quit := introSkip(in)
			if skip != tc.want || quit || in.PendingTokens() != 0 {
				t.Fatalf("skip=%v quit=%v pending=%d", skip, quit, in.PendingTokens())
			}
		})
	}
	in := input.NewState()
	in.Kbd.SetKey(input.KeyAlt, true)
	in.Kbd.SetKey(input.KeyF4, true)
	if skip, quit := introSkip(in); !skip || !quit {
		t.Fatal("Alt+F4 did not exit")
	}
}

// Flag 2 preserves black alternate rows, rather than duplicating the image.
func TestIntroScanlinesAndPalette(t *testing.T) {
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 8, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	m := &introPlayback{decoder: &zrb.Decoder{Width: 4, Height: 2, DisplayHeight: 4, Interlaced: true}, frame: &zrb.Frame{Pixels: []byte{1, 1, 1, 1, 2, 2, 2, 2}}}
	m.frame.Palette[1] = color.RGBA{R: 240, A: 255}
	m.frame.Palette[2] = color.RGBA{G: 240, A: 255}
	m.installFrame(cl)
	for y := 0; y < 4; y++ {
		want := byte(0)
		if y == 0 {
			want = 1
		}
		if y == 2 {
			want = 2
		}
		for x := 0; x < 4; x++ {
			if got := m.pixels[y*4+x]; got != want {
				t.Fatalf("row %d got %d want %d", y, got, want)
			}
		}
	}
	if cl.DisplayPalette()[1][0] != 240 {
		t.Fatal("movie palette not installed")
	}
}

func TestIntroMissingAndMalformed(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	for _, malformed := range []bool{false, true} {
		root := t.TempDir()
		if malformed {
			if err := os.Mkdir(filepath.Join(root, "Data"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "Data", "2.zrb"), []byte("authored invalid movie"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		fs := vfs.New()
		if err := fs.MountDirectory(root, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fs.Close() })
		g.cs = &contentSet{fs: fs}
		queuedWindowTail(cl)
		g.activateGadget("INTRO")
		if g.intro != nil || cl.Input().PendingTokens() != 0 {
			t.Fatal("failed movie retained playback or input")
		}
		modal := g.frontend.Panels.Modal()
		if malformed {
			if modal == nil || !strings.Contains(modal.Message(), introPath) {
				t.Fatal("missing malformed movie diagnostic")
			}
			cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
			g.menuInput(cl)
		} else if modal != nil {
			t.Fatal("missing movie should skip silently")
		}
	}
}

func TestIntroRetailPlaybackAndReturn(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	oldOutput := audio.GlobalOutput()
	audio.SetGlobalOutput(nil)
	t.Cleanup(func() { audio.SetGlobalOutput(oldOutput) })
	if _, err := g.cs.fs.Stat(introPath); err != nil {
		t.Skip("optional original intro movie absent")
	}
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	loadedCursors, err := client.LoadCursors(g.cs.fs)
	if err != nil {
		t.Fatal(err)
	}
	cl.SetCursors(loadedCursors)
	before := cl.DisplayPalette()
	cursors := cl.Cursors()
	queuedWindowTail(cl)
	g.activateGadget("INTRO")
	if g.intro == nil {
		t.Fatal("Intro button did not open movie")
	}
	t.Cleanup(func() { g.closeIntro(cl) })
	if cl.Cursors() != nil || cl.Input().PendingTokens() != 0 {
		t.Fatal("movie did not own cursor/input")
	}
	cl.SetFocused(true)
	// No device is installed by the fixture, so advance using the silent clock.
	for range 180 {
		g.stepIntro(0.03333, cl)
	}
	if g.intro == nil || g.intro.frame.Index != 180 {
		t.Fatal("authored cadence did not advance sequentially")
	}
	if dir := os.Getenv("NANOLATHE_INTRO_SHOT"); dir != "" {
		writeShellShot(t, cl, filepath.Join(dir, "intro.png"))
	}
	at := g.intro.frame.Index
	cl.SetFocused(false)
	g.stepIntro(10, cl)
	if g.intro.frame.Index != at {
		t.Fatal("unfocused movie advanced")
	}
	cl.SetFocused(true)
	g.stepIntro(10, cl)
	if g.intro.frame.Index != at {
		t.Fatal("focus restore counted inactive time")
	}
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: ' '})
	g.stepIntro(0, cl)
	if g.intro != nil || cl.Cursors() != cursors || cl.DisplayPalette() != before || g.frontend.Mode != modeMenuMain {
		t.Fatal("skip did not restore main menu")
	}

	// Shift is sampled once; release does not cancel the repeat request.
	cl.Input().Kbd.SetKey(input.KeyShift, true)
	if err := g.startIntro(cl); err != nil {
		t.Fatal(err)
	}
	cl.Input().Kbd.SetKey(input.KeyShift, false)
	if !g.intro.repeat {
		t.Fatal("Shift activation did not latch repeat")
	}
	g.intro.frame.Index = g.intro.decoder.FrameCount - 1
	g.intro.elapsed = time.Duration(g.intro.decoder.FrameCount) * g.intro.decoder.FrameDuration
	g.stepIntro(0, cl)
	if g.intro == nil || g.intro.frame.Index != 0 {
		t.Fatal("Shift repeat did not reopen movie")
	}
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	g.stepIntro(0, cl)
	if g.intro != nil {
		t.Fatal("character failed to cancel repeat")
	}
	// EOF returns through the same cleanup seam, with the last frame visible
	// before the next service returns. Avoid replaying the entire movie here.
	if err := g.startIntro(cl); err != nil {
		t.Fatal(err)
	}
	g.intro.frame.Index = g.intro.decoder.FrameCount - 1
	g.intro.elapsed = time.Duration(g.intro.decoder.FrameCount) * g.intro.decoder.FrameDuration
	g.stepIntro(0, cl)
	if g.intro != nil {
		t.Fatal("EOF did not restore menu")
	}
}

// The device cursor is millisecond-valued and may stop short of the exact
// movie interval. The final decoded frame, rather than another audio deadline,
// completes playback [08 R-OOS-01 §4].
func TestIntroFinalFrameDoesNotWaitForRoundedAudioCursor(t *testing.T) {
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetFocused(true)
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	player := &introCursorPlayer{position: 66}
	m := &introPlayback{decoder: &zrb.Decoder{FrameCount: 2, FrameDuration: 33330 * time.Microsecond}, frame: &zrb.Frame{Index: 1}, player: player}
	g := &gameShell{intro: m, frontend: ui.NewFrontend(modeMenuMain)}
	g.stepIntro(1, cl)
	if g.intro != nil || !player.closed {
		t.Fatal("final frame waits forever on rounded audio position")
	}
}

type introCursorPlayer struct {
	position int
	closed   bool
}

// The requested startup policy runs once at ordinary interactive entry and
// leaves direct battle entry alone [07 R-FE-01 §3].
func TestStartupMovieEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want bool
	}{
		{"menu", Options{}, true},
		{"map", Options{Map: "authored-map"}, false},
		{"save", Options{LoadSave: "authored-save"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &gameShell{opts: tc.opts}
			g.queueStartupMovie()
			if g.startupMoviePending != tc.want || g.intro != nil {
				t.Fatal("startup selection opened playback before the platform was ready")
			}
		})
	}
}

func TestStartupMovieRetailAudioAndReturn(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	if _, err := g.cs.fs.Stat(startupMoviePath); err != nil {
		t.Skip("optional original startup movie absent")
	}
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	oldOutput := audio.GlobalOutput()
	audio.SetGlobalOutput(nil)
	t.Cleanup(func() { audio.SetGlobalOutput(oldOutput) })
	g.queueStartupMovie()
	// The backend becomes available after shell construction. Its first update
	// must use that device, while Shift must not repeat the startup logo.
	output := &startupMovieOutput{}
	audio.SetGlobalOutput(output)
	cl.SetFocused(true)
	cl.Input().Kbd.SetKey(input.KeyShift, true)
	g.step(0, cl)
	t.Cleanup(func() { g.closeIntro(cl) })
	if g.startupMoviePending || g.intro == nil || g.intro.logical != startupMoviePath || g.intro.repeat {
		t.Fatal("startup did not enter the single-pass logo")
	}
	if output.sample == nil || output.sample.Alias != startupMoviePath || len(output.sample.Data) == 0 || output.loops != 0 {
		t.Fatal("startup soundtrack missing or overlapped by menu music")
	}
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	g.step(0, cl)
	if g.intro != nil || !output.player.closed || !g.menuBGMPending || g.frontend.Mode != modeMenuMain {
		t.Fatal("startup skip did not close sound and restore the menu")
	}
	g.step(0, cl)
	g.openMenu(modeMenuMain)
	g.step(0, cl)
	if g.intro != nil || g.startupMoviePending {
		t.Fatal("returning to the main menu replayed startup")
	}
}

type startupMovieOutput struct {
	shellLoopOutput
	sample *audio.Sample
	player introCursorPlayer
}

func (o *startupMovieOutput) NewMoviePlayer(sample *audio.Sample) (audio.MusicPlayer, error) {
	o.sample = sample
	return &o.player, nil
}

func (*introCursorPlayer) Play()                 {}
func (*introCursorPlayer) Pause()                {}
func (*introCursorPlayer) IsPlaying() bool       { return true }
func (*introCursorPlayer) SetVolume(float64)     {}
func (p *introCursorPlayer) PositionMillis() int { return p.position }
func (*introCursorPlayer) Completed() bool       { return false }
func (*introCursorPlayer) Err() error            { return nil }
func (p *introCursorPlayer) Close() error        { p.closed = true; return nil }
