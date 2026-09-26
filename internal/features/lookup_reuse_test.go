package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Lookup snapshots own their pointer slice even after the service rebuilds
// its key/value cache. Grid reconciliation must still visit later anchors
// when earlier stale records are removed in the same pass [05 R-FEAT-01 §4].
func TestLookupRebuildPreservesSnapshotsAndRemovalWalk(t *testing.T) {
	s := restingForestService(t, 4, 0)
	first := s.Instances()
	if len(first) != 16 {
		t.Fatal("incomplete fixture")
	}
	for _, i := range []int{0, 1, 5, 15} {
		s.Terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	s.TickMotion(1)
	after := s.Instances()
	if len(after) != 12 {
		t.Fatalf("reconciliation retained stale anchors: %d", len(after))
	}
	for i, inst := range first {
		if inst == nil || inst.CZ*4+inst.CX != i {
			t.Fatalf("cache rebuild mutated caller snapshot at %d", i)
		}
	}
	for i, inst := range after {
		idx := inst.CZ*4 + inst.CX
		if idx == 0 || idx == 1 || idx == 5 || idx == 15 || (i > 0 && after[i-1].CZ*4+after[i-1].CX >= idx) {
			t.Fatalf("remaining anchor sequence is wrong at %d", idx)
		}
	}
	// A removed record must not remain reachable through unused cache slots.
	for _, inst := range s.instanceValues[len(s.instanceValues):cap(s.instanceValues)] {
		if inst != nil {
			t.Fatal("cache tail retains a removed record")
		}
	}
}

func BenchmarkFeatureLookupChurn(b *testing.B) {
	s := restingForestService(b, 64, 0)
	inst := s.InstanceAt(0, 0)
	dst := s.Instances()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.deleteInstance(0)
		dst = s.AppendInstances(dst[:0])
		s.setInstance(0, inst)
		dst = s.AppendInstances(dst[:0])
	}
}
