package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
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
	// Retail's scroll and its deadline are statics that nothing resets at
	// screen entry, so the deadline is always already in the past on a
	// briefing's first draw and that draw takes one step [08 R-CAMP-01 §2].
	if b.scroll != 1 {
		t.Fatalf("scroll after the first draw = %d, want 1", b.scroll)
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

// TestBriefingTextRegionClickPagesText is the regression for the play-test
// report that the briefing description "cannot be scrolled" although it says
// "more": stock MSNBRIEF.GUI authors both `TextRegion` and `MOREBAR` as inert
// kind-5 labels (attribute 0x10, no quickkey), so Panel.PressTest's generic
// label/button capture never selects either one [07 R-WGT-01 §7] — a click
// on the caption, or on the text region itself, used to be silently
// swallowed. The single-player transition table lists a click on either
// gadget as one row with one effect, paging the text
// [07 R-FE-01 §2 "TextRegion / MOREBAR"][07 R-HUD-03 §10]. Retail assets are
// opt-in (see internal/testsupport.RetailRoot); this test skips without
// $NANOLATHE_RETAIL_ASSETS.
func TestBriefingTextRegionClickPagesText(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	shell.missionSide = 0
	shell.openMenu(modeMenuMission)
	found := false
	for i := range shell.campaignOptions {
		if shell.campaignOptions[i].Path == "camps/arm campaign.tdf" {
			shell.campaignIdx, found = i, true
		}
	}
	if !found {
		t.Skip("the Arm campaign is not in this install")
	}
	shell.missionIdx = 0
	shell.openCampaignBriefing()
	if shell.briefing == nil || shell.briefingPanel == nil || shell.briefingPanel.Window == nil {
		t.Fatal("the briefing did not open")
	}

	// Force a multi-page text regardless of the authored brief's own length,
	// so a page turn is unambiguous evidence of the click reaching the pager.
	shell.briefing.SetTextRegion(strings.Repeat("line\n", 20), 200, 20, 8, func(s string) int { return len(s) * 6 })
	if shell.briefing.Page() != 0 || shell.briefing.pageCount < 2 {
		t.Fatalf("test setup: page=%d pageCount=%d, want page 0 of at least 2 pages", shell.briefing.Page(), shell.briefing.pageCount)
	}

	idx := -1
	for i, gad := range shell.briefingPanel.Window.Gadgets {
		if gui.Name16Equal(gad.Name, "TextRegion") {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("MSNBRIEF.GUI has no TextRegion gadget")
	}
	rect := shell.briefingPanel.Window.PlacedRect(idx)

	mouse := cl.Input().Mouse
	mouse.SetPosition(float32(rect.X+rect.W/2), float32(rect.Y+rect.H/2))
	mouse.SetButton(input.MouseButtonLeft, true)
	shell.briefingInput(cl)

	if shell.briefing.Page() != 1 {
		t.Fatalf("a click on TextRegion did not page the text: page=%d, want 1", shell.briefing.Page())
	}
}

// TestBriefingRotationCadenceIsWallClockNotSampleRate locks the planet
// rotator to the retail rate of 40/duration frames a second regardless of how
// often the presentation host happens to sample Update — the play-test
// defect this guards against. Nanolathe's own host is fixed at 30 Hz
// [internal/platform/ebitenapp], slower than the rotator's 40 Hz gate, so a
// single check per call structurally cannot reach 40 Hz: it would forever cap
// the rotation at the host's own rate. Driving the same span of simulated
// wall-clock time at three different sampling rates must land on the
// identical frame and gate-pass count [08 R-CAMP-01 §2][03 §4.4].
func TestBriefingRotationCadenceIsWallClockNotSampleRate(t *testing.T) {
	newEntry := func() *formats.GAFEntry {
		e := &formats.GAFEntry{Name: "rotate", FrameCount: 36, Unknown1: 1, Frames: make([]formats.GAFFrameRef, 36)}
		for i := range e.Frames {
			e.Frames[i].Value = 3 // authored duration in whole ticks, stock rotation entries [08 R-CAMP-01 §2]
		}
		return e
	}

	drive := func(sampleMS int64, totalMS int64) (frame, steps int) {
		b := NewCampaignBriefingController(briefingMission(t, "Lava"), 0, &countingRand{}, nil)
		b.SetRotationSequence(newEntry())
		now := int64(0)
		b.Update(now, briefingPresentationTick(now)) // the bootstrap sample; establishes the phase origin
		for now < totalMS {
			now += sampleMS
			if now > totalMS {
				// However coarse the sampling, the last sample always lands
				// exactly on totalMS: the invariant under test is that the
				// pass count depends on elapsed wall time, not on how the
				// intervening calls happened to be chunked.
				now = totalMS
			}
			b.Update(now, briefingPresentationTick(now))
		}
		return b.RotationFrame(), b.RotationSteps()
	}

	// One bootstrap step plus every 25 ms boundary crossed by 2700 ms of wall
	// time: 1 + 2700/25 = 109 gate passes. At 3 ticks/frame that is 36 full
	// frame advances (108 passes) plus one tick into the 37th, i.e. exactly
	// one full 36-frame loop (2.7 s per rotation, stock content
	// [08 R-CAMP-01 §2]) back to frame 0.
	const totalMS = 2700
	const wantSteps = 109
	frame30, steps30 := drive(33, totalMS) // ~30 Hz, Nanolathe's fixed presentation TPS
	frame60, steps60 := drive(16, totalMS) // ~60 Hz, a faster host
	frame11, steps11 := drive(90, totalMS) // ~11 Hz, a slower/lagging host

	if steps30 != wantSteps || steps60 != wantSteps || steps11 != wantSteps {
		t.Fatalf("gate passes over %d ms of wall time depend on sampling rate: 30Hz=%d 60Hz=%d 11Hz=%d, want %d every time", totalMS, steps30, steps60, steps11, wantSteps)
	}
	if frame30 != 0 || frame60 != 0 || frame11 != 0 {
		t.Fatalf("rotation frame after one full loop depends on sampling rate: 30Hz=%d 60Hz=%d 11Hz=%d, want 0 every time", frame30, frame60, frame11)
	}
}

// countingRand is a CRT stand-in for controllers whose draws are not the
// subject of the test.
type countingRand struct{ n int32 }

func (c *countingRand) Rand() int32 { c.n++; return c.n }

// TestBriefingPanoramaTilesFromFrameZeroAtTheScrollRate is the regression for
// the play-test report that the animation above the briefing description runs
// "way too fast". The strip used to be tiled starting at the gadget's frame
// word, `(now / 3) mod frameCount`, which shifted the whole panorama by a full
// frame width — 640 px in every stock `<x>brief.gaf` pan sequence — ten times
// a second. Retail writes that frame word and then uses it only to null-test a
// frame pointer; the tiling starts at index 0 and `scroll` alone moves the
// strip, one pixel every three presentation ticks [08 R-CAMP-01 §2].
func TestBriefingPanoramaTilesFromFrameZeroAtTheScrollRate(t *testing.T) {
	// Stock geometry: every anims/<x>brief.gaf pan sequence is four frames of
	// 640/640/640/520 (2440 px total) and MSNBRIEF.GUI places PANORAMA at
	// (45,130) 550x123 [08 R-CAMP-01 §2].
	widths := []int{640, 640, 640, 520}
	const total = 2440
	const rectX, rectW = 45, 550
	widthAt := func(i int) int { return widths[i] }

	b := NewCampaignBriefingController(briefingMission(t, "Lava"), 0, &countingRand{}, nil)
	b.SetPanoramaFrameCount(len(widths))

	origin := func() (index, x int) {
		index, x = -1, 0
		first := true
		panoramaStrip(len(widths), widthAt, b.scroll, rectX, rectW, func(i, px int) {
			if first {
				index, x, first = i, px, false
			}
		})
		return index, x
	}

	const ticks = 90 // three seconds of the fixed 30 Hz presentation host
	words := map[int]bool{}
	_, prevX := 0, 0
	for tick := int64(0); tick < ticks; tick++ {
		b.Update(tick*1000/30, tick)
		words[b.PanoramaFrame()] = true
		index, x := origin()
		if index != 0 {
			t.Fatalf("tick %d: the strip is tiled from frame %d, want frame 0 — the frame word is not the tiling origin", tick, index)
		}
		if tick > 0 {
			if d := prevX - x; d != 0 && d != 1 {
				t.Fatalf("tick %d: the strip origin jumped %d px in one tick, want 0 or 1", tick, d)
			}
		}
		prevX = x
	}
	if len(words) < 2 {
		t.Fatalf("the gadget frame word never changed over %d ticks (%v); the test cannot distinguish the two tiling origins", ticks, words)
	}
	// One bootstrap step (retail's carried-over deadline is always in the
	// past on a screen's first draw) plus one step every three ticks
	// thereafter: 1 + 89/3 = 30 px of motion across the three seconds, i.e.
	// the traced ten pixels a second [08 R-CAMP-01 §2].
	if b.scroll != 30 {
		t.Fatalf("scroll after %d ticks = %d px, want 30 (1 px per 3 presentation ticks)", ticks, b.scroll)
	}
	if got := rectX - b.scroll; got != prevX {
		t.Fatalf("strip origin = %d, want gadget.x - scroll = %d", prevX, got)
	}

	// The blit count is retail's covering count, not a fill test: for every
	// value of scroll the strip must still reach the gadget's right edge
	// [08 R-CAMP-01 §2].
	for scroll := 0; scroll < total; scroll++ {
		right := 0
		panoramaStrip(len(widths), widthAt, scroll, rectX, rectW, func(i, px int) {
			right = px + widths[i]
		})
		if right < rectX+rectW {
			t.Fatalf("scroll %d: the strip ends at %d, short of the gadget's right edge %d", scroll, right, rectX+rectW)
		}
	}
}
