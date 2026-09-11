package ebitenapp

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
)

// BenchmarkOptions is host-only configuration; it never changes tick arithmetic.
type BenchmarkOptions struct {
	Directory, Renderer string
	Frames              int
	// TPS is the target presentation rate: 30, 60 or 120 draws per second.
	// One authoritative tick spans TPS/30 draws. Modern presents fractions
	// 0..(TPS/30-1)/(TPS/30); classic keeps committed-tick sampling [I6].
	TPS      int
	Metadata map[string]any
	// Diagnostic snapshots run outside the measured profile/counter window.
	BeforeMeasure, AfterMeasure func() error
}
type benchmarkRow struct {
	Frame int
	// Cadence is start-of-work to start-of-work, before simulation. The first
	// measured interval is invalid because profile setup separates it from warmup.
	CadenceValid bool
	// These are host wall times, not GPU timestamps. OutsideDraw includes
	// Ebitengine's deferred flush, queue/display waits and callback scheduling.
	// PaceWait is our intentional deadline sleep; DrawWork is the complete host
	// callback work after it, including the census and pre-record launch.
	PaceWait, OutsideDraw, DrawWork float64
	SimulationStep                  bool
	TickPhase                       int
	// Record is the recording's GAME-GOROUTINE cost: the wait to join an
	// outstanding pre-record plus any synchronous re-record. With the
	// record/submit pipeline off, or on a frame it declined, that is the whole
	// record as before (docs/DESIGN_GPU_RENDERER.md §13.10).
	Step, Record, Submit, Cadence    float64 // milliseconds; Submit is CPU submission, not GPU completion
	CPUCompose, CPUReplay, CPURender float64
	// PreRecord is the wall time the consumed pre-record spent on the pipeline
	// goroutine, overlapped with the previous frame's flush and present, and
	// Hit says this frame presented a pre-recorded list (§13.10).
	PreRecord float64
	Hit       bool
	Stats     gpurender.ModelStats
	Census    any
}
type battleBenchmark struct {
	c               *client.Client
	step            func()
	census          func() any
	options         BenchmarkOptions
	gpu             *gpurender.Renderer
	img             *ebiten.Image
	rows            []benchmarkRow
	frame           int
	done            bool
	pacer           benchmarkPacer
	lastEnd         time.Time
	err             error
	last            time.Time
	cpu             *os.File
	mem             runtime.MemStats
	gpuMemoryBefore int64
}

// benchmarkSimulationTPS is the fixed authoritative cadence. Wall time never
// chooses how many ticks a fixture runs; it only paces its deterministic draws.
const benchmarkSimulationTPS = 30

// benchmarkPacer keeps one host deadline per draw. Arriving after a deadline
// rebases the following deadline rather than producing catch-up draw bursts.
// It is separate from Ebitengine's update clock, which is synchronized to Draw.
type benchmarkPacer struct {
	period time.Duration
	next   time.Time
}

func (p *benchmarkPacer) delay(now time.Time) time.Duration {
	target := p.next
	if target.IsZero() || !now.Before(target) {
		target = now
	}
	p.next = target.Add(p.period)
	return target.Sub(now)
}

func (g *battleBenchmark) tickPhase() (phase, drawsPerTick int) {
	drawsPerTick = g.options.TPS / benchmarkSimulationTPS
	return g.frame % drawsPerTick, drawsPerTick
}

func (g *battleBenchmark) warmupDraws() int { return 2 * g.options.TPS }

func benchmarkMS(start time.Time) float64             { return float64(time.Since(start)) / 1e6 }
func (g *battleBenchmark) Layout(int, int) (int, int) { return 1920, 1080 }
func (g *battleBenchmark) Update() error {
	if g.err != nil {
		return g.err
	}
	if g.done {
		return ebiten.Termination
	}
	return nil
}
func benchmarkFile(path string, write func(*os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = write(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (g *battleBenchmark) file(name string, write func(*os.File) error) error {
	return benchmarkFile(filepath.Join(g.options.Directory, name), write)
}
func (g *battleBenchmark) stopProfile() {
	if g.cpu != nil {
		pprof.StopCPUProfile()
		g.cpu.Close()
		g.cpu = nil
	}
}
func (g *battleBenchmark) finish() error {
	// Freeze exact counter deltas before profiler shutdown or any dump/encoding
	// allocates. The last Draw's deferred submission has returned by this point.
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	var gpuInfo ebiten.DebugInfo
	ebiten.ReadDebugInfo(&gpuInfo)
	g.stopProfile()
	// A live-heap snapshot is distinct from allocations during the window. GC
	// also flushes Go's delayed allocation profile; without it a short run with
	// no GC can incorrectly produce an empty allocation delta. It runs only
	// after measurement, so it cannot improve measured frame times.
	runtime.GC()
	var afterGC runtime.MemStats
	runtime.ReadMemStats(&afterGC)
	// Save both profiles before JSON/state/screenshot allocations. Sampled
	// deltas include profile setup/teardown; MemStats above is the exact window.
	if err := g.file("alloc.pprof", func(f *os.File) error { return pprof.Lookup("allocs").WriteTo(f, 0) }); err != nil {
		return err
	}
	if err := g.file("heap.pprof", func(f *os.File) error { return pprof.Lookup("heap").WriteTo(f, 0) }); err != nil {
		return err
	}
	if err := g.file("goroutine.txt", func(f *os.File) error { return pprof.Lookup("goroutine").WriteTo(f, 2) }); err != nil {
		return err
	}
	memory := map[string]any{"before": g.mem, "after": mem, "after_gc": afterGC, "gpu_image_bytes_before": g.gpuMemoryBefore, "gpu_image_bytes_after": gpuInfo.TotalGPUImageMemoryUsageInBytes, "graphics_library": gpuInfo.GraphicsLibrary.String()}
	if err := g.file("memory.json", func(f *os.File) error { return json.NewEncoder(f).Encode(memory) }); err != nil {
		return err
	}
	info, _ := debug.ReadBuildInfo()
	report := map[string]any{"tps": g.options.TPS, "metadata": g.options.Metadata, "renderer": g.options.Renderer, "go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "build": info, "rows": g.rows, "alloc_bytes": mem.TotalAlloc - g.mem.TotalAlloc, "mallocs": mem.Mallocs - g.mem.Mallocs, "gc": mem.NumGC - g.mem.NumGC, "gc_pause_ns": mem.PauseTotalNs - g.mem.PauseTotalNs}
	if err := g.file("frames.json", func(f *os.File) error { return json.NewEncoder(f).Encode(report) }); err != nil {
		return err
	}
	if g.options.AfterMeasure != nil {
		if err := g.options.AfterMeasure(); err != nil {
			return err
		}
	}
	// Screenshots and encoding deliberately follow all measured work.
	pic := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	g.img.ReadPixels(pic.Pix)
	return g.file("battle.png", func(f *os.File) error { return png.Encode(f, pic) })
}

func (g *battleBenchmark) Draw(screen *ebiten.Image) {
	if g.err != nil || g.done {
		return
	}
	entry := time.Now()
	if g.frame == g.warmupDraws()+g.options.Frames {
		g.c.JoinPreRecord()
		g.err = g.finish()
		g.done = true
		return
	}
	outside := 0.0
	if !g.lastEnd.IsZero() {
		outside = float64(entry.Sub(g.lastEnd)) / 1e6
	}

	if g.frame == g.warmupDraws() {
		g.c.JoinPreRecord()
		if g.options.BeforeMeasure != nil {
			if g.err = g.options.BeforeMeasure(); g.err != nil {
				return
			}
		}
		// Collect warmup and snapshot scratch before starting the measured window.
		runtime.GC()
		var gpuInfo ebiten.DebugInfo
		ebiten.ReadDebugInfo(&gpuInfo)
		g.gpuMemoryBefore = gpuInfo.TotalGPUImageMemoryUsageInBytes
		g.err = g.file("alloc-base.pprof", func(f *os.File) error { return pprof.Lookup("allocs").WriteTo(f, 0) })
		if g.err != nil {
			return
		}
		g.cpu, g.err = os.Create(filepath.Join(g.options.Directory, "cpu.pprof"))
		if g.err != nil {
			return
		}
		if g.err = pprof.StartCPUProfile(g.cpu); g.err != nil {
			g.cpu.Close()
			g.cpu = nil
			return
		}
		runtime.ReadMemStats(&g.mem)
		// Start a fresh interval sequence after the one-time profiling work.
		g.last = time.Time{}
		outside = 0
		entry = time.Now()
		g.pacer.next = time.Time{}
	}
	waitStart := time.Now()
	if delay := g.pacer.delay(entry); delay > 0 {
		time.Sleep(delay)
	}
	start := time.Now()
	paceWait := float64(start.Sub(waitStart)) / 1e6
	drawStart := start
	cadence, cadenceValid := 0.0, !g.last.IsZero()
	if cadenceValid {
		cadence = float64(drawStart.Sub(g.last)) / 1e6
	}
	g.last = drawStart
	// Join before any client mutation or tick publication [DESIGN_GPU_RENDERER
	// §13.10]. Its wait belongs to Record and to this callback's DrawWork.
	g.c.JoinPreRecord()
	join := benchmarkMS(start)
	start = time.Now()
	phase, drawsPerTick := g.tickPhase()
	stepped := phase == 0
	step := 0.0
	if stepped {
		g.step()
		g.c.BumpPresentationEpoch()
		step = benchmarkMS(start)
	}
	g.c.SetTickFraction(float32(phase) / float32(drawsPerTick))
	start = time.Now()
	var record, compose, replay, preRecord float64
	var hit bool
	var stats gpurender.ModelStats
	if g.gpu != nil {
		// The pipeline consumes the pre-recorded list only when it is exactly
		// the list this frame's synchronous record would produce: the tolerance
		// is zero here, so a measured frame is byte-identical to one recorded
		// in place (§13.10).
		g.c.BeginPresentationFrame()
		g.c.ResolveTickFraction()
		var list *drawlist.List
		list, hit = g.c.TakePreRecord(g.c.PresentationDigest(), 0)
		if hit {
			preRecord = float64(g.c.PreRecordNanos()) / 1e6
		} else {
			list = g.c.RecordModernFrame()
		}
		record = join + benchmarkMS(start)
		start = time.Now()
		g.gpu.SetDisplayPalette(g.c.DisplayPalette())
		g.img = g.gpu.Execute(list, 1920, 1080)
		stats = g.gpu.ModelStats()
	} else {
		pixels, a, b := g.c.BenchmarkFrame()
		compose, replay = a, b
		record = join + benchmarkMS(start)
		start = time.Now()
		g.img.WritePixels(pixels)
	}
	screen.DrawImage(g.img, nil)
	submit := benchmarkMS(start)
	measured := g.frame >= g.warmupDraws()
	var census any
	if measured {
		census = g.census()
	}
	frameNumber := g.frame
	g.frame++
	// A following draw on the same committed tick can be recorded while the
	// host flushes and paces. At 60 Hz one of two draws qualifies; at 120 three
	// of four do. Do not record an unused list after the final measured draw.
	if g.gpu != nil && phase+1 < drawsPerTick && g.frame < g.warmupDraws()+g.options.Frames {
		g.c.StartPreRecord(client.ClampTickFraction16(float32(phase+1)/float32(drawsPerTick)), 0, false)
	}
	if measured {
		g.rows = append(g.rows, benchmarkRow{Frame: frameNumber, Step: step, Record: record, Submit: submit, Cadence: cadence, CadenceValid: cadenceValid, PaceWait: paceWait, OutsideDraw: outside, SimulationStep: stepped, TickPhase: phase, CPUCompose: compose, CPUReplay: replay, CPURender: compose + replay, PreRecord: preRecord, Hit: hit, Stats: stats, Census: census})
	}
	g.lastEnd = time.Now()
	if measured {
		g.rows[len(g.rows)-1].DrawWork = float64(g.lastEnd.Sub(drawStart)) / 1e6
	}

}

// BattleBenchmark paces every Draw and runs a fixed number of draws per tick.
// It exercises production simulation/rendering, not the interactive catch-up scheduler.
func BattleBenchmark(c *client.Client, step func(), census func() any, options BenchmarkOptions) error {
	if options.TPS <= 0 {
		options.TPS = 30
	}
	if options.TPS != 30 && options.TPS != 60 && options.TPS != 120 {
		return fmt.Errorf("nanolathe: benchmark presentation rate must be 30, 60 or 120")
	}
	if options.Frames < 1 {
		return fmt.Errorf("nanolathe: benchmark needs at least one measured frame")
	}
	options.Metadata = maps.Clone(options.Metadata)
	if options.Metadata == nil {
		options.Metadata = make(map[string]any)
	}
	options.Metadata["benchmark_version"] = 2
	options.Metadata["simulation_tps"] = benchmarkSimulationTPS
	options.Metadata["draws_per_tick"] = options.TPS / benchmarkSimulationTPS
	options.Metadata["warmup_draws"] = options.TPS * 2
	options.Metadata["pacing"] = "draw-deadline"
	// The public Ebitengine API exposes neither device execution nor present
	// timestamps. OutsideDraw must never be interpreted as either one.
	options.Metadata["gpu_timing_available"] = false
	options.Metadata["present_timing_available"] = false
	g := &battleBenchmark{pacer: benchmarkPacer{period: time.Second / time.Duration(options.TPS)}, c: c, step: step, census: census, options: options, rows: make([]benchmarkRow, 0, options.Frames)}
	if options.Renderer == "modern" {
		g.gpu = gpurender.New(c.PaletteTables(), 1920, 1080)
		c.SetEnhanced(true)
		// Only the Enhanced executor blends; the classic rows keep
		// committed-tick sampling at every draw rate (§13.5) [I6].
		if options.TPS > benchmarkSimulationTPS {
			c.SetInterpolation(true)
		}
	} else {
		g.img = ebiten.NewImage(1920, 1080)
	}
	defer g.stopProfile()
	ebiten.SetWindowVisible(true)
	ebiten.SetRunnableOnUnfocused(true)
	ebiten.SetWindowSize(1920, 1080)
	ebiten.SetVsyncEnabled(true)
	// A separate fixed Update clock caused zero-update callbacks to skip Draw,
	// producing a beat pattern against the host's presentation loop. Every
	// callback now renders; the single deadline above limits the target rate.
	ebiten.SetTPS(ebiten.SyncWithFPS)
	ebiten.SetScreenClearedEveryFrame(false)
	if err := ebiten.RunGame(g); err != nil {
		return err
	}
	if !g.done {
		return fmt.Errorf("nanolathe: battle benchmark closed before measurement completed")
	}
	return g.err
}
