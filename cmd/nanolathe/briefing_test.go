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
	if b.RotationFrame() != 1 {
		t.Fatalf("first eligible planet draw rotation = %d, want 1", b.RotationFrame())
	}
	if b.scroll != 0 {
		t.Fatalf("scroll advanced at deadline setup = %d", b.scroll)
	}
	// A later draw inside the 25 ms rotation gate still advances panorama
	// selection from (tick/3)%count.
	b.lastWallMS, b.lastTick = 100, 1
	b.Update(101, 9)
	if b.PanoramaFrame() != 3 {
		t.Fatalf("panorama frame = %d, want 3", b.PanoramaFrame())
	}
	if b.RotationFrame() != 1 {
		t.Fatalf("rotation changed inside wall gate = %d, want 1", b.RotationFrame())
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
