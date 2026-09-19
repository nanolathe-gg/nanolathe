package path

import (
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

type storageScenario struct {
	name   string
	width  int32
	height int32
	start  Cell
	goal   Cell
	pass   func(Cell) uint8
}

func pathStorageScenarios() []storageScenario {
	return []storageScenario{
		{
			name:   "open",
			width:  96,
			height: 96,
			start:  Cell{X: 3, Z: 48},
			goal:   Cell{X: 92, Z: 48},
			pass:   func(Cell) uint8 { return 3 },
		},
		{
			name:   "wall",
			width:  96,
			height: 96,
			start:  Cell{X: 3, Z: 48},
			goal:   Cell{X: 92, Z: 48},
			pass: func(c Cell) uint8 {
				if c.X == 48 && c.Z != 3 {
					return 0
				}
				return 3
			},
		},
		{
			name:   "maze",
			width:  96,
			height: 96,
			start:  Cell{X: 2, Z: 2},
			goal:   Cell{X: 93, Z: 93},
			pass: func(c Cell) uint8 {
				if c.X <= 0 || c.Z <= 0 || c.X >= 95 || c.Z >= 95 {
					return 0
				}
				if c.X%4 == 0 && c.Z%4 != 3 {
					return 0
				}
				if c.Z%6 == 0 && c.X%6 != 2 {
					return 0
				}
				return 3
			},
		},
	}
}

func storageConfig(sc storageScenario, pass func(Cell) uint8) SearchConfig {
	return SearchConfig{
		Start:         sc.start,
		Goal:          PointGoal(sc.goal, 0),
		Scale:         65536,
		HasBounds:     true,
		Bounds:        Rect{Min: Cell{}, Max: Cell{X: sc.width - 1, Z: sc.height - 1}},
		PassableValue: pass,
	}
}

func TestPathStorageBehaviorFingerprint(t *testing.T) {
	for _, sc := range pathStorageScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			cfg := storageConfig(sc, sc.pass)
			one := RunSearch(cfg)
			s := NewSession(cfg)
			for !s.IsDone() {
				before := s.Popped()
				points, status, done := s.Resume(7)
				delta := s.Popped() - before
				if delta > 7 {
					t.Fatalf("resume exceeded budget: before=%d after=%d", before, s.Popped())
				}
				if !done && (len(points) != 0 || status != 0) {
					t.Fatalf("budget boundary published partial result: points=%v status=%#x", points, status)
				}
			}
			resumed := SearchResult{
				Points:     s.resultPoints,
				Status:     s.resultStatus,
				Notified:   s.notified,
				Popped:     s.popped,
				SetupSteps: s.setupSteps,
				Seeded:     s.seeded,
			}
			if !reflect.DeepEqual(resumed.Points, one.Points) || resumed.Status != one.Status || resumed.Notified != one.Notified || resumed.Popped != one.Popped || resumed.SetupSteps != one.SetupSteps || resumed.Seeded != one.Seeded {
				t.Fatalf("resumed result differs from one-shot: one=%+v resumed=%+v", one, resumed)
			}
		})
	}
}

func TestPathStorageNoSimulationRandomDraws(t *testing.T) {
	oldSim, oldCRT := rng.Global.Sim, rng.Global.Crt
	defer func() {
		rng.Global.Sim, rng.Global.Crt = oldSim, oldCRT
	}()
	rng.SeedGlobal(7, 11)
	state, draws := rng.Global.Sim.State, rng.Global.Sim.Draws()
	sc := pathStorageScenarios()[2]
	_ = RunSearch(storageConfig(sc, sc.pass))
	if rng.Global.Sim.State != state || rng.Global.Sim.Draws() != draws {
		t.Fatalf("path search advanced the simulation stream: state %#x -> %#x, draws %d -> %d", state, rng.Global.Sim.State, draws, rng.Global.Sim.Draws())
	}
}

func BenchmarkPathStorage(b *testing.B) {
	for _, sc := range pathStorageScenarios() {
		b.Run(sc.name, func(b *testing.B) {
			cfg := storageConfig(sc, sc.pass)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = RunSearch(cfg)
			}
		})
	}
}

func BenchmarkPathStorageMultiTickRestamp(b *testing.B) {
	sc := pathStorageScenarios()[2]
	blocked := false
	pass := func(c Cell) uint8 {
		if blocked && c.X == 48 {
			return 0
		}
		return sc.pass(c)
	}
	cfg := storageConfig(sc, pass)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		blocked = false
		s := NewSession(cfg)
		for tick := 0; !s.IsDone(); tick++ {
			if tick == 3 {
				blocked = true
			}
			s.Resume(7)
		}
	}
}

// BenchmarkPathStorageLifecycle includes both normal completion and an
// abandoned in-progress request, which is the path-level equivalent of a
// scheduler cancellation. It reports ordinary Go allocation statistics; the
// retained-heap probe below measures live runtime storage separately.
func BenchmarkPathStorageLifecycle(b *testing.B) {
	sc := pathStorageScenarios()[2]
	cfg := storageConfig(sc, sc.pass)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		completed := NewSession(cfg)
		for !completed.IsDone() {
			completed.Resume(1 << 30)
		}
		cancelled := NewSession(cfg)
		if _, _, done := cancelled.Resume(7); done {
			b.Fatal("lifecycle probe unexpectedly completed its cancellation slice")
		}
	}
}

func TestPathStorageSessionLifecycle(t *testing.T) {
	sc := pathStorageScenarios()[2]
	cfg := storageConfig(sc, sc.pass)
	completed := NewSession(cfg)
	for !completed.IsDone() {
		completed.Resume(1 << 30)
	}
	cancelled := NewSession(cfg)
	if _, _, done := cancelled.Resume(7); done {
		t.Fatal("cancellation lifecycle unexpectedly published on a budget boundary")
	}
	if cancelled.IsDone() {
		t.Fatal("abandoned lifecycle was marked done")
	}
}

func TestPathStorageSessionNodeStoreResetRebindsEntries(t *testing.T) {
	sc := pathStorageScenarios()[0]
	s := NewSession(storageConfig(sc, sc.pass))
	if s.ns == nil || s.entries.len() == 0 {
		t.Fatal("session did not initialize shared node storage")
	}
	done, popped := s.IsDone(), s.Popped()
	// Reset is a lifecycle table-ownership check. A live search also owns heap
	// and control state, so this test deliberately does not resume afterward.
	s.ns.Reset()
	if s.entries.len() != 0 {
		t.Fatalf("node store reset left stale session entries: %d", s.entries.len())
	}
	if s.IsDone() != done || s.Popped() != popped {
		t.Fatalf("node store reset changed session control state: done %v/%v popped %d/%d", done, s.IsDone(), popped, s.Popped())
	}
	id := s.ns.Alloc(s.Start(), 0, 0, invalidNodeID, DirNone)
	if got, ok := s.ns.Find(s.Start()); !ok || got != id {
		t.Fatalf("reset store lookup mismatch: got %d, want %d", got, id)
	}
	if got := s.entries.get(s.Start()).node; got != id {
		t.Fatalf("reset store did not publish through session map: got %d, want %d", got, id)
	}
}

func TestPathStorageRetainedHeap(t *testing.T) {
	// HeapAlloc is the runtime's live allocation total. Keeping the sessions
	// reachable makes this a retained-storage measurement across every map,
	// slice backing array and allocator bucket, rather than a struct-payload
	// estimate. The exact absolute value is runtime-specific; the raw deltas
	// are logged for the baseline and changed revisions.
	const retainedSessions = 4
	sc := pathStorageScenarios()[2]
	cfg := storageConfig(sc, sc.pass)
	oldGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(oldGC)
	var before, completedHeld, completedReleased, cancelledHeld, cancelledReleased runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	completed := make([]*Session, retainedSessions)
	for i := range completed {
		completed[i] = NewSession(cfg)
		for !completed[i].IsDone() {
			completed[i].Resume(1 << 30)
		}
	}
	runtime.KeepAlive(completed)
	runtime.GC()
	runtime.ReadMemStats(&completedHeld)
	runtime.KeepAlive(completed)
	completed = nil
	runtime.GC()
	runtime.ReadMemStats(&completedReleased)

	cancelled := make([]*Session, retainedSessions)
	for i := range cancelled {
		cancelled[i] = NewSession(cfg)
		if _, _, done := cancelled[i].Resume(7); done {
			t.Fatal("retained cancellation probe unexpectedly completed")
		}
	}
	runtime.KeepAlive(cancelled)
	runtime.GC()
	runtime.ReadMemStats(&cancelledHeld)
	runtime.KeepAlive(cancelled)
	cancelled = nil
	runtime.GC()
	runtime.ReadMemStats(&cancelledReleased)
	if completedHeld.HeapAlloc <= completedReleased.HeapAlloc {
		t.Fatalf("completed sessions did not retain live storage: held=%d released=%d", completedHeld.HeapAlloc, completedReleased.HeapAlloc)
	}
	if cancelledHeld.HeapAlloc <= cancelledReleased.HeapAlloc {
		t.Fatalf("partial sessions did not retain live storage: held=%d released=%d", cancelledHeld.HeapAlloc, cancelledReleased.HeapAlloc)
	}

	t.Logf("retained HeapAlloc bytes: baseline=%d completed-held=%d completed-released=%d cancelled-held=%d cancelled-released=%d; completed-held-delta=%d completed-release-residual=%d cancelled-held-delta=%d cancelled-release-residual=%d", before.HeapAlloc, completedHeld.HeapAlloc, completedReleased.HeapAlloc, cancelledHeld.HeapAlloc, cancelledReleased.HeapAlloc, int64(completedHeld.HeapAlloc)-int64(before.HeapAlloc), int64(completedReleased.HeapAlloc)-int64(before.HeapAlloc), int64(cancelledHeld.HeapAlloc)-int64(completedReleased.HeapAlloc), int64(cancelledReleased.HeapAlloc)-int64(completedReleased.HeapAlloc))
}
