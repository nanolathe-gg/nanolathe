package main

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestRetailContinuationPreflightFailureIsAtomic(t *testing.T) {
	progress := session.BankProgress{BetweenMissions: 1, WL: [10]byte{'L'}, Thumbs: [25]byte{'W'}}
	shell := &gameShell{
		cs: &contentSet{fs: vfs.New()}, campaignIdx: 4, missionIdx: 3,
		missionDifficultyValue: 2, missionSide: 1, campaignProgress: progress,
		campaignProgressSet: true,
	}
	if err := shell.applyRetailContinuation(&session.RetailCampaignContinuation{CampaignPath: "camps/nope.tdf", MissionIndex: -1}); err == nil {
		t.Fatal("invalid continuation unexpectedly succeeded")
	}
	if shell.campaignIdx != 4 || shell.missionIdx != 3 || shell.missionDifficultyValue != 2 || shell.missionSide != 1 {
		t.Fatal("failed continuation changed campaign selection")
	}
	if !reflect.DeepEqual(shell.campaignProgress, progress) || !shell.campaignProgressSet || shell.battle != nil {
		t.Fatal("failed continuation changed live progress/battle state")
	}
}

func TestRetailContinuationCopiesThumbsWithoutWLConversion(t *testing.T) {
	progress := session.BankProgress{WL: [10]byte{'L'}, Thumbs: [25]byte{'U'}}
	var thumbs [25]byte
	for i := range thumbs {
		thumbs[i] = 'W'
	}
	applyRetailContinuationProgress(&progress, thumbs)
	if progress.Thumbs != thumbs || progress.BetweenMissions != 1 {
		t.Fatalf("continuation progress = %#v", progress)
	}
	if progress.WL != [10]byte{} {
		t.Fatalf("continuation retained stale WL state: %q", progress.WL)
	}
}

func briefingMission(t *testing.T, planet string) *mission.Mission {
	t.Helper()
	doc, err := formats.ParseTDF([]byte("[GlobalHeader]{Planet=" + planet + "; minwindspeed=10; maxwindspeed=20; narration=voice;}"))
	if err != nil {
		t.Fatal(err)
	}
	return &mission.Mission{OTA: &formats.OTA{Global: doc.Root.Section("GlobalHeader")}, WindBounds: mission.WindBounds{Min: 10, Max: 20}}
}

func TestBriefingPlanetLookupAndFallback(t *testing.T) {
	known, idx := ResolveBriefingPlanet("Archipelago", 0)
	if idx != 1 || known.Brief != "Archibrief" || known.Panorama != "ArchiPan" || known.Rotate != "ArchiRotate" {
		t.Fatalf("known authored row = %#v, %d", known, idx)
	}
	unknown, idx := ResolveBriefingPlanet("Urban", 0)
	if idx != 0 || unknown.Name != "Green planet" {
		t.Fatalf("unknown planet must use Green planet row, got %#v, %d", unknown, idx)
	}
	spaced, idx := ResolveBriefingPlanet(" Archipelago ", 0)
	if idx != 0 || spaced.Name != "Green planet" {
		t.Fatalf("authored whitespace must not match a planet row: %#v, %d", spaced, idx)
	}
	lunar, idx := ResolveBriefingPlanet("Lunar", 1)
	if idx != 8 || lunar.Name != "Lunar2" {
		t.Fatalf("Core Lunar rewrite = %#v, %d", lunar, idx)
	}
}

func TestBriefingPresentationTick(t *testing.T) {
	if got := briefingPresentationTick(999); got != 29 {
		t.Fatalf("presentation tick at 999 ms = %d, want 29", got)
	}
	if got := briefingPresentationTick(1000); got != 30 {
		t.Fatalf("presentation tick at 1000 ms = %d, want 30", got)
	}
}

func TestBriefingWindDrawOrderAndStartRequest(t *testing.T) {
	stream := rng.NewCRT(7)
	called := false
	b := NewCampaignBriefingController(briefingMission(t, "Lava"), 0, &stream, func() (freshBattleRequest, error) {
		called = true
		return freshBattleRequest{}, nil
	})
	if stream.Draws() != 2 {
		t.Fatalf("briefing open CRT draws = %d, want 2", stream.Draws())
	}
	before := stream.Draws()
	b.SetPanoramaFrameCount(10)
	b.countdown = 0
	b.Update(25, 1)
	if stream.Draws()-before != 2 {
		t.Fatalf("wind rollover CRT draws = %d, want 2", stream.Draws()-before)
	}
	if b.RotationSteps() != 1 {
		t.Fatalf("first eligible rotator pass = %d, want 1", b.RotationSteps())
	}
	if b.scroll != 0 {
		t.Fatalf("scroll advanced at deadline setup = %d", b.scroll)
	}
	// A later draw inside the 25 ms rotation gate still advances panorama
	// selection from (tick/3)%count.
	b.Update(26, 9)
	if b.PanoramaFrame() != 3 {
		t.Fatalf("panorama frame = %d, want 3", b.PanoramaFrame())
	}
	if b.RotationSteps() != 1 {
		t.Fatalf("rotator stepped inside the 25 ms gate = %d, want 1", b.RotationSteps())
	}
	b.Update(50, 10)
	if b.RotationSteps() != 2 {
		t.Fatalf("rotator passes after the gate expired = %d, want 2", b.RotationSteps())
	}
	opening := b.OpeningAudio()
	if len(opening) != 1 || opening[0].Kind != BriefingAudioStart || opening[0].Path != "camps/briefs/voice.wav" || opening[0].Delay != 60 || opening[0].Volume != 0 {
		t.Fatalf("opening narration effect = %#v", opening)
	}
	stop, err := b.Dispatch(BriefingActionShutup)
	if err != nil || len(stop.Audio) != 1 || stop.Audio[0].Kind != BriefingAudioStop || stop.Audio[0].Delay != 0 {
		t.Fatalf("SHUTUP stop effect = %#v, err=%v", stop.Audio, err)
	}
	start, err := b.Dispatch(BriefingActionShutup)
	if err != nil || len(start.Audio) != 1 || start.Audio[0].Kind != BriefingAudioStart || start.Audio[0].Delay != 60 || start.Audio[0].Volume != 0 {
		t.Fatalf("SHUTUP restart effect = %#v, err=%v", start.Audio, err)
	}
	if _, err := b.Dispatch(BriefingActionStart); err != nil || !called || b.State() != BriefingClosed {
		t.Fatalf("Start event: called=%v state=%v err=%v", called, b.State(), err)
	}
}

func TestBriefingBattleSeedSourceCarriesCRTState(t *testing.T) {
	stream := rng.NewCRT(19)
	_ = stream.Rand()
	source := briefingBattleSeedSource{opts: Options{Seed: 33}, crt: &stream}
	want := stream.State
	seeds := source.NextBattleSeeds()
	if seeds.CRT != want {
		t.Fatalf("battle CRT seed = %d, want post-briefing state %d", seeds.CRT, want)
	}
}

func TestBriefingArtKeepsPreviousOnOptionalMediaMiss(t *testing.T) {
	previous := &formats.GAF{Version: 1}
	if got := loadBriefingArt(nil, BriefingPlanet{Brief: "LavaBrief"}, previous); got != previous {
		t.Fatalf("optional art miss replaced prior GAF: got %p want %p", got, previous)
	}
}

// TestRetailWordWrapContract locks the front-end wrapper's three consequences:
// the width test is `>=` and not `>`, breaks happen only at a space or hyphen
// (so a word longer than the width is never split), and the separator is
// consumed and replaced by `\r\n` [07 R-FE-02 §6].
func TestRetailWordWrapContract(t *testing.T) {
	// One unit per byte makes the arithmetic readable.
	measure := func(s string) int { return len(s) }

	// The measure runs when the *successor* is a break byte, and the break goes
	// back to the separator before the word that overflowed. "aa bb" measures 5,
	// so a width of 5 breaks and a width of 6 does not: the test is `>=`.
	if got := retailWordWrap("aa bb cc", 5, measure); got != "aa\r\nbb cc" {
		t.Fatalf("inclusive width test = %q", got)
	}
	if got := retailWordWrap("aa bb cc", 6, measure); got != "aa bb cc" {
		t.Fatalf("line narrower than the width = %q", got)
	}
	// A hyphen is a break opportunity and, like the space, is consumed.
	if got := retailWordWrap("aa-bb cc", 5, measure); got != "aa\r\nbb cc" {
		t.Fatalf("hyphen break = %q", got)
	}
	// A word longer than the width is never split: with no earlier separator in
	// the line the overflow is emitted whole and the next break lands after it.
	if got := retailWordWrap("aaaaaa bb cc", 4, measure); got != "aaaaaa\r\nbb cc" {
		t.Fatalf("long word split = %q", got)
	}
	// An authored newline restarts the line without a wrap.
	if got := retailWordWrap("ab\ncd", 8, measure); got != "ab\ncd" {
		t.Fatalf("authored newline = %q", got)
	}
	// 0xFF ends the text.
	if got := retailWordWrap("ab\xffcd", 8, measure); got != "ab" {
		t.Fatalf("0xFF terminator = %q", got)
	}
}

// TestBriefingBlinkRunPreSplitAndLay locks the run pre-pass and the pager's
// marker handling: a run crossing a line end is closed and reopened, the
// markers never reach the label, and the run's pen is the width of the label
// text laid before it [07 R-FE-02 §7][07 R-HUD-03 §10].
func TestBriefingBlinkRunPreSplitAndLay(t *testing.T) {
	if got := briefingSplitBlinkRuns("ab &Ycd\r\nef& gh"); got != "ab &Ycd&\r\n&Yef& gh" {
		t.Fatalf("pre-split = %q", got)
	}
	open := true
	line := briefingLayLine("ab &Ycd& ef", func(s string) int { return len(s) }, &open, briefingBlinkWordCap)
	if line.Text != "ab cd ef" {
		t.Fatalf("laid label = %q, markers must not reach it", line.Text)
	}
	if len(line.Runs) != 1 || line.Runs[0].Text != "cd" || line.Runs[0].X != 3 || line.Runs[0].Entry != 2 {
		t.Fatalf("laid runs = %+v", line.Runs)
	}
	if !open {
		t.Fatal("the closing marker must leave the pager's marker state open")
	}
}

// TestBriefingPagerLinesAndCaption locks the lines-per-page divide, the page
// wrap and the three MOREBAR captions [07 R-HUD-03 §10].
func TestBriefingPagerLinesAndCaption(t *testing.T) {
	b := NewCampaignBriefingController(briefingMission(t, "Lava"), 0, &countingRand{}, nil)
	// Six single-character lines, a region tall enough for two of them.
	b.SetTextRegion("a\nb\nc\nd\ne\nf", 100, 2*12, 10, func(s string) int { return len(s) })
	if b.pageLines != 2 || b.pageCount != 3 {
		t.Fatalf("pageLines=%d pageCount=%d, want 2 and 3", b.pageLines, b.pageCount)
	}
	if got := b.MoreCaption(); got != "MORE..." {
		t.Fatalf("page 0 caption = %q", got)
	}
	if len(b.Lines()) != 2 || b.Lines()[0].Text != "a" || b.Lines()[1].Text != "b" {
		t.Fatalf("page 0 lines = %+v", b.Lines())
	}
	b.Dispatch(BriefingActionMore)
	b.Dispatch(BriefingActionMore)
	if b.Page() != 2 || b.Lines()[0].Text != "e" {
		t.Fatalf("page 2 = %d %+v", b.Page(), b.Lines())
	}
	if got := b.MoreCaption(); got != "BACK TO START" {
		t.Fatalf("last page caption = %q", got)
	}
	b.Dispatch(BriefingActionMore)
	if b.Page() != 0 {
		t.Fatalf("MORE past the last page = %d, want a wrap to 0", b.Page())
	}
}

// countingRand is a CRT stand-in for controllers whose draws are not the
// subject of the test.
type countingRand struct{ n int32 }

func (c *countingRand) Rand() int32 { c.n++; return c.n }
