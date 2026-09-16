package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
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

// TestDispatchProjectileViewCoversEveryRendertype walks the whole dispatch
// table 0..7 with every authored input supplied and names the branch each
// rendertype takes [03 §5.4] C6. A rendertype outside the table stays
// suppressed rather than resolving anything [I9].
func TestDispatchProjectileViewCoversEveryRendertype(t *testing.T) {
	wantKind := map[int32]string{
		RenderTypeBeam:              "beam",
		RenderTypeBaseSpriteModel:   "base-sprite+model",
		RenderTypeGlobalGAF:         "lens",
		RenderTypeBaseModelDistinct: "base+model-distinct",
		RenderTypeSelectorGAF:       "selector-gaf",
		RenderTypeLifetimeGAF:       "lifetime-gaf",
		RenderTypeRecordOrientation: "record-orientation",
		RenderTypeSegmented:         "segmented",
	}
	if len(wantKind) != RendertypeCount {
		t.Fatalf("dispatch table covers %d rendertypes, want %d [03 §5.4] C6", len(wantKind), RendertypeCount)
	}
	segment := []ProjectilePoint{{}, {X: numeric.Fixed(1 << 16)}}
	opts := ProjectileDispatchOptions{
		FrameCount: testFrameCount,
		ResolveGAF: testGAF,
		Color:      func(frame.ProjectileView) (int32, int32, bool) { return 7, 2, true },
		SegmentPoints: func(frame.ProjectileView) ([]ProjectilePoint, []ProjectilePoint, bool) {
			return segment, segment, true
		},
	}
	for rt := int32(0); rt < RendertypeCount; rt++ {
		v := frame.ProjectileView{
			RenderType:   rt,
			Model:        "peewee.3do",
			Selector:     2,
			CreationTick: 100,
			ExpiryTick:   120,
			Lifetime:     20,
		}
		d := DispatchProjectileView(v, 105, opts)
		if d.Suppressed {
			t.Fatalf("rendertype %d suppressed with every input supplied [03 §5.4] C6", rt)
		}
		if d.Kind != wantKind[rt] {
			t.Fatalf("rendertype %d kind %q, want %q [03 §5.4] C6", rt, d.Kind, wantKind[rt])
		}
		if d.RenderType != rt {
			t.Fatalf("rendertype %d published as %d", rt, d.RenderType)
		}
	}
	unknown := frame.ProjectileView{RenderType: RendertypeCount, Model: "peewee.3do", Selector: 2, Lifetime: 20}
	if d := DispatchProjectileView(unknown, 105, opts); !d.Suppressed || d.Kind != "unknown" {
		t.Fatalf("rendertype %d off the table got %+v, want an unknown suppression [I9]", RendertypeCount, d)
	}
}

// The lifetime family draws only while 0 <= frame < frameCount [03 §5.4]: a
// record whose expiry has arrived produces frame == frameCount and is dropped.
func TestDispatchProjectileViewLifetimeOutOfRangeSuppresses(t *testing.T) {
	v := frame.ProjectileView{RenderType: RenderTypeLifetimeGAF, Lifetime: 10, CreationTick: 100, ExpiryTick: 105}
	opts := ProjectileDispatchOptions{FrameCount: testFrameCount, ResolveGAF: testGAF}
	if d := DispatchProjectileView(v, 105, opts); !d.Suppressed {
		t.Fatalf("expired lifetime record drew frame %d, want suppression [03 §5.4]", d.Frame)
	}
	// One tick of life left keeps it inside the range.
	v.ExpiryTick = 106
	if d := DispatchProjectileView(v, 105, opts); d.Suppressed || d.Frame != 9 {
		t.Fatalf("live lifetime record got %+v, want frame 9 [03 §5.4]", d)
	}
}

// The draw gate runs once per record before dispatch, and a rejected record
// leaves the walk without touching art [03 §5.4].
func TestBuildProjectileDrawsVisibilityGateCullsPerRecord(t *testing.T) {
	views := []frame.ProjectileView{
		{Handle: pool.Handle(1), RenderType: RenderTypeBeam},
		{Handle: pool.Handle(2), RenderType: RenderTypeBeam},
	}
	calls := 0
	visible := func(v frame.ProjectileView) bool {
		calls++
		return v.Handle == pool.Handle(1)
	}
	opts := ProjectileDispatchOptions{Color: func(frame.ProjectileView) (int32, int32, bool) { return 1, 0, true }}
	draws, aborted := BuildProjectileDrawsInto(nil, views, 10, visible, nil, opts)
	if aborted {
		t.Fatal("visibility culling must not abort the batch")
	}
	if calls != 2 {
		t.Fatalf("draw gate ran %d times, want once per record [03 §5.4]", calls)
	}
	if len(draws) != 1 || draws[0].Handle != 1 {
		t.Fatalf("visibility gate produced %+v, want only the visible record [03 §5.4]", draws)
	}
}

func TestDispatchProjectileViewMissingAuthoredDataSuppresses(t *testing.T) {
	base := frame.ProjectileView{Model: "peewee.3do", Selector: 2, CreationTick: 100, ExpiryTick: 120, Lifetime: 20}
	missing := ProjectileDispatchOptions{FrameCount: testFrameCount, ResolveGAF: func(ProjectileGAFRequest) (*formats.GAFFrame, bool) { return nil, false }}
	for _, rt := range []int32{RenderTypeBaseSpriteModel, RenderTypeBaseModelDistinct, RenderTypeSelectorGAF, RenderTypeLifetimeGAF, RenderTypeRecordOrientation} {
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
	crt := funcCRT(7)
	first, second, ok := SnapshotSegmentedPointPasses(v, crt)
	if !ok || len(first) < 2 || len(second) < 2 {
		t.Fatalf("segmented passes missing: first=%d second=%d ok=%v", len(first), len(second), ok)
	}
	if first[0] != second[0] || first[0] != (ProjectilePoint{}) {
		t.Fatal("each segmented pass must start at the unjittered tail [06 R-WFX-01 §4]")
	}
	if first[1] == second[1] {
		t.Fatal("segmented passes must consume distinct CRT jitter")
	}
	if crt.Draws() != 24 { // two passes × four points × three axes
		t.Fatalf("two passes consumed %d CRT draws, want 24 [06 R-WFX-01 §4]", crt.Draws())
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
	draws, aborted := BuildProjectileDrawsInto(nil, views, 1, func(v frame.ProjectileView) bool { return v.Handle != pool.Handle(3) }, func(v frame.ProjectileView) bool { return false }, opts)
	if !aborted || len(draws) != 1 || draws[0].Handle != 1 {
		t.Fatalf("visibility/abort got draws=%+v aborted=%v", draws, aborted)
	}
}

// TestBuildProjectileDrawsNilVisibilityFailsClosed verifies that an absent
// visibility callback emits no projectile draw specs [03 §5.4].
func TestBuildProjectileDrawsNilVisibilityFailsClosed(t *testing.T) {
	views := []frame.ProjectileView{{Handle: pool.Handle(1), RenderType: RenderTypeBeam}}
	opts := ProjectileDispatchOptions{Color: func(frame.ProjectileView) (int32, int32, bool) { return 1, 0, true }}
	draws, aborted := BuildProjectileDrawsInto(nil, views, 1, nil, nil, opts)
	if aborted || len(draws) != 0 {
		t.Fatalf("nil visibility must fail closed: draws=%v aborted=%v", draws, aborted)
	}
}

func TestLensDispatchNeedsNoArtOrAge(t *testing.T) {
	for _, now := range []uint32{0, 100, 999} {
		d := DispatchProjectileView(frame.ProjectileView{Handle: 1, RenderType: RenderTypeGlobalGAF}, now, ProjectileDispatchOptions{ResolveGAF: func(ProjectileGAFRequest) (*formats.GAFFrame, bool) {
			t.Fatal("lens looked up GAF art")
			return nil, false
		}})
		if d.Suppressed || d.Kind != "lens" || d.FrameAsset != nil {
			t.Fatalf("lens dispatch: %+v", d)
		}
	}
}

// [06 R-WFX-01 §4] Scheduler roots never reach visibility, lens admission,
// art resolution or the presentation CRT; emitted shots keep pool order.
func TestBuildProjectileDrawsSkipsBurstSchedulersBeforeCallbacks(t *testing.T) {
	views := []frame.ProjectileView{
		{Handle: 1, RenderType: RenderTypeSegmented, BurstRemaining: 2},
		{Handle: 2, RenderType: RenderTypeGlobalGAF, BurstRemaining: -1},
		{Handle: 3, RenderType: RenderTypeBeam, HasPrimaryColor: true, PrimaryColor: 5},
	}
	visibilityCalls := 0
	visible := func(v frame.ProjectileView) bool {
		visibilityCalls++
		if v.Handle != 3 {
			t.Fatalf("scheduler reached visibility: %+v", v)
		}
		return true
	}
	options := ProjectileDispatchOptions{
		SegmentPoints: func(frame.ProjectileView) ([]ProjectilePoint, []ProjectilePoint, bool) {
			t.Fatal("scheduler consumed segment work")
			return nil, nil, false
		},
		ResolveGAF: func(ProjectileGAFRequest) (*formats.GAFFrame, bool) {
			t.Fatal("scheduler resolved art")
			return nil, false
		},
	}
	draws, aborted := BuildProjectileDrawsInto(nil, views, 10, visible, func(frame.ProjectileView) bool {
		t.Fatal("scheduler reached lens admission")
		return false
	}, options)
	if aborted || visibilityCalls != 1 || len(draws) != 1 || draws[0].Handle != 3 {
		t.Fatalf("draws=%+v aborted=%v visibilityCalls=%d", draws, aborted, visibilityCalls)
	}
}
