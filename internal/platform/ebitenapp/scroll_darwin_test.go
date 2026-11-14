//go:build darwin && !ebitenginevmguest

package ebitenapp

import (
	"math"
	"runtime"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Exercise real AppKit decoding and the native adapter: collector-only tests
// cannot catch confusing a scrolling distance with a wheel count (§16.6 of
// DESIGN_GPU_RENDERER). These are authored events, not physical-device traces.
func TestNativeWheelCountsIgnoreLineDistance(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	lib, err := purego.Dlopen(coreGraphicsLibrary, purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(lib)
	var create func(uintptr, uint32, uint32, int32, int32, int32) uintptr
	var setDouble func(uintptr, uint32, float64)
	var setInteger func(uintptr, uint32, int64)
	var integerValue func(uintptr, uint32) int64
	var release func(uintptr)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateScrollWheelEvent2")
	purego.RegisterLibFunc(&setDouble, lib, "CGEventSetDoubleValueField")
	purego.RegisterLibFunc(&setInteger, lib, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&integerValue, lib, "CGEventGetIntegerValueField")
	purego.RegisterLibFunc(&release, lib, "CFRelease")
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	nativeScroll.setActive(true)
	defer nativeScroll.setActive(false)
	const (
		lineUnits     = 1 // kCGScrollEventUnitLine from CGEventTypes.h
		fixedDeltaY   = 93
		pointDeltaY   = 96
		isContinuous  = 88
		momentumPhase = 123
	)
	add := func(count int32, distance float64, precise, momentum bool) float64 {
		t.Helper()
		cg := create(0, lineUnits, 1, count, 0, 0)
		if cg == 0 {
			t.Fatal("create scroll event returned nil")
		}
		defer release(cg)
		setDouble(cg, fixedDeltaY, distance)
		if precise {
			setInteger(cg, isContinuous, 1)
			setInteger(cg, pointDeltaY, int64(distance))
		}
		if momentum {
			setInteger(cg, momentumPhase, 1)
		}
		event := objc.ID(objc.GetClass("NSEvent")).Send(objc.RegisterName("eventWithCGEvent:"), cg)
		if event == 0 {
			t.Fatal("decode scroll event returned nil")
		}
		if got := objc.Send[bool](event, objc.RegisterName("hasPreciseScrollingDeltas")); got != precise {
			t.Fatalf("precise=%v, want %v", got, precise)
		}
		if got := objc.Send[uint64](event, objc.RegisterName("momentumPhase")) != 0; got != momentum {
			t.Fatalf("momentum=%v, want %v", got, momentum)
		}
		if got := integerValue(uintptr(event.Send(objc.RegisterName("CGEvent"))), scrollWheelDeltaY); got != int64(count) {
			t.Fatalf("native count=%d, want %d", got, count)
		}
		raw := objc.Send[float64](event, objc.RegisterName("scrollingDeltaY"))
		if math.Abs(raw-distance) > 0.00002 {
			t.Fatalf("native distance=%g, want %g", raw, distance)
		}
		recordNativeScroll(event, integerValue)
		return raw
	}
	poll := func() *input.MouseState {
		var sample sampledInput
		sample.applyScroll(nativeScroll.take(99, 99)) // Ebitengine's copy must not replay.
		var buffer hostInputBuffer
		buffer.add(sample)
		buffer.add(sampledInput{}) // An idle refresh must preserve the accepted event.
		in := input.NewState()
		applyInput(in, buffer.take())
		return input.StateFromSample(input.SampleFromState(in, 0, 1024, 768)).Mouse
	}
	for _, direction := range []int32{-1, 1} {
		cam := &camera.Camera{ViewW: 1024, ViewH: 768, MapW: 16384, MapH: 16384}
		var zoom camera.ZoomController
		wants := []camera.Zoom{1280, 1600, 2000}
		if direction < 0 {
			wants = []camera.Zoom{819, 655, 524}
		}
		for i, distance := range []float64{0.1, 0.1, 4} {
			raw := add(direction, distance*float64(direction), false, false)
			mouse := poll()
			if mouse.ZoomScrollY != float32(direction) || mouse.ScrollY != float32(raw) || mouse.PanX != 0 || mouse.PanY != 0 {
				t.Fatalf("distance=%g count=%d: mouse=%+v", raw, direction, mouse)
			}
			// Slow clicks used to lose each fraction after the 180ms quiet gap.
			zoom.Wheel(cam, 500, 300, float64(mouse.ZoomScrollY), uint32(i)*220)
			if got := zoom.Target(cam); got != wants[i] {
				t.Fatalf("click %d direction %d: target=%d, want %d", i, direction, got, wants[i])
			}
			for range 32 {
				zoom.Step(cam)
			}
		}
	}
	// Counts survive both a multi-tick native event and batching separate clicks.
	for _, count := range []int32{-3, 3} {
		add(count, 0.1*float64(count), false, false)
		if got := poll().ZoomScrollY; got != float32(count) {
			t.Fatalf("multiple ticks=%g, want %d", got, count)
		}
	}
	// AppKit's nonzero distance keeps the user's scrolling direction even
	// when an authored event gives the integer count a different sign.
	for _, tc := range []struct {
		count    int32
		distance float64
		want     float32
	}{
		{1, -0.1, -1}, {-1, 4, 1}, {3, -4, -3}, {-3, 0.1, 3},
	} {
		add(tc.count, tc.distance, false, false)
		if got := poll().ZoomScrollY; got != tc.want {
			t.Fatalf("count=%d distance=%g: direction=%g, want %g", tc.count, tc.distance, got, tc.want)
		}
	}
	raw := add(1, 0.1, false, false) + add(1, 4, false, false)
	if mouse := poll(); mouse.ZoomScrollY != 2 || mouse.ScrollY != float32(raw) {
		t.Fatalf("batched clicks=%+v", mouse)
	}
	// A driver without an integer count still supplies one deliberate notch.
	for _, distance := range []float64{-4, -0.1, 0, 0.1, 4} {
		add(0, distance, false, false)
		want := float32(0)
		if distance > 0 {
			want = 1
		} else if distance < 0 {
			want = -1
		}
		if got := poll().ZoomScrollY; got != want {
			t.Fatalf("absent count distance=%g: zoom=%g, want %g", distance, got, want)
		}
	}
	for _, precise := range []bool{false, true} {
		for _, momentum := range []bool{false, true} {
			if !precise && !momentum {
				continue
			}
			raw := add(3, 4, precise, momentum)
			mouse := poll()
			wantPan, wantGUI := float64(0), float32(raw)
			if precise {
				wantGUI = float32(raw * 0.1)
				if !momentum {
					wantPan = raw
				}
			}
			if mouse.ZoomScrollY != 0 || mouse.PanY != wantPan || mouse.ScrollY != wantGUI {
				t.Fatalf("precise=%v momentum=%v: mouse=%+v", precise, momentum, mouse)
			}
		}
	}
	if mouse := poll(); mouse.Scrolled() || mouse.ZoomScrollY != 0 || mouse.PanY != 0 {
		t.Fatalf("idle poll replayed scrolling: %+v", mouse)
	}
}

// A wheel turned with Shift held arrives on the horizontal axis; its notch
// count must survive as ZoomScrollX without zooming. Authored events only.
func TestNativeHorizontalWheelCount(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	lib, err := purego.Dlopen(coreGraphicsLibrary, purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(lib)
	var create func(uintptr, uint32, uint32, int32, int32, int32) uintptr
	var setDouble func(uintptr, uint32, float64)
	var integerValue func(uintptr, uint32) int64
	var release func(uintptr)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateScrollWheelEvent2")
	purego.RegisterLibFunc(&setDouble, lib, "CGEventSetDoubleValueField")
	purego.RegisterLibFunc(&integerValue, lib, "CGEventGetIntegerValueField")
	purego.RegisterLibFunc(&release, lib, "CFRelease")
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	nativeScroll.setActive(true)
	defer nativeScroll.setActive(false)
	const (
		lineUnits   = 1  // kCGScrollEventUnitLine
		fixedDeltaX = 94 // kCGScrollWheelEventFixedPtDeltaAxis2
	)
	for _, direction := range []int32{-1, 1} {
		cg := create(0, lineUnits, 2, 0, direction, 0)
		if cg == 0 {
			t.Fatal("create scroll event returned nil")
		}
		setDouble(cg, fixedDeltaX, 0.1*float64(direction))
		event := objc.ID(objc.GetClass("NSEvent")).Send(objc.RegisterName("eventWithCGEvent:"), cg)
		if event == 0 {
			t.Fatal("decode scroll event returned nil")
		}
		recordNativeScroll(event, integerValue)
		release(cg)
		var sample sampledInput
		sample.applyScroll(nativeScroll.take(99, 99))
		var buffer hostInputBuffer
		buffer.add(sample)
		in := input.NewState()
		applyInput(in, buffer.take())
		mouse := input.StateFromSample(input.SampleFromState(in, 0, 1024, 768)).Mouse
		if mouse.ZoomScrollX != float32(direction) || mouse.ZoomScrollY != 0 || mouse.PanX != 0 {
			t.Fatalf("direction %d: mouse=%+v", direction, mouse)
		}
	}
}
