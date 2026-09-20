package film

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func mustParse(t *testing.T, text string) *Script {
	t.Helper()
	s, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

// A presented frame must land on an exact k/n blend fraction: the whole point
// of a film capture is that no fraction comes from a wall clock
// (DESIGN_GPU_RENDERER §13.5).
func TestTimelineFractionsAreExactTickDivisions(t *testing.T) {
	s := mustParse(t, `{"fps":120,"shots":[{"ticks":2},{"ticks":1}]}`)
	if s.FramesPerTick() != 4 {
		t.Fatalf("frames per tick = %d, want 4", s.FramesPerTick())
	}
	if s.TotalFrames() != 12 {
		t.Fatalf("total frames = %d, want 12", s.TotalFrames())
	}
	want := []struct {
		shot, tick, phase int
		fraction          float64
		cut               bool
	}{
		{0, 0, 0, 0.00, true}, {0, 0, 1, 0.25, false}, {0, 0, 2, 0.50, false}, {0, 0, 3, 0.75, false},
		{0, 1, 0, 0.00, false}, {0, 1, 1, 0.25, false}, {0, 1, 2, 0.50, false}, {0, 1, 3, 0.75, false},
		{1, 0, 0, 0.00, true}, {1, 0, 1, 0.25, false}, {1, 0, 2, 0.50, false}, {1, 0, 3, 0.75, false},
	}
	for frame, w := range want {
		got, ok := s.At(frame)
		if !ok {
			t.Fatalf("frame %d is outside the timeline", frame)
		}
		if got.Shot != w.shot || got.ShotTick != w.tick || got.Phase != w.phase || got.Fraction != w.fraction || got.Cut != w.cut {
			t.Fatalf("frame %d = %+v, want shot %d tick %d phase %d fraction %v cut %v",
				frame, got, w.shot, w.tick, w.phase, w.fraction, w.cut)
		}
	}
	if _, ok := s.At(12); ok {
		t.Fatal("a frame past the end resolved")
	}
}

// Only a whole multiple of the authoritative rate divides a tick evenly, and a
// script that asks for anything else is rejected rather than rounded.
func TestCaptureRateMustDivideTheTick(t *testing.T) {
	_, err := Parse([]byte(`{"fps":50,"shots":[{"ticks":1}]}`))
	var problems *ScriptError
	if !errors.As(err, &problems) {
		t.Fatalf("error = %v, want a script error", err)
	}
	if !strings.Contains(err.Error(), "multiple of the 30 Hz") {
		t.Fatalf("error %q does not name the tick rate", err)
	}
}

// Validation reports every problem at once: a re-render costs minutes.
func TestValidationReportsEveryProblem(t *testing.T) {
	_, err := Parse([]byte(`{"fps":45,"scene":{"kind":"siege"},"shots":[
		{"name":"a","ticks":0,"camera":[{"tick":3,"ease":"bounce"}],
		 "text":[{"at":1,"ticks":5,"style":"crawl","lines":["X"]}]}]}`))
	var problems *ScriptError
	if !errors.As(err, &problems) {
		t.Fatalf("error = %v, want a script error", err)
	}
	for _, want := range []string{"multiple of the 30 Hz", "siege", "no duration", "bounce", "crawl"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q:\n%s", want, err)
		}
	}
}

// An unknown field is a typo in a hand-written script, and a silently ignored
// camera key is a whole wasted render.
func TestUnknownScriptFieldIsRejected(t *testing.T) {
	if _, err := Parse([]byte(`{"fps":60,"zoom":2,"shots":[{"ticks":1}]}`)); err == nil {
		t.Fatal("an unknown field parsed")
	}
}

func TestCameraTrackHoldsOutsideItsKeysAndEasesBetweenThem(t *testing.T) {
	s := mustParse(t, `{"shots":[{"ticks":60,"camera":[
		{"tick":10,"x":100,"z":-50,"zoom":1},
		{"tick":50,"x":300,"z":50,"zoom":2,"ease":"linear"}]}]}`)
	shot := &s.Shots[0]

	x, z, zoom, world := shot.CameraAt(0)
	if x != 100 || z != -50 || zoom != 1 || world {
		t.Fatalf("before the first key = (%v,%v,%v,%v), want the first key held", x, z, zoom, world)
	}
	if x, z, zoom, _ = shot.CameraAt(60); x != 300 || z != 50 || zoom != 2 {
		t.Fatalf("after the last key = (%v,%v,%v), want the last key held", x, z, zoom)
	}
	// Linear on the position, geometric on the zoom: half the ticks is half
	// the distance but the square root of the magnification ratio.
	x, z, zoom, _ = shot.CameraAt(30)
	if x != 200 || z != 0 {
		t.Fatalf("midpoint position = (%v,%v), want (200,0)", x, z)
	}
	if math.Abs(zoom-math.Sqrt2) > 1e-9 {
		t.Fatalf("midpoint zoom = %v, want sqrt(2)", zoom)
	}
}

// Keys may be authored out of order; the track sorts them.
func TestCameraKeysSortByTick(t *testing.T) {
	s := mustParse(t, `{"shots":[{"ticks":20,"camera":[{"tick":20,"x":10},{"tick":0,"x":0}]}]}`)
	if x, _, _, _ := s.Shots[0].CameraAt(0); x != 0 {
		t.Fatalf("first key x = %v, want 0", x)
	}
}

// A clean capture composes the chrome and writes only the viewport inside it,
// so the written frame is exactly the scripted size.
func TestCleanCaptureGrowsTheSurfaceByTheChrome(t *testing.T) {
	s := mustParse(t, `{"width":1920,"height":1080,"clean":true,"shots":[{"ticks":1}]}`)
	w, h := s.Surface()
	if w != 1920+ChromeInsetX || h != 1080+2*ChromeInsetY {
		t.Fatalf("clean surface = %dx%d, want the frame plus the chrome", w, h)
	}
	if x, y := s.CropOrigin(); x != ChromeInsetX || y != ChromeInsetY {
		t.Fatalf("crop origin = %d,%d, want the chrome insets", x, y)
	}
	plain := mustParse(t, `{"width":1920,"height":1080,"shots":[{"ticks":1}]}`)
	if w, h := plain.Surface(); w != 1920 || h != 1080 {
		t.Fatalf("a capture that keeps its interface composes %dx%d, want the frame size", w, h)
	}
}

func TestSceneOverridesDefaultIndependentlyAndOnlyLoadAtCuts(t *testing.T) {
	s := mustParse(t, `{"scene":{"kind":"skirmish","map":"original","seed":7,"pre_ticks":40,"per_side":8,"buildings":4,"factories":true,"fog":true},"shots":[{"ticks":1},{"ticks":1,"scene":{"map":"next"}},{"ticks":1}]}`)
	next := s.Shots[1].Scene
	if next.Kind != "battle" || next.PerSide != 120 || next.Buildings != 12 || next.Map != "next" || next.Seed != 0 || next.PreTicks != 0 || next.Factories || next.Fog {
		t.Fatalf("override inherited previous scene instead of defaults: %+v", next)
	}
	for i, want := range []*Scene{&s.Scene, nil, next, nil, nil, nil} {
		cursor, _ := s.At(i)
		if got := s.SceneAtCut(cursor); got != want {
			t.Fatalf("frame %d new scene = %p, want %p", i, got, want)
		}
	}
	first := mustParse(t, `{"shots":[{"ticks":1,"scene":{"map":"first","roster":"air"}}]}`)
	cursor, _ := first.At(0)
	if first.SceneAtCut(cursor) != first.Shots[0].Scene || first.Shots[0].Scene.Buildings != 0 {
		t.Fatal("first-shot override must replace the initial scene and air must default to no buildings")
	}
}

func TestSceneOverrideValidationNamesTheShot(t *testing.T) {
	_, err := Parse([]byte(`{"shots":[{"name":"bad-cut","ticks":1,"scene":{"kind":"unknown","roster":"orbital","pre_ticks":-1,"anchor":[1]}}]}`))
	for _, want := range []string{"bad-cut", "unknown", "orbital", "negative", "exactly two"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
	for _, scene := range []string{`{"roster":"air","buildings":1}`, `{"roster":"air","factories":true}`, `{"anchor":[]}`} {
		if _, err := Parse([]byte(`{"shots":[{"ticks":1,"scene":` + scene + `}]}`)); err == nil {
			t.Fatalf("invalid scene accepted: %s", scene)
		}
	}
	if _, err := Parse([]byte(`{"shots":[{"ticks":1,"scene":{"mapp":"typo"}}]}`)); err == nil {
		t.Fatal("unknown override field accepted")
	}
}
