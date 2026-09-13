package audio

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"testing"
)

func TestBattleMusicStrictThresholdsAndBucketOrder(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		current, previous, older int32
		units                    int
		evals                    int32
		want                     int
	}{
		{"sum30 equal", 50, 0, 0, 31, 10, 0},
		{"sum30 above", 51, 0, 0, 31, 10, 1},
		{"current excluded from five", 31, 0, 0, 31, 10, 0},
		{"previous five above", 0, 31, 0, 31, 10, 1},
		{"previous five equal", 0, 30, 0, 31, 10, 0},
		{"sixth bucket excluded", 0, 0, 31, 31, 10, 0},
		{"thirty units", 51, 0, 0, 30, 10, 0},
		{"unit count low word", 51, 0, 0, 65566, 10, 0},
		{"ten evaluations", 51, 0, 0, 31, 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(nil)
			now := uint32(31)
			s.Music.SetPresentationClock(func() uint32 { return now })
			s.StartBattleMusic()
			s.intensity.evals = tc.evals
			s.intensity.buckets[0], s.intensity.buckets[29], s.intensity.buckets[24] = tc.current, tc.previous, tc.older
			s.UpdateBattleMusic(tc.units, false)
			if s.Music.DesiredCategory() != tc.want {
				t.Fatalf("category=%d want %d", s.Music.DesiredCategory(), tc.want)
			}
		})
	}
}

func TestBattleMusicCalmAdmissionClockAndBattleLifetime(t *testing.T) {
	s := NewService(nil)
	now := uint32(30)
	s.Music.SetPresentationClock(func() uint32 { return now })
	s.StartBattleMusic()
	s.UpdateBattleMusic(100, false)
	if s.intensity.evals != 0 {
		t.Fatal("equal clock threshold evaluated")
	}
	now = 31
	s.UpdateBattleMusic(100, true)
	if s.intensity.evals != 0 {
		t.Fatal("terminal result evaluated")
	}
	s.Music.Configure(ModeSequential, 1)
	s.intensity.lastWant, s.intensity.evals = 1, 59
	s.UpdateBattleMusic(100, false)
	if s.Music.DesiredCategory() != 1 {
		t.Fatal("calm at sixty evaluations")
	}
	now += 31
	s.intensity.buckets[s.intensity.current] = 10
	s.UpdateBattleMusic(100, false)
	if s.Music.DesiredCategory() != 1 {
		t.Fatal("calm at sum30 ten")
	}
	now += 31
	s.intensity.buckets = [30]int32{}
	s.UpdateBattleMusic(100, false)
	if s.Music.DesiredCategory() != 0 || s.intensity.evals != 0 {
		t.Fatal("calm transition absent")
	}
	// Battle entry clears the ring/index but retains process chooser history.
	s.intensity.evals = 17
	stamp := s.intensity.stamp
	s.EndBattleMusic()
	s.StartBattleMusic()
	if s.intensity.evals != 17 || s.intensity.stamp != stamp || s.intensity.lastWant != 0 || s.intensity.current != 0 {
		t.Fatal("battle entry reset retained chooser state")
	}
	// The retail clock test adds thirty before comparing unsigned, including
	// wrap. It is deliberately not a signed-elapsed comparison.
	s.intensity.stamp = ^uint32(0) - 10
	now = 20
	s.UpdateBattleMusic(100, false)
	if s.intensity.evals != 18 {
		t.Fatal("wrapped clock deadline not admitted")
	}
}

func TestBattleMusicRetainedActivityAndExitFade(t *testing.T) {
	s := NewService(nil)
	var now uint32
	m := s.Music
	m.SetPresentationClock(func() uint32 { return now })
	m.Open(2)
	m.Configure(ModeCategoryShuffle, 0)
	m.SetVolume(32)
	p := &musicProbePlayer{}
	m.openTrack = func(int) (MusicPlayer, error) { return p, nil }
	s.StartBattleMusic()
	m.Play(1)
	events := []frame.EventView{{Kind: frame.EventKindMusicIntensity, Magnitude: 1}, {Kind: frame.EventKindMusicIntensity, Magnitude: 5}}
	s.DrainEvents(100, events)
	s.DrainEvents(100, events)
	if s.intensity.buckets[0] != 6 {
		t.Fatal("activity replayed or lost")
	}
	s.EndBattleMusic()
	if p.closed || m.DesiredCategory() != 4 {
		t.Fatal("exit cut off fade")
	}
	now = 2
	if err := s.ServiceMusic(); err != nil {
		t.Fatal(err)
	}
	if p.closed || p.volume != float64((32<<10)-(32<<10)/18)/65535 {
		t.Fatalf("first exit fade step=%g closed=%v", p.volume, p.closed)
	}
	for range 18 {
		now += 2
		if err := s.ServiceMusic(); err != nil {
			t.Fatal(err)
		}
	}
	if !p.closed || m.IsPlaying() {
		t.Fatal("exit fade never released media")
	}
}

func TestBattleMusicRetainedWantCanSuppressLaterBattleTransition(t *testing.T) {
	s := NewService(nil)
	now := uint32(1000)
	s.Music.SetPresentationClock(func() uint32 { return now })
	s.StartBattleMusic()
	s.intensity.lastWant, s.intensity.evals = 1, 10
	s.EndBattleMusic()
	s.StartBattleMusic()
	s.intensity.buckets[0] = 51
	s.UpdateBattleMusic(31, false)
	if s.Music.DesiredCategory() != 0 || s.intensity.evals != 11 {
		t.Fatal("retained Battle want incorrectly reset or retriggered")
	}
}

func TestBattleMusicDefersMissingDeviceButPreservesActiveTransition(t *testing.T) {
	old := GlobalOutput()
	SetGlobalOutput(nil)
	t.Cleanup(func() { SetGlobalOutput(old) })
	s := NewService(musicFS(t, "1.wav"))
	s.ConfigureMusic(false)
	s.Music.Configure(ModeCategoryShuffle, 0)
	s.Music.SetTrackCategory(1, 0)
	s.Music.SetVolume(32)
	var now uint32
	s.Music.SetPresentationClock(func() uint32 { return now })
	// A modeled playing status alone cannot prove there is a device player.
	s.Music.status = StatusPlaying
	s.StartBattleMusic()
	if !s.musicStartPending {
		t.Fatal("modeled status suppressed deferred device start")
	}
	if err := s.ServiceMusic(); err != nil {
		t.Fatal(err)
	}
	out := &musicProbeOutput{}
	SetGlobalOutput(out)
	if err := s.ServiceMusic(); err != nil {
		t.Fatal(err)
	}
	if len(out.players) != 1 || !out.players[0].playing {
		t.Fatal("late device never received battle music")
	}
	// A live transition to Building already has its own fade and delay; the
	// host's entry request must not insert an explicit tick through either.
	s.Music.Configure(ModeCategoryShuffle, 1)
	s.StartBattleMusic()
	if s.musicStartPending || s.Music.fadeTimer < 0 {
		t.Fatal("entry tick superseded active transition")
	}
	now = 2
	s.ServiceMusic()
	if out.players[0].volume >= float64(32<<10)/65535 {
		t.Fatal("entry restored volume during fade")
	}
	for range 18 {
		now += 2
		s.ServiceMusic()
	}
	s.StartBattleMusic()
	if s.musicStartPending || s.Music.delayTimer < 0 {
		t.Fatal("entry tick superseded Building delay")
	}
	s.ServiceMusic()
	if out.players[0].volume != 0 {
		t.Fatal("entry restored volume during Building delay")
	}
}
