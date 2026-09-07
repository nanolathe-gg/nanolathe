package headless

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestHeadlessSkirmishStepsAndReports(t *testing.T) {
	request := Request{
		Root:           testsupport.RetailRoot(t),
		Map:            "ashap plateau",
		SimulationSeed: 1,
		CRTSeed:        1,
		TickLimit:      300,
	}
	report, err := Run(request)
	if !errors.Is(err, ErrTickLimit) {
		t.Fatalf("Run error = %v, want tick limit", err)
	}
	if report.Tick != 300 || report.Status != "tick_limit" {
		t.Fatalf("tick/status = %d/%q", report.Tick, report.Status)
	}
	if report.ScenarioKind != ScenarioSkirmish || report.ScenarioIdentity != "ashap plateau" {
		t.Fatalf("scenario = %q/%q", report.ScenarioKind, report.ScenarioIdentity)
	}
	if report.SimulationSeed != 1 || report.CRTSeed != 1 || report.StateHash == "" || report.CatalogHash == "" || report.ManifestHash == "" {
		t.Fatalf("seeds/hashes = %d/%d/%q/%q/%q", report.SimulationSeed, report.CRTSeed, report.StateHash, report.CatalogHash, report.ManifestHash)
	}
	if !report.Players[1].AIManagerBound {
		t.Fatal("computer player's AI manager was not reported as bound")
	}
}

func TestRunSessionAdvancesOrdinaryStepLoop(t *testing.T) {
	request := Request{Map: "synthetic", SimulationSeed: 17, CRTSeed: 19, TickLimit: 7}
	sess := syntheticSession(request)

	report, err := RunSession(request, sess)
	if !errors.Is(err, ErrTickLimit) {
		t.Fatalf("RunSession error = %v, want tick limit", err)
	}
	if report.Tick != 7 || report.Status != "tick_limit" {
		t.Fatalf("tick/status = %d/%q, want 7/tick_limit", report.Tick, report.Status)
	}
	if report.ScenarioKind != ScenarioSkirmish || report.ScenarioIdentity != "synthetic" {
		t.Fatalf("scenario = %q/%q", report.ScenarioKind, report.ScenarioIdentity)
	}
	if report.SimulationSeed != 17 || report.CRTSeed != 19 {
		t.Fatalf("seeds = %d/%d", report.SimulationSeed, report.CRTSeed)
	}
	if report.StateHash == "" {
		t.Fatal("authoritative state hash is empty")
	}
	if current := sess.Snapshot.Current(); current == nil || current.Tick != 7 {
		t.Fatalf("committed frame = %+v, want tick 7", current)
	}
}

func TestRunSessionDrainsPresentationEventsAndReportsExactOverflow(t *testing.T) {
	request := Request{Map: "synthetic", SimulationSeed: 17, CRTSeed: 19, TickLimit: 1}
	sess := syntheticSession(request)
	write := sess.Snapshot.BeginWrite()
	for i := 0; i < 4097; i++ {
		write.Events = append(write.Events, frame.EventView{ID: uint32(i + 1), Tick: 0})
	}
	if err := sess.Snapshot.Publish(0); err != nil {
		t.Fatal(err)
	}

	report, err := RunSession(request, sess)
	if !errors.Is(err, ErrTickLimit) {
		t.Fatalf("RunSession error = %v, want tick limit", err)
	}
	if report.PresentationEventsDropped != 1 {
		t.Fatalf("reported retained-event loss = %d, want 1", report.PresentationEventsDropped)
	}
	if pending := sess.Snapshot.PendingCommittedEvents(); pending != 0 {
		t.Fatalf("headless run retained %d presentation events", pending)
	}
}

func TestEqualRequestProducesEqualReport(t *testing.T) {
	request := Request{Map: "synthetic", SimulationSeed: 29, CRTSeed: 31, TickLimit: 11}
	first, firstErr := RunSession(request, syntheticSession(request))
	second, secondErr := RunSession(request, syntheticSession(request))
	if !errors.Is(firstErr, ErrTickLimit) || !errors.Is(secondErr, ErrTickLimit) {
		t.Fatalf("errors = %v/%v, want tick limit", firstErr, secondErr)
	}
	if first.StateHash != second.StateHash {
		t.Fatalf("equal requests differ: %s/%s", first.StateHash, second.StateHash)
	}
	if first.SimulationState != second.SimulationState || first.CRTState != second.CRTState || first.SimulationDraws != second.SimulationDraws || first.CRTDraws != second.CRTDraws {
		t.Fatalf("equal requests changed RNG states/counts: first=%+v second=%+v", first, second)
	}
	if first.Tick != second.Tick || !reflect.DeepEqual(first.Players, second.Players) {
		t.Fatalf("equal reports differ: first=%+v second=%+v", first, second)
	}
}

func syntheticSession(request Request) *session.Session {
	sess := &session.Session{
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: frame.NewBuffer(),
		Units:    units.NewSliced(10, nil),
		State:    session.StateBattle,
	}
	sess.RegisterAll()
	sess.SeedSessionRNG(request.SimulationSeed, request.CRTSeed)
	return sess
}

// TestSkirmishRequestThreadsDifficultyIntoTheSessionWord locks WU-19-217: a
// displayless request's Difficulty must reach the battle's difficulty word
// (`sess.Skirmish.Difficulty`, read by `sessionDifficultyWord` for both the AI
// profile's plan gate [08 R-AI-01 §12] and the computer player's production
// discount [05 R-ECO-01 §3]), not only the campaign path's
// NewMissionWithProgressSeeds call. Skipped when retail assets are absent.
func TestSkirmishRequestThreadsDifficultyIntoTheSessionWord(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	battle, err := ComposeFreshBattle(FreshBattleRequest{
		Kind: ScenarioDirectOTA, Map: "ashap plateau", Difficulty: 2,
		LocalOwner: -1, SimulationSeed: 7, CRTSeed: 7, FS: fs,
	})
	if err != nil {
		t.Fatalf("ComposeFreshBattle: %v", err)
	}
	if battle.Session.Skirmish.Difficulty != 2 {
		t.Fatalf("session difficulty word = %d, want 2", battle.Session.Skirmish.Difficulty)
	}
}

func TestRequestRequiresExactlyOneScenario(t *testing.T) {
	for _, request := range []Request{{}, {Map: "a", Mission: "b"}} {
		if _, _, err := scenario(request); err == nil {
			t.Fatalf("scenario(%+v) succeeded", request)
		}
	}
}

func TestFreshBattleAdaptersHaveEqualAuthoritativeSetup(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	cfg := session.DirectSkirmishConfig("ashap plateau")
	requests := []struct {
		name string
		kind ScenarioKind
	}{
		{name: "direct", kind: ScenarioDirectOTA},
		{name: "menu", kind: ScenarioSkirmish},
		{name: "displayless", kind: ScenarioDirectOTA},
	}
	var baseline FreshBattle
	for i, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComposeFreshBattle(FreshBattleRequest{
				Kind: tc.kind, Map: cfg.MapName, Skirmish: cfg,
				LocalOwner: 0, SimulationSeed: 41, CRTSeed: 43,
				FS: fs, PresentationWidth: 640, PresentationHeight: 480,
			})
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				baseline = got
				return
			}
			if got.Identity != baseline.Identity || got.SimulationSeed != baseline.SimulationSeed || got.CRTSeed != baseline.CRTSeed || got.LocalOwner != baseline.LocalOwner || got.Watching != baseline.Watching {
				t.Fatalf("setup identity differs: got=%+v baseline=%+v", got, baseline)
			}
			if got.TerrainWidth != baseline.TerrainWidth || got.TerrainHeight != baseline.TerrainHeight || got.InitialHash != baseline.InitialHash {
				t.Fatalf("terrain/hash differs: got=(%d,%d,%s) baseline=(%d,%d,%s)", got.TerrainWidth, got.TerrainHeight, got.InitialHash, baseline.TerrainWidth, baseline.TerrainHeight, baseline.InitialHash)
			}
			if !reflect.DeepEqual(got.Session.Skirmish, baseline.Session.Skirmish) || !reflect.DeepEqual(got.Session.Econ.Players, baseline.Session.Econ.Players) {
				t.Fatal("equivalent adapters produced different setup/player records")
			}
		})
	}
}

func TestFreshBattleInvalidRequestDiagnosticFamilyMatches(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	var family string
	for _, adapter := range []string{"direct", "menu", "displayless"} {
		_, err := ComposeFreshBattle(FreshBattleRequest{
			Kind: ScenarioCampaign, Mission: "camps/definitely-missing-campaign.tdf:MISSION0", LocalOwner: -1,
			SimulationSeed: 1, CRTSeed: 1, FS: fs,
		})
		if err == nil {
			t.Fatalf("adapter %q unexpectedly loaded", adapter)
		}
		got := err.Error()
		idx := strings.Index(got, ": logical path")
		if idx < 0 {
			t.Fatalf("diagnostic has no standard family boundary: %q", got)
		}
		got = got[:idx]
		if family == "" {
			family = got
		} else if got != family {
			t.Fatalf("diagnostic families differ: %q/%q", got, family)
		}
	}
}

func TestFreshCampaignAdaptersHaveEqualAuthoritativeSetup(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	const identity = "camps/Arm Campaign.tdf:MISSION0"
	var baseline FreshBattle
	for i, adapter := range []string{"direct", "menu", "displayless"} {
		t.Run(adapter, func(t *testing.T) {
			got, err := ComposeFreshBattle(FreshBattleRequest{
				Kind: ScenarioCampaign, Mission: identity,
				CampaignIndex: 0, CampaignSlot: 0, Difficulty: 1, LocalOwner: -1,
				SimulationSeed: 47, CRTSeed: 53, FS: fs,
				PresentationWidth: 640, PresentationHeight: 480,
			})
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				baseline = got
				return
			}
			if got.Identity != baseline.Identity || got.SimulationSeed != baseline.SimulationSeed || got.CRTSeed != baseline.CRTSeed || got.LocalOwner != baseline.LocalOwner || got.TerrainWidth != baseline.TerrainWidth || got.TerrainHeight != baseline.TerrainHeight || got.InitialHash != baseline.InitialHash {
				t.Fatalf("campaign setup differs: got=%+v baseline=%+v", got, baseline)
			}
			if !reflect.DeepEqual(got.Session.Econ.Players, baseline.Session.Econ.Players) {
				t.Fatal("equivalent campaign adapters produced different player records")
			}
		})
	}
}

// TestCoastToCoastSurvivesTheCancelledMobileBuild is the retail-gated half of
// WU-19-73's crash fix. Seed 7 on `Coast to Coast` panicked at tick 3865 with
// `slice bounds out of range [1:0]` in the order queue's head removal: a
// `MobileBuild` record carrying gate bit 1 was removed while
// [R-ORDER-02 §2]'s cancel notification — construction's cancel-current — had
// already removed it, and the outer removal spliced a segment that was no
// longer there. The queue now unlinks by identity after the cleanup, so the
// second removal finds nothing to remove.
//
// The mechanism itself is locked without retail assets by
// TestRemoveHeadSurvivesACancelNoticeThatRemovesTheHead in internal/orders;
// this run is the scenario that found it. 5000 ticks clears the crash tick with
// room to spare.
func TestCoastToCoastSurvivesTheCancelledMobileBuild(t *testing.T) {
	request := Request{
		Root:           testsupport.RetailRoot(t),
		Map:            "Coast to Coast",
		SimulationSeed: 7,
		CRTSeed:        7,
		TickLimit:      5000,
	}
	report, err := Run(request)
	if !errors.Is(err, ErrTickLimit) {
		t.Fatalf("Run error = %v, want the tick limit (a panic here is the regression)", err)
	}
	if report.Tick != 5000 || report.Status != "tick_limit" {
		t.Fatalf("tick/status = %d/%q, want 5000/tick_limit", report.Tick, report.Status)
	}
}
