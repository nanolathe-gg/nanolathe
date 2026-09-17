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
	// The stop/reset re-arms *next* to track 1 with a disc present
	// [03 R-AUD-01 §4 step 2], and the sequential arm is "next + 1"
	// [03 R-AUD-01 §4 step 5, mode 1], so the resumed disc starts at 2.
	m.Stop()
	m.SetNumTracks(3)
	m.Configure(ModeSequential, 0)
	if m.NextTrack() != 1 {
		t.Fatalf("next after stop %d want 1", m.NextTrack())
	}
	m.Tick(false) // not playing → seq plays next+1 = 2
	if m.CurTrack() != 2 {
		t.Fatalf("seq first %d want 2", m.CurTrack())
	}
	m.NotifyTrackEnd() // track ends
	m.Tick(false)      // should advance to 3
	if m.CurTrack() != 3 {
		t.Fatalf("seq second %d want 3", m.CurTrack())
	}
	m.NotifyTrackEnd()
	m.Tick(false) // wrap to 1
	if m.CurTrack() != 1 {
		t.Fatalf("seq wrap %d want 1", m.CurTrack())
	}
	m.NotifyTrackEnd()
	m.Tick(false) // 2
	if m.CurTrack() != 2 {
		t.Fatalf("seq fourth %d want 2", m.CurTrack())
	}
}

// TestMusic_StopResetsNextToOne locks the stop/reset contract: status idle,
// fade step zero, timers cancelled, and *next* re-armed to track 1 — or 0 only
// when the disc carries no audio tracks [03 R-AUD-01 §4 step 2]. The MUSIC
// screen's `CDSTOP` is the same primitive and likewise re-selects track 1
// [03 R-AUD-01 §4 "the MUSIC screen"].
func TestMusic_StopResetsNextToOne(t *testing.T) {
	m := NewMusicController()
	m.Open(16)
	m.Play(7)
	if m.NextTrack() != 7 {
		t.Fatalf("next while playing %d want 7", m.NextTrack())
	}
	m.Stop()
	if got := m.NextTrack(); got != 1 {
		t.Fatalf("next after stop %d want 1", got)
	}
	if m.Status() != StatusIdle {
		t.Fatalf("status after stop %v want idle", m.Status())
	}

	// No audio tracks → the reset leaves next at 0.
	empty := NewMusicController()
	empty.SetNumTracks(0)
	empty.Stop()
	if got := empty.NextTrack(); got != 0 {
		t.Fatalf("next after stop with no tracks %d want 0", got)
	}

	// The silence category runs the same stop/reset through the tick
	// [03 R-AUD-01 §4 step 2].
	sil := NewMusicController()
	sil.Open(16)
	sil.Play(5)
	sil.Configure(ModeSequential, 4)
	sil.Tick(true)
	if got := sil.NextTrack(); got != 1 {
		t.Fatalf("next after silence tick %d want 1", got)
	}
	if sil.Status() != StatusIdle {
		t.Fatalf("status after silence tick %v want idle", sil.Status())
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

// TestMusic_CategoryShuffleBuildingSelectable locks [03 R-AUD-01 §4]:
// categories are 0..4 with 0 = Building, and the retail default per-disc
// list is seven Battle bytes followed by Building zeros — so Building is
// the common resting category, not an invalid or unreachable one. Configure
// must accept desiredCat 0, and the shuffle scan must be able to land on a
// Building-tagged track.
func TestMusic_CategoryShuffleBuildingSelectable(t *testing.T) {
	m := NewMusicController()
	m.Open(4)
	// Force the retail-shaped default list: Battle tracks then Building
	// zeros, rather than the (i%4)+1 bring-up cycle which never emits 0.
	m.trackCategory[1] = 1 // Battle
	m.trackCategory[2] = 1 // Battle
	m.trackCategory[3] = 0 // Building
	m.trackCategory[4] = 0 // Building

	m.Configure(ModeCategoryShuffle, 0) // desired Building
	if m.desiredCat != 0 {
		t.Fatalf("Configure must accept desiredCat=0 (Building); got %d", m.desiredCat)
	}
	m.Seed(77)
	m.Tick(false)
	if m.CurTrack() == 0 {
		t.Fatalf("Building (0) must be selectable by the category shuffle, not treated as invalid")
	}
	if int(m.trackCategory[m.CurTrack()]) != 0 {
		t.Fatalf("chosen track %d has category %d, want 0 (Building)", m.CurTrack(), m.trackCategory[m.CurTrack()])
	}
}

// TestMusic_ConfigureCategoryGateIsZeroToFour locks the corrected gate: all
// five retail categories (0 Building .. 4 Unused) are accepted, and only
// values outside that range are rejected (left unchanged) [03 R-AUD-01 §4].
func TestMusic_ConfigureCategoryGateIsZeroToFour(t *testing.T) {
	m := NewMusicController()
	for cat := 0; cat <= 4; cat++ {
		m.Configure(ModeCategoryShuffle, cat)
		if m.desiredCat != cat {
			t.Fatalf("Configure should accept desiredCat=%d, got %d", cat, m.desiredCat)
		}
	}
	m.Configure(ModeCategoryShuffle, 3)
	m.Configure(ModeCategoryShuffle, -1) // out of range → left unchanged
	if m.desiredCat != 3 {
		t.Fatalf("Configure should reject desiredCat=-1 and keep prior value 3, got %d", m.desiredCat)
	}
	m.Configure(ModeCategoryShuffle, 5) // out of range → left unchanged
	if m.desiredCat != 3 {
		t.Fatalf("Configure should reject desiredCat=5 and keep prior value 3, got %d", m.desiredCat)
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

// Repeat's arm is "not `playing` **or** `next ≠ requested` → `requested = 1`
// when it was 0; `PlayTrack(requested)`" [03 R-AUD-01 §4 step 5, mode 3]. The
// requested track is a field of the CD object in its own right, written by the
// MUSIC screen's `Repeat` stage; `PlayTrack` writes *next*. This test used to
// read the two as one word, which is what let a Repeat battle start silent.
func TestMusic_SingleRepeat(t *testing.T) {
	m := NewMusicController()
	m.Open(5)
	m.Configure(ModeSingle, 0)
	m.SetRequestedTrack(3) // the `TRACKMODE` `Repeat` arm's copy
	m.Play(3)
	// While the requested track plays, both halves of the condition are false
	// and the tick leaves the selection alone.
	m.Tick(true)
	if m.CurTrack() != 3 {
		t.Fatalf("single while playing should stay 3")
	}
	m.NotifyTrackEnd()
	m.Tick(false) // not playing → replay the requested track
	if m.CurTrack() != 3 {
		t.Fatalf("single repeat %d want 3", m.CurTrack())
	}
	// A next track that is not the requested one is pulled back even while the
	// drive reports playing — the `next ≠ requested` half.
	m.Play(5)
	m.Tick(true)
	if m.CurTrack() != 3 || m.NextTrack() != 3 {
		t.Fatalf("repeat did not pull back to the requested track: cur=%d next=%d", m.CurTrack(), m.NextTrack())
	}
}

// A Repeat tick with nothing requested defaults the requested track to 1 and
// plays it [03 R-AUD-01 §4 step 5, mode 3]. It does not stop: that reading is
// what left `cdmode=3` battles with no music until the MUSIC screen was used.
func TestMusic_SingleZeroPlaysTrackOneAndResumeReappliesVolume(t *testing.T) {
	m := NewMusicController()
	m.Open(5)
	m.Configure(ModeSingle, 0)
	m.SetRequestedTrack(3)
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
	m.SetRequestedTrack(0)
	m.Tick(false)
	if m.RequestedTrack() != 1 || m.CurTrack() != 1 || m.Status() != StatusPlaying {
		t.Fatalf("repeat with nothing requested: requested=%d cur=%d status=%d",
			m.RequestedTrack(), m.CurTrack(), m.Status())
	}
}

// TestMusic_UnusedCategoryStopsBeforeThePausedTest locks [03 R-AUD-01 §8]:
// desired category 4 (Unused = silence) stops and resets whatever the play
// mode, and the test precedes the paused test, so a paused disc is stopped
// too.
func TestMusic_UnusedCategoryStopsBeforeThePausedTest(t *testing.T) {
	m := NewMusicController()
	m.Open(4)
	m.Configure(ModeSequential, 1)
	m.Play(2)
	m.Pause(true)
	if m.Status() != StatusPaused {
		t.Fatalf("setup: status %d, want paused", m.Status())
	}
	m.Configure(ModeSequential, 4)
	m.Tick(true)
	if m.Status() != StatusIdle || m.CurTrack() != 0 {
		t.Fatalf("Unused must stop a paused disc: status %d cur %d", m.Status(), m.CurTrack())
	}
	// And in Play All while not paused, before the mode dispatch would advance.
	m.Configure(ModeSequential, 4)
	m.Tick(false)
	if m.Status() != StatusIdle || m.CurTrack() != 0 {
		t.Fatalf("Unused must override Play All: status %d cur %d", m.Status(), m.CurTrack())
	}
}

// TestMusic_VictoryDefeatTakeTheCategoryBranchInAnyMode locks [03 R-AUD-01
// §8]: desired 2 or 3 routes to the category branch regardless of play mode,
// idle included, and the branch leaves a playing track alone only when its
// category already matches.
func TestMusic_VictoryDefeatTakeTheCategoryBranchInAnyMode(t *testing.T) {
	for _, mode := range []PlayMode{ModeIdle, ModeSequential, ModeRandom, ModeSingle} {
		m := NewMusicController()
		m.Open(8) // categories 1,2,3,4,1,2,3,4
		m.Seed(5)
		m.Configure(mode, 3) // Defeat
		m.Tick(false)
		if m.CurTrack() == 0 || int(m.trackCategory[m.CurTrack()]) != 3 {
			t.Fatalf("mode %d: desired 3 must pick a category-3 track, got cur %d", mode, m.CurTrack())
		}
		// A playing track of the wrong category is switched even while playing.
		m.Play(1) // category 1
		m.Tick(true)
		if int(m.trackCategory[m.CurTrack()]) != 3 {
			t.Fatalf("mode %d: a playing category-1 track must be replaced, got cur %d", mode, m.CurTrack())
		}
		// A playing track of the right category is left alone.
		cur := m.CurTrack()
		m.Tick(true)
		if m.CurTrack() != cur {
			t.Fatalf("mode %d: matching track must keep playing, got %d want %d", mode, m.CurTrack(), cur)
		}
	}
}

// TestMusic_CategoryScanPlaysTheMaxOneUthMatch locks the corrected pick index
// of [03 R-AUD-01 §8]: with every track matching, the scan from `next` plays
// the max(1,u)-th following track, u being the draw's low four bits.
func TestMusic_CategoryScanPlaysTheMaxOneUthMatch(t *testing.T) {
	for seed := uint32(1); seed < 40; seed++ {
		m := NewMusicController()
		m.Open(20)
		for i := range m.trackCategory {
			m.trackCategory[i] = 2
		}
		m.Configure(ModeCategoryShuffle, 2)
		m.Seed(seed)
		draw := (&presentationCRT{state: seed}).Rand()
		u := int(draw & 0xF)
		want := u
		if want < 1 {
			want = 1
		}
		m.Tick(false)
		if m.CurTrack() != want {
			t.Fatalf("seed %d (u=%d): picked track %d, want %d", seed, u, m.CurTrack(), want)
		}
	}
}

func TestMusicFadeDelayAndGaugeBoundary(t *testing.T) {
	m := NewMusicController()
	if m.baseVolume != -1 {
		t.Fatal("missing device must initialize raw volume to -1")
	}
	m.Open(4)
	m.Configure(ModeCategoryShuffle, 1)
	m.SetVolume(64)
	if m.baseVolume != 65535 {
		t.Fatal("gauge upper clamp differs")
	}
	m.SetVolume(-1)
	if m.baseVolume != 0 {
		t.Fatal("negative gauge was not clamped after signed shift")
	}
	m.SetVolume(18)
	var now uint32
	m.SetPresentationClock(func() uint32 { return now })
	m.SetPresentationClock(func() uint32 { t.Fatal("attachment rebound the running clock"); return 999 })
	m.SetDesired(0)
	if m.fadeStep != -1024 {
		t.Fatalf("step=%d", m.fadeStep)
	}
	now = 1
	m.ServiceTimers()
	if m.fadeLevel != 18432 {
		t.Fatal("period-two timer fired early")
	}
	m.SetVolume(7)
	if m.Volume() != 18 {
		t.Fatal("gauge overwrote volume during fade")
	}
	now = 40
	m.ServiceTimers()
	if m.fadeLevel != 17408 {
		t.Fatal("late service caught up multiple firings")
	}
	for i := 0; i < 17; i++ {
		now += 2
		m.ServiceTimers()
	}
	if m.fadeStep != 0 || m.fadeTimer != -1 || m.delayTimer != 0 || m.outputVolume != 0 {
		t.Fatalf("completion step=%d fade=%d delay=%d output=%d", m.fadeStep, m.fadeTimer, m.delayTimer, m.outputVolume)
	}
	m.SetVolume(7)
	if m.baseVolume != 7168 {
		t.Fatal("Building delay incorrectly blocks gauge")
	}
	now += 119
	m.ServiceTimers()
	if m.delayTimer < 0 {
		t.Fatal("Building delay fired before 120")
	}
	now++
	m.ServiceTimers()
	if m.delayTimer != -1 {
		t.Fatal("Building delay did not remove itself")
	}
}

func TestMusicRegistrationServicesBeforeFirstFreeReuse(t *testing.T) {
	m := NewMusicController()
	m.Configure(ModeCategoryShuffle, 1)
	var now uint32
	reads := 0
	m.SetPresentationClock(func() uint32 { reads++; return now })
	m.baseVolume = 18
	m.SetDesired(0)
	// Last fade firing removes slot zero, then registering the delay services
	// the live table again before claiming that same first-free slot.
	m.fadeLevel = 1
	reads = 0
	now = 2
	m.ServiceTimers()
	if reads != 4 || m.delayTimer != 0 || m.timers[0].remaining != 120 {
		t.Fatalf("reads=%d delay=%d remaining=%d", reads, m.delayTimer, m.timers[0].remaining)
	}
	// A second timer can coexist. Registration first services the old delay;
	// it must not replace it or impose a fixed fade-before-delay order.
	now = 3
	m.SetDesired(1)
	if m.fadeTimer != 1 || m.delayTimer != 0 || m.timers[0].remaining != 119 {
		t.Fatalf("fade=%d delay=%d remaining=%d", m.fadeTimer, m.delayTimer, m.timers[0].remaining)
	}
}

func TestMusicFadeCancellationAndCompletionPolling(t *testing.T) {
	m := NewMusicController()
	m.Open(4)
	m.Configure(ModeCategoryShuffle, 0)
	m.SetVolume(18)
	m.SetPresentationClock(func() uint32 { return 0 })
	m.SetDesired(1)
	m.SetDesired(2)
	if m.fadeTimer != -1 || m.delayTimer != -1 || m.fadeStep != -1024 || m.outputVolume != 18432 {
		t.Fatal("cancellation reset step or skipped immediate volume restoration")
	}
	m.SetVolume(7)
	if m.Volume() != 18 {
		t.Fatal("cancelled timer incorrectly unlocked nonzero-step gauge")
	}
	m.Stop()
	m.Configure(ModeSequential, 0)
	m.Play(1)
	playing := true
	polls := 0
	m.SetPlaybackPoll(func() bool {
		polls++
		if m.Status() != StatusPlaying {
			t.Fatal("notification changed controller status before the tick poll")
		}
		return playing
	})
	m.NotifySuccessfulCompletion()
	if m.CurTrack() != 1 || polls != 1 {
		t.Fatal("completion advanced or repolled while media still plays")
	}
	playing = false
	m.NotifySuccessfulCompletion()
	if m.CurTrack() != 2 || polls != 3 {
		t.Fatal("successful completion did not freshly poll and advance stopped media")
	}
}

// A battle entered with `cdmode=3` and nothing requested must start playing.
// The tick's Repeat arm supplies track 1 [03 R-AUD-01 §4 step 5, mode 3]; it
// used to read *next*, find it 0, and stop, which left the battle silent until
// the MUSIC screen was opened.
func TestMusic_RepeatBattleEntryStartsWithoutARequestedTrack(t *testing.T) {
	m := NewMusicController()
	m.Open(16)
	m.Configure(ModeSingle, 0)
	m.Tick(false)
	if m.Status() != StatusPlaying || m.CurTrack() != 1 {
		t.Fatalf("repeat entry status=%d cur=%d, want playing on track 1", m.Status(), m.CurTrack())
	}
}
