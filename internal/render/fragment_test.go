package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

type fragmentImpactRecorder struct {
	pool        *FixedEffectPool
	groundCalls int
	liveAtCall  bool
	appendOK    bool
}

func (r *fragmentImpactRecorder) GroundFragmentImpact(GroundFragmentImpact) {
	r.groundCalls++
	r.liveAtCall = r.pool.fragments[0].live && r.pool.records[FixedEffectCap-1].FragmentSlot == 1
	r.appendOK = r.pool.Append(persistentEffect(1000))
}

func (r *fragmentImpactRecorder) WaterFragmentImpact(WaterFragmentImpact) {
}

// TestShatterAdmissionAndCollision locks paired capacity-before-draw admission,
// the X/Z/Y draw order, inclusive terrain contact, and impact-before-release
// compaction [04 R-COB-04 §3].
func TestShatterAdmissionAndCollision(t *testing.T) {
	normal := fragmentNormal(FragmentQuad{Vertices: [4][3]numeric.Fixed{
		{}, {65535, 0, 0}, {65535, 65535, 0}, {},
	}})
	if normal != [3]float32{0, 0, 1} || fragmentVertexFloat(65536) <= 1 {
		t.Fatalf("normal=%v scale=%g, want +Z and reciprocal-65535 conversion", normal, fragmentVertexFloat(65536))
	}

	var pool FixedEffectPool
	service := NewEffectServiceWithPool(FixedEffectCap, &pool)
	for i := 0; i < FixedEffectCap-1; i++ {
		if !pool.Append(persistentEffect(i)) {
			t.Fatalf("filler %d admission failed", i)
		}
	}

	freezeCalls := 0
	req := FragmentRequest{
		UnitDefID: 9, PieceIndex: 2,
		Position:     [3]numeric.Fixed{0, numeric.FixedFromInt(9), 0},
		ExplodeOnHit: true,
		Quads: []FragmentQuad{
			{PrimitiveIndex: 7}, // zero area reaches the ordinary NaN-to-zero conversion path.
			{PrimitiveIndex: 8},
		},
		Freeze: func(defID uint16, pieceIndex int, quad FragmentQuad) FrozenFragmentMaterial {
			freezeCalls++
			return FrozenFragmentMaterial{UnitDefID: defID, PieceIndex: pieceIndex, PrimitiveIndex: quad.PrimitiveIndex}
		},
	}
	stream := rng.SimulationFromState(7)
	if !service.AdmitShatter(req, stream.Uint32n) {
		t.Fatal("first fragment admission failed")
	}
	if freezeCalls != 1 || stream.Draws() != 8 || pool.Len() != FixedEffectCap {
		t.Fatalf("freeze=%d draws=%d len=%d, want one/eight/%d", freezeCalls, stream.Draws(), pool.Len(), FixedEffectCap)
	}

	probe := rng.SimulationFromState(7)
	wantX := int32(80-int32(probe.Uint32n(160))) * 512
	wantZ := int32(80-int32(probe.Uint32n(160))) * 512
	wantY := int32(80-int32(probe.Uint32n(160))) * 512
	for i := 0; i < 5; i++ {
		probe.Uint32n([]uint32{1600, 1600, 1600, 200, 200}[i])
	}
	record := &pool.records[FixedEffectCap-1]
	if record.VX != numeric.Fixed(wantX) || record.VY != numeric.Fixed(wantY) || record.VZ != numeric.Fixed(wantZ) {
		t.Fatalf("velocity=(%d,%d,%d), want X/Z/Y-draw values (%d,%d,%d)", record.VX, record.VY, record.VZ, wantX, wantY, wantZ)
	}
	views := pool.SnapshotViews()
	if got := views[FixedEffectCap-1]; got.FragmentSlot != 1 || got.Strip != -1 {
		t.Fatalf("published fragment view=%+v, want FragmentSlot=1 and Strip=-1", got)
	}
	metadata := service.FragmentMetadataInto(nil)
	if len(metadata) != 1 || metadata[0].Slot != 0 || metadata[0].Position[1] != numeric.FixedFromInt(9) {
		t.Fatalf("metadata=%+v, want one first-slot fragment at source position", metadata)
	}

	// Make the next predicted Y exactly terrain height. The -65536 rate bounces
	// to 32768, whose signed whole word is zero, so this visit removes the pair.
	record.VX, record.VY, record.VZ = 0, -numeric.FixedFromInt(1), 0
	recorder := &fragmentImpactRecorder{pool: &pool}
	service.SetFragmentStepContext(FragmentStepContext{
		TerrainHeight: func(numeric.Fixed, numeric.Fixed) numeric.Fixed { return numeric.FixedFromInt(8) },
		Impact:        recorder,
	})
	pool.Update(1)
	if recorder.groundCalls != 1 || !recorder.liveAtCall || recorder.appendOK {
		t.Fatalf("ground=%d liveAtCall=%v appendOK=%v, want 1/true/false", recorder.groundCalls, recorder.liveAtCall, recorder.appendOK)
	}
	if pool.Len() != FixedEffectCap-1 || pool.fragments[0].live || len(service.FragmentMetadataInto(nil)) != 0 {
		t.Fatalf("post-contact len=%d live=%v metadata=%d, want %d/false/0", pool.Len(), pool.fragments[0].live, len(service.FragmentMetadataInto(nil)), FixedEffectCap-1)
	}
}
