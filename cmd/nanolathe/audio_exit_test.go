package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type exitMusicPlayer struct {
	playing, closed bool
	volume          float64
}

func (p *exitMusicPlayer) Play()               { p.playing = true }
func (p *exitMusicPlayer) Pause()              { p.playing = false }
func (p *exitMusicPlayer) IsPlaying() bool     { return p.playing }
func (p *exitMusicPlayer) SetVolume(v float64) { p.volume = v }
func (p *exitMusicPlayer) PositionMillis() int { return 0 }
func (p *exitMusicPlayer) Completed() bool     { return false }
func (p *exitMusicPlayer) Err() error          { return nil }
func (p *exitMusicPlayer) Close() error        { p.closed = true; p.playing = false; return nil }

type exitMusicOutput struct {
	player       exitMusicPlayer
	loops, stops int
}

func (o *exitMusicOutput) PlaySample(*audio.Sample, float64, float64) error           { return nil }
func (o *exitMusicOutput) PlayRegisteredSample(*audio.Sample, float64, float64) error { return nil }
func (o *exitMusicOutput) NewMusicPlayer(io.ReadSeeker, string) (audio.MusicPlayer, error) {
	o.player = exitMusicPlayer{}
	return &o.player, nil
}
func (o *exitMusicOutput) PlayLoopingRegisteredSample(*audio.Sample, float64, float64) error {
	o.loops++
	return nil
}
func (o *exitMusicOutput) StopVoices() { o.stops++ }

func TestBattleExitFadeSurvivesShellMenuPump(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "music"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "music", "1.wav"), []byte("authored fake driver media"), 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	s := audio.NewService(fs)
	var now uint32
	s.Music.SetPresentationClock(func() uint32 { return now })
	s.ConfigureMusic(false)
	s.Music.Configure(audio.ModeCategoryShuffle, 0)
	s.Music.SetTrackCategory(1, 0)
	s.Music.SetVolume(32)
	if _, err := s.Cache.Put(menuBGMAlias, []byte{128, 128}); err != nil {
		t.Fatal(err)
	}
	out := &exitMusicOutput{}
	old := audio.GlobalOutput()
	audio.SetGlobalOutput(out)
	t.Cleanup(func() { audio.SetGlobalOutput(old) })
	s.StartBattleMusic()
	if !s.Music.Play(1) {
		t.Fatal("fake music start")
	}
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.sess.Audio = s
	g := &gameShell{audioOwner: s, cs: &contentSet{fs: fs}, frontend: ui.NewFrontend(modeMenuMain), battle: b}
	b.shell = g
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetFocused(true)
	cl.SetAudioService(s)
	g.teardownBattle(cl) // reaches detachBattleAudio, then releases the session
	if g.battle != nil || b.sess != nil || out.player.closed {
		t.Fatal("teardown cut music off or retained battle")
	}
	g.frontend.Mode = modeMenuMain
	cl.SetAudioService(g.audioOwner) // menu rebinding must leave fade state intact
	g.armMenuBGM()
	now = 2
	g.step(1.0/30, cl)
	want := float64((32<<10)-(32<<10)/18) / 65535
	if out.player.closed || out.player.volume != want || out.loops != 1 || out.stops == 0 {
		t.Fatalf("menu fade=%g closed=%v loops=%d stops=%d", out.player.volume, out.player.closed, out.loops, out.stops)
	}
	// The existing focus gate remains: background menu steps do not service
	// semantic music timers, and resuming services one callback per pump.
	cl.SetFocused(false)
	now = 20
	g.step(1.0/30, cl)
	if out.player.volume != want {
		t.Fatal("unfocused shell advanced semantic fade")
	}
	cl.SetFocused(true)
	for range 18 {
		now += 2
		g.step(1.0/30, cl)
	}
	if !out.player.closed || s.Music.IsPlaying() {
		t.Fatal("shell never completed exit fade")
	}
	// The next battle adopts the same shell service. Returning from category
	// 4 starts its player synchronously; the next shell pump must not add a
	// second explicit category tick (and presentation random draw).
	next := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	next.sess.Audio = s
	attachBattleAudio(cl, next.sess, fs)
	s.StartBattleMusic()
	if !out.player.playing || out.player.closed {
		t.Fatal("reentry failed to start physical media")
	}
	applications := s.Music.VolumeApplications()
	g.step(1.0/30, cl)
	if s.Music.VolumeApplications() != applications {
		t.Fatal("shell duplicated reentry category tick")
	}
}
