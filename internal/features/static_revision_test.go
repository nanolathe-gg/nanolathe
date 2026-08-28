package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func TestStaticRevisionFeatureLifecycle(t *testing.T) {
	terrain := newEmptyTerrain(8, 8)
	blocking := featureDef("blocking-tree", 0, 0, 10)
	blocking.Blocking = true
	blocking.FootprintX, blocking.FootprintZ = 2, 2
	blocking.FeatureDeadDef = featureDef("dead-tree", 0, 0, 10)
	blocking.FeatureDeadDef.Blocking = true
	blocking.FeatureReclamateDef = featureDef("reclaimed-smudge", 0, 0, 10)
	terrain.FeatureDefs = []*content.FeatureDef{blocking, blocking.FeatureDeadDef, blocking.FeatureReclamateDef}
	svc := NewService(terrain, nil, nil, nil)

	if svc.PlaceAt(1, 1, blocking) == nil {
		t.Fatal("blocking feature was not placed")
	}
	if got := terrain.StaticObstacleRevision(); got != 1 {
		t.Fatalf("blocking create revision = %d, want 1", got)
	}

	// A blocking feature's final removal changes static passability once.
	svc.RemoveFeatureAt(1, 1, CauseBurnt)
	if got := terrain.StaticObstacleRevision(); got != 2 {
		t.Fatalf("blocking removal revision = %d, want 2", got)
	}

	// Decorative/nonblocking features do not invalidate static routes.
	decorative := featureDef("decorative", 0, 0, 0)
	if svc.PlaceAt(3, 3, decorative) == nil {
		t.Fatal("decorative feature was not placed")
	}
	if got := terrain.StaticObstacleRevision(); got != 2 {
		t.Fatalf("nonblocking create revision = %d, want unchanged 2", got)
	}
	svc.RemoveFeatureAt(3, 3, CauseDead)
	if got := terrain.StaticObstacleRevision(); got != 2 {
		t.Fatalf("nonblocking removal revision = %d, want unchanged 2", got)
	}

	// A blocking→blocking successor is one logical mutation, despite clearing
	// the old footprint and stamping a new one.
	if svc.PlaceAt(1, 1, blocking) == nil {
		t.Fatal("blocking feature was not recreated")
	}
	before := terrain.StaticObstacleRevision()
	svc.RemoveFeatureAt(1, 1, CauseDead)
	if got := terrain.StaticObstacleRevision(); got != before+1 {
		t.Fatalf("blocking successor revision = %d, want %d", got, before+1)
	}

	// Reclaiming a blocking feature into a nonblocking successor also bumps
	// once, opening the corridor for a subsequent route publication.
	before = terrain.StaticObstacleRevision()
	svc.RemoveFeatureAt(1, 1, CauseReclaim)
	if got := terrain.StaticObstacleRevision(); got != before+1 {
		t.Fatalf("reclaim-open revision = %d, want %d", got, before+1)
	}

	// A failed blocking successor still leaves the old blocking footprint
	// removed, so the transition must advance the revision once rather than
	// silently leaving stale routes valid.
	if svc.PlaceAt(6, 6, blocking) == nil {
		t.Fatal("blocking feature was not placed for failed-successor case")
	}
	blocking.FeatureDeadDef = &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "too-large"},
		Blocking:         true,
		FootprintX:       4,
		FootprintZ:       4,
	}
	before = terrain.StaticObstacleRevision()
	svc.RemoveFeatureAt(6, 6, CauseDead)
	if got := terrain.StaticObstacleRevision(); got != before+1 {
		t.Fatalf("failed blocking successor revision = %d, want %d", got, before+1)
	}
	if svc.InstanceAt(6, 6) != nil {
		t.Fatal("failed successor unexpectedly left an instance")
	}
}

func TestStaticRevisionBurnReplacementCoalescesDirectClearAndSpawn(t *testing.T) {
	terrain := newEmptyTerrain(8, 8)
	burning := featureDef("burning", 0, 0, 10)
	burning.Blocking = true
	burning.FeatureBurntDef = featureDef("burnt", 0, 0, 10)
	burning.FeatureBurntDef.Blocking = true
	terrain.FeatureDefs = []*content.FeatureDef{burning, burning.FeatureBurntDef}
	svc := NewService(terrain, nil, nil, nil)
	inst := svc.PlaceAt(2, 2, burning)
	if inst == nil {
		t.Fatal("burning feature was not placed")
	}
	inst.IsBurning = true
	inst.BurnDuration = 1
	before := terrain.StaticObstacleRevision()
	svc.TickLifecycle(1)
	if got := terrain.StaticObstacleRevision(); got != before+1 {
		t.Fatalf("blocking burn replacement revision = %d, want %d", got, before+1)
	}
	if got := svc.InstanceAt(2, 2); got == nil || got.Def != burning.FeatureBurntDef {
		t.Fatalf("burn replacement instance = %#v, want blocking successor", got)
	}
}

func TestStaticRevisionBurnFailedBlockingSuccessorStillBumps(t *testing.T) {
	terrain := newEmptyTerrain(8, 8)
	burning := featureDef("burning-fail", 0, 0, 10)
	burning.Blocking = true
	burning.FeatureBurntDef = &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "too-large-burnt"},
		Blocking:         true,
		FootprintX:       20,
		FootprintZ:       20,
	}
	terrain.FeatureDefs = []*content.FeatureDef{burning, burning.FeatureBurntDef}
	svc := NewService(terrain, nil, nil, nil)
	inst := svc.PlaceAt(2, 2, burning)
	if inst == nil {
		t.Fatal("burning feature was not placed")
	}
	inst.IsBurning = true
	inst.BurnDuration = 1
	before := terrain.StaticObstacleRevision()
	svc.TickLifecycle(1)
	if got := terrain.StaticObstacleRevision(); got != before+1 {
		t.Fatalf("failed blocking burn successor revision = %d, want %d", got, before+1)
	}
	if svc.InstanceAt(2, 2) != nil {
		t.Fatal("failed blocking successor unexpectedly left an instance")
	}
}
