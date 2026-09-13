package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Shared art and reused source identity must not replace copied weapon values.
// Compaction preserves each survivor's metadata and relative admission order.
func TestBlastProfileSurvivesPoolCompaction(t *testing.T) {
	p := &FixedEffectPool{}
	s := NewEffectServiceWithPool(EffectCapacity, p)
	events := []Event{
		{ID: 1, Sequence: 1, Kind: KindExplosion, Graphic: "shared", Source: 4, DurationsA: []int32{1}, HasBlastProfile: true, BlastAreaOfEffect: 511, BlastDamage: 1001},
		{ID: 2, Sequence: 2, Kind: KindExplosion, Graphic: "shared", Source: 4, DurationsA: []int32{10}, HasBlastProfile: true, BlastAreaOfEffect: 129, BlastDamage: 0},
		{ID: 3, Sequence: 3, Kind: KindCOBSFX, Graphic: "shared", Source: 4, DurationsA: []int32{10}},
	}
	s.Advance(1, events)
	records := p.Records()
	if len(records) != 3 || !records[0].HasBlastProfile || records[0].BlastDamage != 1001 || !records[1].HasBlastProfile || records[1].BlastDamage != 0 || records[2].HasBlastProfile {
		t.Fatalf("records = %+v", records)
	}
	views := p.SnapshotViewsInto(make([]frame.EffectView, 0, 3))
	s.Advance(2, nil)
	views = p.SnapshotViewsInto(views)
	if len(views) != 2 || views[0].ID != 2 || views[1].ID != 3 {
		t.Fatalf("compaction order = %+v", views)
	}
	if !views[0].HasBlastProfile || views[0].BlastAreaOfEffect != 129 || views[0].BlastDamage != 0 || views[1].HasBlastProfile || views[1].BlastAreaOfEffect != 0 || views[1].BlastDamage != 0 {
		t.Fatalf("compacted profiles = %+v", views)
	}
}
