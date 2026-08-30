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
