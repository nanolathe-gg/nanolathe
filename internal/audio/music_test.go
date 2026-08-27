package audio

import "testing"

func TestMusic_SequentialWrap(t *testing.T) {
	m := NewMusicController()
	m.Open(3)
	m.Configure(ModeSequential, 0)
	m.Play(3)
	if m.CurTrack() != 3 {
		t.Fatalf("cur %d want 3", m.CurTrack())
	}
	// not playing → tick should increment to 1 wrap via playSequential that uses nextTrack
	m.Stop()
	m.SetNumTracks(3)
	m.Configure(ModeSequential, 0)
	// after stop next=1, cur=0 idle
	m.Tick(false) // not playing → seq plays 1
	if m.CurTrack() != 1 {
		t.Fatalf("seq first %d want 1", m.CurTrack())
	}
	m.NotifyTrackEnd() // track ends
	m.Tick(false)      // should advance to 2
	if m.CurTrack() != 2 {
		t.Fatalf("seq second %d want 2", m.CurTrack())
	}
	m.NotifyTrackEnd()
	m.Tick(false) // 3
	if m.CurTrack() != 3 {
		t.Fatalf("seq third %d want 3", m.CurTrack())
	}
	m.NotifyTrackEnd()
	m.Tick(false) // wrap to 1
	if m.CurTrack() != 1 {
		t.Fatalf("seq wrap %d want 1", m.CurTrack())
	}
}

func TestMusic_RandomDeterministic(t *testing.T) {
	m := NewMusicController()
	m.Open(10)
	m.Configure(ModeRandom, 0)
	m.Seed(12345)
	// two controllers same seed should pick same sequence when ticking with same isPlaying false
	m2 := NewMusicController()
	m2.Open(10)
	m2.Configure(ModeRandom, 0)
	m2.Seed(12345)
	seq1 := []int{}
	seq2 := []int{}
	for i := 0; i < 5; i++ {
		m.Tick(false)
		seq1 = append(seq1, m.CurTrack())
		m.NotifyTrackEnd()
		m2.Tick(false)
		seq2 = append(seq2, m2.CurTrack())
		m2.NotifyTrackEnd()
	}
	for i := range seq1 {
		if seq1[i] != seq2[i] {
			t.Fatalf("random seq diverged at %d %d vs %d", i, seq1[i], seq2[i])
		}
		if seq1[i] < 1 || seq1[i] > 10 {
			t.Fatalf("track out of range %d", seq1[i])
		}
	}
}

func TestMusic_CategoryShuffle(t *testing.T) {
	m := NewMusicController()
	m.Open(10)
	// trackCategory[1]=1,2=2,3=3,4=4,5=1 etc for 10 tracks: 1:1,2:2,3:3,4:4,5:1,6:2,7:3,8:4,9:1,10:2
	m.Configure(ModeCategoryShuffle, 3) // desired 3
	m.Seed(77)
	m.Tick(false)
	if m.CurTrack() == 0 {
		t.Fatalf("category shuffle should pick track")
	}
	if int(m.trackCategory[m.CurTrack()]) != 3 {
		t.Fatalf("chosen track %d cat %d want 3", m.CurTrack(), m.trackCategory[m.CurTrack()])
	}
	// desired with no eligible tracks e.g., cat 4 when numTracks 2 where cats are 1,2 only → stop
	m2 := NewMusicController()
	m2.Open(2) // cats 1,2
	m2.Configure(ModeCategoryShuffle, 4)
	m2.Tick(false)
	if m2.CurTrack() != 0 || m2.Status() != StatusIdle {
		t.Fatalf("no eligible should stop, got cur %d status %d", m2.CurTrack(), m2.Status())
	}
}

func TestMusic_Pause(t *testing.T) {
	m := NewMusicController()
	m.Open(5)
	m.Configure(ModeSequential, 0)
	m.Play(2)
	if !m.IsPlaying() {
		t.Fatal("should be playing")
	}
	m.Pause(true)
	if m.Status() != StatusPaused {
		t.Fatalf("paused status %d", m.Status())
	}
	// tick while paused should not advance
	cur := m.CurTrack()
	m.Tick(false)
	if m.CurTrack() != cur {
		t.Fatalf("paused tick advanced")
	}
	m.Pause(false)
	if m.Status() != StatusPlaying {
		t.Fatal("resume")
	}
}

func TestMusic_FailureFallback(t *testing.T) {
	m := NewMusicController()
	m.Open(0) // no CD
	m.Configure(ModeSequential, 0)
	m.Tick(false)
	if m.CurTrack() != 0 {
		t.Fatalf("no tracks should stay 0")
	}
	m.SetEnabled(false)
	if !m.Play(5) {
		t.Fatal("disabled play should return true (no-op)")
	}
	if m.CurTrack() != 0 {
		t.Fatalf("disabled should not change cur")
	}
}

func TestMusic_SingleRepeat(t *testing.T) {
	m := NewMusicController()
	m.Open(5)
	m.Configure(ModeSingle, 0)
	m.Play(3)
	// while playing, tick with isPlaying true should not change
	m.Tick(true)
	if m.CurTrack() != 3 {
		t.Fatalf("single while playing should stay 3")
	}
	m.NotifyTrackEnd()
	m.Tick(false) // not playing, single should re-play requested (nextTrack 3)
	if m.CurTrack() != 3 {
		t.Fatalf("single repeat %d want 3", m.CurTrack())
	}
}

func TestMusic_SingleZeroStopsAndResumeReappliesVolume(t *testing.T) {
	m := NewMusicController()
	m.Open(5)
	m.Configure(ModeSingle, 0)
	m.Play(3)
	m.SetPosition(1234)
	m.SetVolume(17)
	before := m.VolumeApplications()
	m.Pause(true)
	m.Pause(false)
	if m.Status() != StatusPlaying || m.Position() != 1234 {
		t.Fatalf("resume status=%d position=%d", m.Status(), m.Position())
	}
	if m.VolumeApplications() != before+1 {
		t.Fatalf("resume should reapply volume: %d -> %d", before, m.VolumeApplications())
	}
	m.Stop()
	m.Tick(false)
	if m.CurTrack() != 0 || m.Status() != StatusIdle {
		t.Fatalf("single requested zero must stop, cur=%d status=%d", m.CurTrack(), m.Status())
	}
}
