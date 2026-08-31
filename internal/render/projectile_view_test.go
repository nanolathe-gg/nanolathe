package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func testProjectileFrame() *formats.GAFFrame {
	return &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}, Transparent: []bool{false}}
}

func testFrameCount(frame.ProjectileView) (int, bool) { return 10, true }

func testGAF(req ProjectileGAFRequest) (*formats.GAFFrame, bool) {
	return testProjectileFrame(), true
}

func TestDispatchProjectileViewPreservesFamilyTailAndRequiresBeamColor(t *testing.T) {
	v := frame.ProjectileView{
		Handle:     pool.Handle(7),
		X:          numeric.Fixed(9 << 16),
		Y:          numeric.Fixed(3 << 16),
		Z:          numeric.Fixed(12 << 16),
		TailX:      numeric.Fixed(2 << 16),
		TailY:      numeric.Fixed(1 << 16),
		TailZ:      numeric.Fixed(4 << 16),
		RenderType: RenderTypeBeam,
		Graphic:    "peewee-tracer",
		Model:      "peewee.3do",
		PaletteRow: 13, // not a beam color fallback
	}
	d := DispatchProjectileView(v, 20, ProjectileDispatchOptions{})
	if !d.Suppressed {
		t.Fatal("unresolved beam color must suppress draw")
	}
	d = DispatchProjectileView(v, 20, ProjectileDispatchOptions{Color: func(frame.ProjectileView) (int32, int32, bool) { return 4, 5, true }})
	if d.Suppressed || d.Kind != "beam" || d.Color != 4 || d.Color2 != 5 {
		t.Fatalf("resolved beam metadata got %+v", d)
	}
	if d.Head.X != v.X || d.Tail.X != v.TailX || d.Tail.Z != v.TailZ {
		t.Fatalf("head/tail mismatch: %+v", d)
	}
}

func TestDispatchProjectileViewAllAuthoredGAFFamilies(t *testing.T) {
	base := frame.ProjectileView{CreationTick: 100, ExpiryTick: 120, Lifetime: 20, Selector: 2, Model: "peewee.3do"}
	for _, tc := range []struct {
		rt   int32
		kind string
		base bool
	}{
		{RenderTypeBaseSpriteModel, "base-sprite+model", true},
		{RenderTypeGlobalGAF, "global-gaf", false},
		{RenderTypeBaseModelDistinct, "base+model-distinct", true},
		{RenderTypeSelectorGAF, "selector-gaf", false},
		{RenderTypeLifetimeGAF, "lifetime-gaf", false},
		{RenderTypeRecordOrientation, "record-orientation", true},
	} {
		v := base
		v.RenderType = tc.rt
		d := DispatchProjectileView(v, 105, ProjectileDispatchOptions{FrameCount: testFrameCount, ResolveGAF: testGAF})
		if d.Kind != tc.kind || d.Suppressed {
			t.Fatalf("rt %d got %+v", tc.rt, d)
		}
		if tc.base {
			if d.BaseFrame == nil || d.FrameAsset != nil {
				t.Fatalf("rt %d base frame contract got %+v", tc.rt, d)
			}
		} else if tc.rt == RenderTypeSelectorGAF {
			if d.BaseFrame == nil || d.FrameAsset == nil {
				t.Fatalf("rt %d shadow+selected frame contract got %+v", tc.rt, d)
			}
		} else if d.FrameAsset == nil || d.BaseFrame != nil {
			t.Fatalf("rt %d selected frame contract got %+v", tc.rt, d)
		}
	}
}

func TestDispatchProjectileViewMissingAuthoredDataSuppresses(t *testing.T) {
	base := frame.ProjectileView{Model: "peewee.3do", Selector: 2, CreationTick: 100, ExpiryTick: 120, Lifetime: 20}
	missing := ProjectileDispatchOptions{FrameCount: testFrameCount, ResolveGAF: func(ProjectileGAFRequest) (*formats.GAFFrame, bool) { return nil, false }}
	for _, rt := range []int32{RenderTypeBaseSpriteModel, RenderTypeGlobalGAF, RenderTypeBaseModelDistinct, RenderTypeSelectorGAF, RenderTypeLifetimeGAF, RenderTypeRecordOrientation} {
		v := base
		v.RenderType = rt
		if d := DispatchProjectileView(v, 105, missing); !d.Suppressed {
			t.Fatalf("rt %d missing frame must suppress: %+v", rt, d)
		}
	}
	unknownSelector := base
	unknownSelector.RenderType = RenderTypeSelectorGAF
	unknownSelector.Selector = 0 // publisher's unresolved zero is not enough
	if d := DispatchProjectileView(unknownSelector, 105, ProjectileDispatchOptions{ResolveGAF: testGAF}); !d.Suppressed {
		t.Fatal("missing frame-count validity must suppress selector family")
	}
	unknownLifetime := base
	unknownLifetime.RenderType = RenderTypeLifetimeGAF
	unknownLifetime.Lifetime = 0
	if d := DispatchProjectileView(unknownLifetime, 105, ProjectileDispatchOptions{FrameCount: testFrameCount, ResolveGAF: testGAF}); !d.Suppressed {
		t.Fatal("missing lifetime must suppress lifetime family")
	}
}

func TestDispatchProjectileViewFrameFamilies(t *testing.T) {
	base := frame.ProjectileView{CreationTick: 100, ExpiryTick: 120, Lifetime: 20, Selector: 2}
	selector := base
	selector.RenderType = RenderTypeSelectorGAF
	d := DispatchProjectileView(selector, 105, ProjectileDispatchOptions{FrameCount: testFrameCount, ResolveGAF: testGAF})
	if d.Frame != 5 || d.Suppressed {
		t.Fatalf("selector frame got %+v", d)
	}
	lifetime := base
	lifetime.RenderType = RenderTypeLifetimeGAF
	d = DispatchProjectileView(lifetime, 105, ProjectileDispatchOptions{FrameCount: testFrameCount, ResolveGAF: testGAF})
	if d.Frame != 3 || d.Suppressed {
		t.Fatalf("lifetime frame got %+v want 3 [03 §5.4]", d)
	}
}

func TestDispatchProjectileViewAbsentSelectorStaysSuppressed(t *testing.T) {
	v := frame.ProjectileView{
		RenderType:       RenderTypeSelectorGAF,
		Selector:         -1,
		SelectorSequence: 0, // zero value is not an authoritative presence bit
		FrameCount:       10,
	}
	if d := DispatchProjectileView(v, 5, ProjectileDispatchOptions{ResolveGAF: testGAF}); !d.Suppressed {
		t.Fatalf("absent selector was inferred from zero-valued alias: %+v", d)
	}
}

func TestSnapshotSegmentedPointPassesUseBothDeterministicStreams(t *testing.T) {
	v := frame.ProjectileView{X: numeric.Fixed(20 << 16), TailX: 0}
	first, second, ok := SnapshotSegmentedPointPasses(v, funcCRT(7))
	if !ok || len(first) < 2 || len(second) < 2 {
		t.Fatalf("segmented passes missing: first=%d second=%d ok=%v", len(first), len(second), ok)
	}
	if first[0] != second[0] || first[len(first)-1] != second[len(second)-1] {
		t.Fatal("segmented passes must preserve shared endpoints")
	}
	if first[1] == second[1] {
		t.Fatal("segmented passes must consume distinct CRT jitter")
	}
	v.RenderType = RenderTypeSegmented
	d := DispatchProjectileView(v, 1, ProjectileDispatchOptions{
		Color: func(frame.ProjectileView) (int32, int32, bool) { return 1, 2, true },
		SegmentPoints: func(frame.ProjectileView) ([]ProjectilePoint, []ProjectilePoint, bool) {
			return first, second, true
		},
	})
	if d.Suppressed || len(d.Segments) != len(first) || len(d.Segments2) != len(second) {
		t.Fatalf("two-pass dispatch got %+v", d)
	}
}

func funcCRT(seed uint32) *rng.CRT {
	crt := rng.NewCRT(seed)
	return &crt
}

func TestBuildProjectileDrawsVisibilityAndGlobalAbort(t *testing.T) {
	views := []frame.ProjectileView{
		{Handle: pool.Handle(1), RenderType: RenderTypeBeam},
		{Handle: pool.Handle(2), RenderType: RenderTypeGlobalGAF},
		{Handle: pool.Handle(3), RenderType: RenderTypeBeam},
	}
	opts := ProjectileDispatchOptions{Color: func(frame.ProjectileView) (int32, int32, bool) { return 1, 0, true }, ResolveGAF: testGAF}
	draws, aborted := BuildProjectileDraws(views, 1, func(v frame.ProjectileView) bool { return v.Handle != pool.Handle(3) }, func(v frame.ProjectileView) bool { return false }, opts)
	if !aborted || len(draws) != 1 || draws[0].Handle != 1 {
		t.Fatalf("visibility/abort got draws=%+v aborted=%v", draws, aborted)
	}
}

// TestBuildProjectileDrawsNilVisibilityFailsClosed verifies that an absent
// visibility callback emits no projectile draw specs [03 §5.4].
func TestBuildProjectileDrawsNilVisibilityFailsClosed(t *testing.T) {
	views := []frame.ProjectileView{{Handle: pool.Handle(1), RenderType: RenderTypeBeam}}
	opts := ProjectileDispatchOptions{Color: func(frame.ProjectileView) (int32, int32, bool) { return 1, 0, true }}
	draws, aborted := BuildProjectileDraws(views, 1, nil, nil, opts)
	if aborted || len(draws) != 0 {
		t.Fatalf("nil visibility must fail closed: draws=%v aborted=%v", draws, aborted)
	}
}
