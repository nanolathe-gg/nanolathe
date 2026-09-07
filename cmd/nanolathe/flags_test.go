package main

import (
	"fmt"
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
	classic, err := parseFlags([]string{"--shot", "frame.png"}, new(strings.Builder))
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
		{Options{Shot: "model.png", ShotModel: "armsolar", Renderer: "modern", ShotModelScale: 8}, "0.25..4"},
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
