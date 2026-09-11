package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// battleBenchmarkDiagnostics observes the owner between viewer steps. Snapshot
// allocation and disk writes belong outside the measured profile/memory window.
// Unlike the interactive debug capture, this never changes pause or publishes.
type battleBenchmarkDiagnostics struct {
	directory string
	sess      *session.Session
	previous  func(string, uint32)
	active    bool
	timing    battleBenchmarkPhaseTimer
}

func newBattleBenchmarkDiagnostics(directory string, sess *session.Session) *battleBenchmarkDiagnostics {
	return &battleBenchmarkDiagnostics{directory: directory, sess: sess}
}

func (d *battleBenchmarkDiagnostics) Begin() error {
	if d == nil || d.active {
		return nil
	}
	if d.sess == nil || d.sess.Clock == nil {
		return fmt.Errorf("nanolathe: benchmark diagnostics failed: logical path %s, providers searched [session], expected an initialized session clock", d.directory)
	}
	if err := d.snapshot("state-start"); err != nil {
		return err
	}
	d.timing = battleBenchmarkPhaseTimer{}
	d.previous = d.sess.PhaseObserver
	d.sess.PhaseObserver = d.observe
	d.active = true
	return nil
}

func (d *battleBenchmarkDiagnostics) observe(phase string, tick uint32) {
	d.timing.observe(phase, time.Now())
	if d.previous != nil {
		d.previous(phase, tick)
	}
}

// Step wraps the benchmark's one-tick viewer step. Frames which only present
// an interpolation sample do not call this; a paused/nonadvancing step is not
// counted as a measured tick. No measured-path slices or maps are allocated.
func (d *battleBenchmarkDiagnostics) Step(fn func()) {
	if d == nil || !d.active {
		fn()
		return
	}
	tick := d.sess.Clock.GlobalTick
	d.timing.begin(time.Now())
	fn()
	d.timing.end(time.Now(), d.sess.Clock.GlobalTick-tick)
}

func (d *battleBenchmarkDiagnostics) End() error {
	if d == nil || !d.active {
		return nil
	}
	d.active = false
	d.sess.PhaseObserver = d.previous
	d.previous = nil
	return errors.Join(d.snapshot("state-end"), battleBenchmarkWriteJSON(filepath.Join(d.directory, "phases.json"), d.timing.summary()))
}

func (d *battleBenchmarkDiagnostics) snapshot(name string) error {
	dir := filepath.Join(d.directory, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return battleBenchmarkArtifactError(dir, err)
	}
	s := d.sess
	if err := battleBenchmarkWriteJSON(filepath.Join(dir, "session.json"), s.DebugSnapshot()); err != nil {
		return err
	}
	if err := battleBenchmarkWrite(filepath.Join(dir, "units.jsonl"), func(w io.Writer) error {
		encoder := json.NewEncoder(w)
		return s.VisitDebugUnits(func(unit session.DebugUnit) error { return encoder.Encode(unit) })
	}); err != nil {
		return err
	}
	// Null identifies an absent service, rather than implying an empty live
	// service. These are the same bounded projections as docs/DEBUG_CAPTURE.md.
	var movement, features, projectiles, construction, admissions any
	if s.Movement != nil {
		movement = s.Movement.ParitySnapshot(s.Units, s.Clock.GlobalTick)
	}
	if s.Features != nil {
		features = s.Features.DebugSnapshot()
	}
	if s.Combat != nil {
		projectiles = s.Combat.DebugSnapshot()
	}
	if s.Build != nil {
		construction = s.Build.SnapshotLinks()
		admissions = s.Build.DebugAdmissionSnapshot()
	}
	ai := make([]any, len(s.AI))
	for i, manager := range s.AI {
		if manager != nil {
			ai[i] = manager.DebugSnapshot()
		}
	}
	for _, artifact := range []struct {
		name string
		data any
	}{
		{"movement.json", movement}, {"features.json", features},
		{"projectiles.json", projectiles}, {"construction.json", construction},
		{"construction-admissions.json", admissions}, {"ai.json", ai},
	} {
		if err := battleBenchmarkWriteJSON(filepath.Join(dir, artifact.name), artifact.data); err != nil {
			return err
		}
	}
	return nil
}

func battleBenchmarkWriteJSON(path string, value any) error {
	return battleBenchmarkWrite(path, func(w io.Writer) error {
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		return e.Encode(value)
	})
}

func battleBenchmarkWrite(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return battleBenchmarkArtifactError(path, err)
	}
	err = errors.Join(write(f), f.Close())
	if err != nil {
		return battleBenchmarkArtifactError(path, err)
	}
	return nil
}

func battleBenchmarkArtifactError(path string, err error) error {
	return fmt.Errorf("nanolathe: benchmark artifact write failed: logical path %s, providers searched [filesystem], expected a writable diagnostic artifact: %w", path, err)
}

type battleBenchmarkPhaseCost struct {
	Phase         string  `json:"phase"`
	TotalNS       int64   `json:"total_ns"`
	MeanNSPerTick float64 `json:"mean_ns_per_tick"`
	MaxNSPerTick  int64   `json:"max_ns_per_tick"`
	Calls         uint64  `json:"calls"`
}

type battleBenchmarkPhaseReport struct {
	MeasuredTicks uint64                     `json:"measured_ticks"`
	Phases        []battleBenchmarkPhaseCost `json:"phases"`
}

// Registry callbacks arrive once per phase in order [I7]. The thirteenth
// slot includes session sharing/result/publication and the remaining viewer
// work (event drains and host bookkeeping); it is explicitly a host tail.
type battleBenchmarkPhaseTimer struct {
	names         [13]string
	total, max    [13]time.Duration
	current       [13]time.Duration
	calls         [13]uint64
	index         int
	last          time.Time
	measuredTicks uint64
}

func (t *battleBenchmarkPhaseTimer) begin(now time.Time) {
	t.current = [13]time.Duration{}
	t.index = 0
	t.last = now
}

func (t *battleBenchmarkPhaseTimer) observe(phase string, now time.Time) {
	if t.index >= len(t.current)-1 {
		return
	}
	t.names[t.index] = phase
	t.current[t.index] = now.Sub(t.last)
	t.last = now
	t.index++
}

func (t *battleBenchmarkPhaseTimer) end(now time.Time, ticks uint32) {
	if ticks == 0 {
		return
	}
	t.measuredTicks += uint64(ticks)
	tail := len(t.current) - 1
	t.names[tail] = "host-tail-sharing-result-publication-viewer"
	t.current[tail] = now.Sub(t.last)
	for i, elapsed := range t.current {
		if i >= t.index && i != tail {
			continue
		}
		t.total[i] += elapsed
		t.max[i] = max(t.max[i], elapsed)
		t.calls[i]++
	}
}

func (t *battleBenchmarkPhaseTimer) summary() battleBenchmarkPhaseReport {
	out := battleBenchmarkPhaseReport{MeasuredTicks: t.measuredTicks, Phases: []battleBenchmarkPhaseCost{}}
	for i, name := range t.names {
		if t.calls[i] == 0 {
			continue
		}
		out.Phases = append(out.Phases, battleBenchmarkPhaseCost{
			Phase: name, TotalNS: t.total[i].Nanoseconds(),
			MeanNSPerTick: float64(t.total[i].Nanoseconds()) / float64(t.measuredTicks),
			MaxNSPerTick:  t.max[i].Nanoseconds(), Calls: t.calls[i],
		})
	}
	return out
}
