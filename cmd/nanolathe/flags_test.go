package main

import (
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
		{"classic capture", Options{Shot: "frame.png", Renderer: "modern", ShotRenderer: "classic", ShotGPUProfileFrames: 1}, "requires --shot-renderer"},
		{"negative threshold", Options{Shot: "frame.png", ShotRenderer: "both", ShotRendererMax: -1}, "nonnegative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateShotOptions(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateShotOptions error = %v, want %q", err, tc.want)
			}
		})
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
