package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"io"
	"strings"
	"testing"
	"time"
)

func TestValidateShotGPUProfileFrames(t *testing.T) {
	if err := validateShotOptions(Options{Shot: "frame.png", Renderer: "modern", ShotRenderer: "both", ShotGPUProfileFrames: 1}); err != nil {
		t.Fatalf("valid GPU profile options rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"negative frames", Options{Shot: "frame.png", ShotRenderer: "modern", ShotGPUProfileFrames: -1}, "nonnegative"},
		{"classic renderer", Options{Shot: "frame.png", Renderer: "classic", ShotRenderer: "modern", ShotGPUProfileFrames: 1}, "requires --renderer=modern"},
		{"modern capture needs modern renderer", Options{Shot: "frame.png", Renderer: "classic", ShotRenderer: "modern"}, "--shot-renderer=modern requires --renderer=modern"},
		{"both capture needs modern renderer", Options{Shot: "frame.png", Renderer: "classic", ShotRenderer: "both"}, "--shot-renderer=both requires --renderer=modern"},
		{"classic capture", Options{Shot: "frame.png", Renderer: "modern", ShotRenderer: "classic", ShotGPUProfileFrames: 1}, "requires --shot-renderer"},
		{"profile seconds modern", Options{Shot: "frame.png", Renderer: "modern", ProfileSeconds: 1}, "--shot-gpu-profile-frames"},
		{"negative threshold", Options{Shot: "frame.png", Renderer: "modern", ShotRenderer: "both", ShotRendererMax: -1}, "nonnegative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateShotOptions(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateShotOptions error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEffectiveShotRendererFollowsRendererWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{name: "modern", opts: Options{Renderer: "modern"}, want: "modern"},
		{name: "classic", opts: Options{Renderer: "classic"}, want: "classic"},
		{name: "unknown renderer retains classic", opts: Options{Renderer: "future"}, want: "classic"},
		{name: "explicit classic", opts: Options{Renderer: "modern", ShotRenderer: "classic"}, want: "classic"},
		{name: "explicit both", opts: Options{Renderer: "modern", ShotRenderer: "both"}, want: "both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveShotRenderer(tc.opts); got != tc.want {
				t.Fatalf("effectiveShotRenderer(%+v) = %q, want %q", tc.opts, got, tc.want)
			}
		})
	}
}

func TestParsedShotRendererDefaultsToPresentationRenderer(t *testing.T) {
	modern, err := parseFlags([]string{"--shot", "frame.png", "--renderer", "modern"}, new(strings.Builder))
	if err != nil {
		t.Fatalf("parse modern shot flags: %v", err)
	}
	if modern.ShotRenderer != "" || effectiveShotRenderer(modern) != "modern" {
		t.Fatalf("modern omitted shot renderer = %q, effective %q; want omitted and modern", modern.ShotRenderer, effectiveShotRenderer(modern))
	}
	classic, err := parseFlags([]string{"--shot", "frame.png", "--renderer", "classic"}, new(strings.Builder))
	if err != nil {
		t.Fatalf("parse classic shot flags: %v", err)
	}
	if classic.ShotRenderer != "" || effectiveShotRenderer(classic) != "classic" {
		t.Fatalf("classic omitted shot renderer = %q, effective %q; want omitted and classic", classic.ShotRenderer, effectiveShotRenderer(classic))
	}
}

func TestValidateShotModelRequiresModernAndValidPose(t *testing.T) {
	valid := Options{Shot: "model.png", ShotModel: "armsolar", Renderer: "modern", ShotRenderer: "modern", ShotModelScale: 2}
	if err := validateShotOptions(valid); err != nil {
		t.Fatalf("valid model preview rejected: %v", err)
	}
	valid.ShotModelPose = "activated"
	if err := validateShotOptions(valid); err != nil {
		t.Fatalf("valid activated model preview rejected: %v", err)
	}
	for _, tc := range []struct {
		opts Options
		want string
	}{
		{Options{Shot: "model.png", ShotModel: "armsolar", Renderer: "classic", ShotModelScale: 2}, "requires --renderer=modern"},
		{Options{Shot: "model.png", ShotModel: "armsolar", Renderer: "modern", ShotModelScale: 8}, "must be 1 or 2"},
		{Options{Shot: "model.png", ShotModel: "armsolar", Renderer: "modern", ShotRenderer: "both", ShotModelScale: 2, ShotGPUProfileFrames: 120}, "battle captures only"},
		{Options{Shot: "model.png", ShotModel: "armsolar", Renderer: "modern", ShotModelScale: 2, ShotModelPose: "unknown"}, "wants \"open\" or \"activated\""},
	} {
		if err := validateShotOptions(tc.opts); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("validateShotOptions(%+v) = %v, want %q", tc.opts, err, tc.want)
		}
	}
}

func TestGPUProfilePercentilesUseNearestRank(t *testing.T) {
	values := []time.Duration{5 * time.Millisecond, 1 * time.Millisecond, 9 * time.Millisecond, 3 * time.Millisecond}
	if got := median(values); got != 4*time.Millisecond {
		t.Fatalf("median = %s, want 4ms", got)
	}
	if got := percentile(values, 0.95); got != 9*time.Millisecond {
		t.Fatalf("p95 = %s, want 9ms", got)
	}
}

func TestGPUProfileWindowHasExactlyMeasuredCadenceSamples(t *testing.T) {
	for _, requested := range []int{1, 3} {
		t.Run(fmt.Sprintf("N=%d", requested), func(t *testing.T) {
			w := newGPUProfileWindow(requested)
			base := time.Unix(100, 0)
			measured, cadence := 0, 0
			var last time.Duration
			for i := 0; i < gpuProfileWarmupFrames+requested+1; i++ {
				submission, interval, hasInterval := w.observeDraw(base.Add(time.Duration(i) * 10 * time.Millisecond))
				if submission {
					measured++
				}
				if hasInterval {
					cadence++
					last = interval
				}
			}
			if measured != requested || cadence != requested {
				t.Fatalf("profile window measured %d submissions and %d cadence intervals, want %d each", measured, cadence, requested)
			}
			if last != 10*time.Millisecond || !w.complete() {
				t.Fatalf("profile window final interval = %s, complete=%v; want 10ms and complete", last, w.complete())
			}
			// Readback/capture happens after this completion draw and is not a
			// draw boundary, so the accounting state has no extra sample.
			if w.draws != gpuProfileWarmupFrames+requested+1 {
				t.Fatalf("final capture changed draw count to %d, want %d", w.draws, gpuProfileWarmupFrames+requested+1)
			}
		})
	}
}

// --zoom is the view scale of DESIGN_GPU_RENDERER §14.6 and the free factor of
// §16.8: the classic executor takes only the three views, the modern one any
// factor in the free range. It is not a capture option — the window path takes
// it too — so it is validated whatever route the run takes, and left unset it
// stays zero for the routes to resolve.
func TestZoomFlagPerExecutor(t *testing.T) {
	for arg, want := range map[string]camera.Zoom{"1": camera.ZoomUnit, "1.5": camera.ZoomOf(camera.ViewScaleMid), "2": camera.ZoomMax} {
		opts, err := parseFlags([]string{"-zoom", arg}, io.Discard)
		if err != nil {
			t.Fatalf("--zoom %s rejected: %v", arg, err)
		}
		if opts.Zoom != want {
			t.Fatalf("--zoom %s parsed as %s", arg, opts.Zoom)
		}
	}
	if opts, err := parseFlags(nil, io.Discard); err != nil || opts.Zoom != 0 {
		t.Fatalf("unset --zoom = %v, %v; want zero and no error", opts.Zoom, err)
	}
	// Classic rejects anything but the three views.
	for _, arg := range []string{"1.25", "0.7", "0.3"} {
		_, err := parseFlags([]string{"-renderer", "classic", "-zoom", arg}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "must be 1 (native), 1.5 or 2 (the detail view)") {
			t.Fatalf("classic --zoom %s error = %v, want the three-view rejection", arg, err)
		}
	}
	// Modern accepts them.
	for arg, want := range map[string]camera.Zoom{"1.25": camera.ZoomUnit * 5 / 4, "0.7": 717, "0.3": 307} {
		opts, err := parseFlags([]string{"-renderer", "modern", "-zoom", arg}, io.Discard)
		if err != nil {
			t.Fatalf("modern --zoom %s rejected: %v", arg, err)
		}
		if opts.Zoom != want {
			t.Fatalf("modern --zoom %s parsed as %d, want %d", arg, opts.Zoom, want)
		}
	}
	// Neither executor takes a factor outside the flag's own range.
	for _, arg := range []string{"0", "3", "-1"} {
		if _, err := parseFlags([]string{"-renderer", "modern", "-zoom", arg}, io.Discard); err == nil {
			t.Fatalf("modern --zoom %s was accepted", arg)
		}
	}
	if _, err := parseFlags([]string{"-shot-zoom", "2"}, io.Discard); err == nil {
		t.Fatal("--shot-zoom is retired and must no longer parse")
	}
}
