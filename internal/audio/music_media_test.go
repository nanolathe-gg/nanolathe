package audio

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func musicFS(t *testing.T, names ...string) vfs.FSOps {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "music"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, "music", name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

// The CD audio count excludes the data track and duplicate packaged extras
// [03 R-AUD-01 §4 "Packaged MP3 media"]. Contents here are authored placeholders.
func TestPackagedMusicLogicalMapping(t *testing.T) {
	var names []string
	for i := 0; i < 18; i++ {
		names = append(names, fmt.Sprintf("%d.mp3", i))
	}
	fs := musicFS(t, names...)
	s := NewService(fs)
	s.ConfigureMusic(false)
	if ProbeMusicTracks(fs) != 16 || s.Music.NumTracks() != 16 {
		t.Fatal("data/duplicate tracks included")
	}
	for i, path := range s.musicTracks {
		if path != fmt.Sprintf("music/%d.mp3", i+2) {
			t.Fatalf("logical %d maps to %s", i+1, path)
		}
		want := uint8(0)
		if i < 7 {
			want = 1
		}
		if s.Music.trackCategory[i+1] != want {
			t.Fatalf("category for logical %d", i+1)
		}
	}
	s.Music.trackCategory[1] = 0
	s.ConfigureMusic(false)
	if s.Music.trackCategory[1] != 0 {
		t.Fatal("reconfiguration overwrote edited category")
	}
}

// Directory ordering is a Nanolathe host policy, not retail evidence.
func TestMusicDirectoryNumericOrder(t *testing.T) {
	paths := MusicTracks(musicFS(t, "10.wav", "2.wav", "1.wav"))
	if strings.Join(paths, ",") != "music/1.wav,music/2.wav,music/10.wav" {
		t.Fatal(paths)
	}
}

type musicProbePlayer struct {
	playing, completed, closed bool
	volume                     float64
	err                        error
}

func (p *musicProbePlayer) Play()               { p.playing = true }
func (p *musicProbePlayer) Pause()              { p.playing = false }
func (p *musicProbePlayer) IsPlaying() bool     { return p.playing }
func (p *musicProbePlayer) SetVolume(v float64) { p.volume = v }
func (p *musicProbePlayer) PositionMillis() int { return 123 }
func (p *musicProbePlayer) Completed() bool     { return p.completed }
func (p *musicProbePlayer) Err() error          { return p.err }
func (p *musicProbePlayer) Close() error        { p.closed = true; p.playing = false; return nil }

type musicProbeOutput struct {
	outputSpy
	names   []string
	players []*musicProbePlayer
}

func (o *musicProbeOutput) NewMusicPlayer(source io.ReadSeeker, _ string) (MusicPlayer, error) {
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	o.names = append(o.names, string(data))
	p := &musicProbePlayer{}
	o.players = append(o.players, p)
	return p, nil
}

// Completion may select the same track in Repeat/Random/Custom. The stopped
// device must reopen even though the controller still says Playing
// [03 R-AUD-01 §4]. Pause and failures must not create completion events.
func TestMusicMediaCompletionPauseAndFailure(t *testing.T) {
	old := GlobalOutput()
	SetGlobalOutput(nil)
	t.Cleanup(func() { SetGlobalOutput(old) })
	s := NewService(musicFS(t, "1.wav"))
	s.ConfigureMusic(false)
	s.Music.Configure(ModeSingle, 0)
	s.Music.nextTrack = 1
	s.Music.SetVolume(32)
	s.StartMusic()
	if err := s.ServiceMusic(); err != nil {
		t.Fatal(err)
	}
	if s.Music.IsPlaying() {
		t.Fatal("started before device installation")
	}
	out := &musicProbeOutput{}
	SetGlobalOutput(out)
	if err := s.ServiceMusic(); err != nil {
		t.Fatal(err)
	}
	p := out.players[0]
	if !p.playing || p.volume != float64(32<<10)/65535 {
		t.Fatal("start or music gain missing")
	}
	s.Music.Pause(true)
	p.completed = true
	if err := s.ServiceMusic(); err != nil {
		t.Fatal(err)
	}
	if len(out.players) != 1 {
		t.Fatal("pause treated as completion")
	}
	s.Music.Pause(false)
	if !p.playing {
		t.Fatal("did not resume same player")
	}
	p.playing = false
	if err := s.ServiceMusic(); err != nil {
		t.Fatal(err)
	}
	if len(out.players) != 2 || !p.closed || out.names[1] != "1.wav" {
		t.Fatal("same-track completion did not restart")
	}
	next := out.players[1]
	s.Music.SetVolume(64)
	if next.volume != 1 {
		t.Fatal("live maximum gain not clamped")
	}
	// An expiring fade must not replace the failed player before its error
	// is drained. Zero volume makes the next fade callback finish at once.
	var now uint32
	s.Music.SetPresentationClock(func() uint32 { return now })
	s.Music.Configure(ModeCategoryShuffle, 0)
	s.Music.SetTrackCategory(1, 1)
	s.Music.SetVolume(0)
	s.Music.SetDesired(1)
	now = 2
	next.err = errors.New("broken stream")
	next.playing = false
	err := s.ServiceMusic()
	if err == nil || !strings.Contains(err.Error(), "logical path music/1.wav") {
		t.Fatalf("lost provenance: %v", err)
	}
	if !next.closed || len(out.players) != 2 {
		t.Fatal("failed stream advanced music")
	}
	if err := s.ServiceMusic(); err != nil {
		t.Fatal("reported failure twice")
	}
}

func TestMusicFadeReachesDeviceAndDisableClosesIt(t *testing.T) {
	m := NewMusicController()
	m.Open(2)
	m.Configure(ModeCategoryShuffle, 0)
	m.SetVolume(32)
	var now uint32
	m.SetPresentationClock(func() uint32 { return now })
	p := &musicProbePlayer{}
	m.openTrack = func(int) (MusicPlayer, error) { return p, nil }
	m.Play(1)
	m.SetDesired(1)
	now = 2
	m.ServiceTimers()
	if p.volume != float64((32<<10)-(32<<10)/18)/65535 {
		t.Fatalf("fade gain = %g", p.volume)
	}
	m.SetEnabled(false)
	if !p.closed || m.Status() != StatusIdle {
		t.Fatal("disable did not release music")
	}
}
