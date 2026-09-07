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

// gpuProfileWindow keeps the draw-boundary bookkeeping separate from device
// work. It needs one completion draw after the N measured draws so the final
// measured interval has a right endpoint before the one final readback.
type gpuProfileWindow struct {
	requested int
	draws     int
	previous  time.Time
}

func newGPUProfileWindow(requested int) *gpuProfileWindow {
	return &gpuProfileWindow{requested: requested}
}

// observeDraw records the start of one Draw. The bool says whether this draw's
// Execute+screen.DrawImage should contribute a submission sample. The second
// bool and duration describe the completed interval ending at this draw.
func (w *gpuProfileWindow) observeDraw(start time.Time) (measureSubmission bool, cadence time.Duration, hasCadence bool) {
	ordinal := w.draws
	w.draws++
	if ordinal < gpuProfileWarmupFrames {
		return false, 0, false
	}
	if !w.previous.IsZero() {
		cadence, hasCadence = start.Sub(w.previous), true
	}
	w.previous = start
	return ordinal < gpuProfileWarmupFrames+w.requested, cadence, hasCadence
}

func (w *gpuProfileWindow) complete() bool {
	return w.draws >= gpuProfileWarmupFrames+w.requested+1
}

type gpuProfileStats struct {
	submission []time.Duration
	cadence    []time.Duration
}

func newGPUProfileStats(n int) *gpuProfileStats {
	return &gpuProfileStats{
		submission: make([]time.Duration, 0, n),
		cadence:    make([]time.Duration, 0, n),
	}
}

func (s *gpuProfileStats) addSubmission(d time.Duration) {
	s.submission = append(s.submission, d)
}

func (s *gpuProfileStats) addCadence(d time.Duration) {
	if d < 0 {
		// The monotonic clock should make this impossible. Keep the report
		// truthful if a platform clock has unusual behavior.
		d = 0
	}
	s.cadence = append(s.cadence, d)
}

func (s *gpuProfileStats) complete() bool {
	return len(s.submission) == cap(s.submission) && len(s.cadence) == cap(s.cadence)
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
	fmt.Fprintf(os.Stderr, "nanolathe: GPU profile: scene=%q resolution=%dx%d warmup=%d frames=%d submission-samples=%d cadence-samples=%d host=%s/%s backend=%s\n",
		scene, w, h, gpuProfileWarmupFrames, len(stats.submission), len(stats.submission), len(stats.cadence), runtime.GOOS, runtime.GOARCH, debug.GraphicsLibrary)
	fmt.Fprintf(os.Stderr, "  %-18s %.3f ms (one-time; includes %s)\n", "preparation", milliseconds(preparation), preparationLabel)
	fmt.Fprint(os.Stderr, formatGPUProfileMetric("submission (CPU enqueue)", stats.submission))
	fmt.Fprint(os.Stderr, formatGPUProfileMetric("draw cadence (observed)", stats.cadence))
	fmt.Fprintln(os.Stderr, "  policy: vsync disabled, TPS=SyncWithFPS, runnable while unfocused")
	fmt.Fprintln(os.Stderr, "  submission is Execute+screen.DrawImage enqueue; cadence is Draw-start to next Draw-start and includes device backpressure, presentation and host scheduling")
	fmt.Fprintln(os.Stderr, "  screenshot ReadPixels and PNG encoding are excluded; final ReadPixels runs once after samples; frozen-list replay excludes simulation and recurring preparation and is not a universal gameplay FPS claim")
}
