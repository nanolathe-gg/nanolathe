package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func benchmarkDiagnosticsFixture(t *testing.T) *session.Session {
	t.Helper()
	w := units.NewSliced(2, nil)
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "diagnostic-fixture"}, MaxDamage: 10}
	authorTestUnitScripts(def)
	_, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{Clock: &clock.State{Requested: 10, Active: 10}, Units: w, Snapshot: frame.NewBuffer(), State: session.StateBattle}
	s.SeedSessionRNG(7, 11)
	return s
}

func TestBattleBenchmarkSnapshotsPreserveState(t *testing.T) {
	s := benchmarkDiagnosticsFixture(t)
	fingerprint, err := s.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	sim, crt, beforeClock, beforeFrame := *s.SimRNG(), *s.CrtRNG(), *s.Clock, s.Snapshot.Current()
	d := newBattleBenchmarkDiagnostics(t.TempDir(), s)
	if err := d.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := d.End(); err != nil {
		t.Fatal(err)
	}
	after, err := s.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if after != fingerprint || sim != *s.SimRNG() || crt != *s.CrtRNG() || beforeClock != *s.Clock || beforeFrame != s.Snapshot.Current() {
		t.Fatal("diagnostic snapshots changed state, clock, RNG or publication")
	}
	for _, name := range []string{"session.json", "units.jsonl", "movement.json", "features.json", "projectiles.json", "construction.json", "construction-admissions.json", "ai.json"} {
		start, err := os.ReadFile(filepath.Join(d.directory, "state-start", name))
		if err != nil {
			t.Fatal(err)
		}
		end, err := os.ReadFile(filepath.Join(d.directory, "state-end", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(start, end) {
			t.Fatalf("unchanged state produced different %s", name)
		}
		if name == "units.jsonl" {
			var unit session.DebugUnit
			if err := json.Unmarshal(start, &unit); err != nil {
				t.Fatal(err)
			}
			if unit.PublishedIdentity != 0 || unit.Orders != nil {
				t.Fatal("capture minted an identity or created an order queue")
			}
		}
	}
}

func TestBattleBenchmarkDiagnosticsObserveOnlyAdvancingTicks(t *testing.T) {
	s, control := benchmarkDiagnosticsFixture(t), benchmarkDiagnosticsFixture(t)
	calls := 0
	s.PhaseObserver = func(string, uint32) { calls++ }
	d := newBattleBenchmarkDiagnostics(t.TempDir(), s)
	if err := d.Begin(); err != nil {
		t.Fatal(err)
	}
	d.Step(func() {}) // An interpolation-only or stopped host sample.
	for i := 0; i < 3; i++ {
		d.Step(func() { s.Step(s.Clock.ScaledAnchor + 1) })
		control.Step(control.Clock.ScaledAnchor + 1)
	}
	if err := d.End(); err != nil {
		t.Fatal(err)
	}
	observed, err := s.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	want, err := control.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if observed != want || *s.SimRNG() != *control.SimRNG() || *s.CrtRNG() != *control.CrtRNG() {
		t.Fatal("observer changed advancing session state")
	}
	report := d.timing.summary()
	if report.MeasuredTicks != uint64(s.Clock.GlobalTick) || report.MeasuredTicks == 0 || calls != int(report.MeasuredTicks)*12 || len(report.Phases) != 13 {
		t.Fatalf("incomplete attribution: calls=%d report=%+v", calls, report)
	}
	for _, row := range report.Phases {
		// Empty phases can begin and end within one host clock quantum. Their
		// zero duration is valid; attribution is proved by the call count.
		if row.Calls != report.MeasuredTicks || row.MaxNSPerTick < 0 || row.TotalNS < row.MaxNSPerTick || row.MeanNSPerTick < 0 {
			t.Fatalf("invalid phase row: %+v", row)
		}
	}
	previousCalls := calls
	s.PhaseObserver("after-end", 0)
	if calls != previousCalls+1 {
		t.Fatal("previous observer was not restored")
	}
	d.Step(func() { calls++ })
	if calls != previousCalls+2 {
		t.Fatal("inactive Step did not call its function")
	}
}

func TestBattleBenchmarkPhaseTimerPartitionsViewerStep(t *testing.T) {
	var timer battleBenchmarkPhaseTimer
	start := time.Unix(0, 0)
	timer.begin(start)
	timer.observe("first", start.Add(2*time.Nanosecond))
	timer.observe("second", start.Add(5*time.Nanosecond))
	timer.end(start.Add(12*time.Nanosecond), 1)
	timer.begin(start)
	timer.observe("first", start.Add(4*time.Nanosecond))
	timer.observe("second", start.Add(5*time.Nanosecond))
	timer.end(start.Add(8*time.Nanosecond), 1)
	timer.begin(start)
	timer.end(start.Add(time.Hour), 0)
	report := timer.summary()
	if report.MeasuredTicks != 2 || len(report.Phases) != 3 {
		t.Fatalf("wrong denominator or rows: %+v", report)
	}
	want := []battleBenchmarkPhaseCost{
		{Phase: "first", TotalNS: 6, MeanNSPerTick: 3, MaxNSPerTick: 4, Calls: 2},
		{Phase: "second", TotalNS: 4, MeanNSPerTick: 2, MaxNSPerTick: 3, Calls: 2},
		{Phase: "host-tail-sharing-result-publication-viewer", TotalNS: 10, MeanNSPerTick: 5, MaxNSPerTick: 7, Calls: 2},
	}
	for i, row := range report.Phases {
		if row != want[i] {
			t.Fatalf("phase %d: got %+v, want %+v", i, row, want[i])
		}
	}
}
