package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// A short warm-up absorbs first-use atlas and shader work. It is deliberately
// inside the Ebitengine loop so all device operations use the same context as
// the measured frames [DESIGN_GPU_RENDERER.md §6].
const gpuProfileWarmupFrames = 8

type gpuProfileStats struct {
	submission   []time.Duration
	synchronized []time.Duration
	readback     []time.Duration
}

func newGPUProfileStats(n int) *gpuProfileStats {
	return &gpuProfileStats{
		submission:   make([]time.Duration, 0, n),
		synchronized: make([]time.Duration, 0, n),
		readback:     make([]time.Duration, 0, n),
	}
}

func (s *gpuProfileStats) add(submission, synchronized, readback time.Duration) {
	if readback < 0 {
		// The monotonic clock should make this impossible. Keep the report
		// truthful if a platform clock has unusual behavior.
		readback = 0
	}
	s.submission = append(s.submission, submission)
	s.synchronized = append(s.synchronized, synchronized)
	s.readback = append(s.readback, readback)
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	// Nearest-rank percentile: p95 and p99 are the observations at ceil(p*N).
	index := int(math.Ceil(float64(len(ordered)) * p))
	if index < 1 {
		index = 1
	}
	if index > len(ordered) {
		index = len(ordered)
	}
	return ordered[index-1]
}

func median(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func formatGPUProfileMetric(name string, values []time.Duration) string {
	return fmt.Sprintf("  %-18s median %.3f ms  p95 %.3f ms  p99 %.3f ms\n",
		name, milliseconds(median(values)), milliseconds(percentile(values, 0.95)), milliseconds(percentile(values, 0.99)))
}

func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func printGPUProfile(stats *gpuProfileStats, w, h int, scene string, preparation time.Duration, preparationLabel string) {
	var debug ebiten.DebugInfo
	ebiten.ReadDebugInfo(&debug)
	fmt.Fprintf(os.Stderr, "nanolathe: GPU profile: scene=%q resolution=%dx%d warmup=%d frames=%d host=%s/%s backend=%s\n",
		scene, w, h, gpuProfileWarmupFrames, len(stats.submission), runtime.GOOS, runtime.GOARCH, debug.GraphicsLibrary)
	fmt.Fprintf(os.Stderr, "  %-18s %.3f ms (one-time; includes %s)\n", "preparation", milliseconds(preparation), preparationLabel)
	fmt.Fprint(os.Stderr, formatGPUProfileMetric("submission (CPU enqueue)", stats.submission))
	fmt.Fprint(os.Stderr, formatGPUProfileMetric("Execute+ReadPixels", stats.synchronized))
	fmt.Fprint(os.Stderr, formatGPUProfileMetric("readback wait+transfer", stats.readback))
	fmt.Fprintln(os.Stderr, "  timings are host wall-clock diagnostics; submission is not GPU time, and readback includes synchronization wait plus transfer; this is not normal presentation")
}
