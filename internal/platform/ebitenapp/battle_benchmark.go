package ebitenapp

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
)

// BenchmarkOptions is host-only configuration; it never changes tick arithmetic.
type BenchmarkOptions struct {
	Directory, Renderer string
	Frames              int
	// TPS is the draw rate. At 30 and 60 one simulation step runs per draw; 30
	// is the retail cadence and 60 an intermediate presentation target (a
	// cadence at 60 is only reachable when CPU and GPU both finish inside
	// 16.7 ms). At 120 the benchmark runs one authoritative step every fourth
	// draw and presents the four frames at fractions 0, ¼, ½ and ¾, so the
	// report measures the interpolated presentation
	// (docs/DESIGN_GPU_RENDERER.md §13.5).
	TPS      int
	Metadata map[string]any
}
type benchmarkRow struct {
	Frame                            int
	Step, Record, Submit, Cadence    float64 // milliseconds; Submit is CPU submission, not GPU completion
	CPUCompose, CPUReplay, CPURender float64
	Stats                            gpurender.ModelStats
	Census                           any
}
type battleBenchmark struct {
	c             *client.Client
	step          func()
	census        func() any
	options       BenchmarkOptions
	gpu           *gpurender.Renderer
	img           *ebiten.Image
	rows          []benchmarkRow
	frame         int
	pending, done bool
	err           error
	last          time.Time
	cpu           *os.File
	mem           runtime.MemStats
}

// benchmarkInterpolatedTPS is the draw rate at which the benchmark measures the
// interpolated presentation, and benchmarkStepEvery is how many draws share one
// authoritative step there (§13.5).
const (
	benchmarkInterpolatedTPS = 120
	benchmarkStepEvery       = benchmarkInterpolatedTPS / 30
)

// tickPhase reports this draw's position in the four-draw group and whether the
// run is the interpolated one. At 30 and 60 every draw steps, as before.
func (g *battleBenchmark) tickPhase() (int, bool) {
	if g.options.TPS != benchmarkInterpolatedTPS {
		return 0, false
	}
	return g.frame % benchmarkStepEvery, true
}

func benchmarkMS(start time.Time) float64             { return float64(time.Since(start)) / 1e6 }
func (g *battleBenchmark) Layout(int, int) (int, int) { return 1920, 1080 }
func (g *battleBenchmark) Update() error {
	if g.err != nil {
		return g.err
	}
	if g.done {
		return ebiten.Termination
	}
	g.pending = true
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
	g.stopProfile()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	info, _ := debug.ReadBuildInfo()
	report := map[string]any{"tps": g.options.TPS, "metadata": g.options.Metadata, "renderer": g.options.Renderer, "go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "build": info, "rows": g.rows, "alloc_bytes": mem.TotalAlloc - g.mem.TotalAlloc, "mallocs": mem.Mallocs - g.mem.Mallocs, "gc": mem.NumGC - g.mem.NumGC, "gc_pause_ns": mem.PauseTotalNs - g.mem.PauseTotalNs}
	if err := g.file("frames.json", func(f *os.File) error { return json.NewEncoder(f).Encode(report) }); err != nil {
		return err
	}
	if err := g.file("alloc.pprof", func(f *os.File) error { return pprof.Lookup("allocs").WriteTo(f, 0) }); err != nil {
		return err
	}
	// Screenshots and encoding deliberately follow all measured work.
	pic := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	g.img.ReadPixels(pic.Pix)
	return g.file("battle.png", func(f *os.File) error { return png.Encode(f, pic) })
}
func (g *battleBenchmark) Draw(screen *ebiten.Image) {
	if !g.pending || g.err != nil || g.done {
		return
	}
	g.pending = false
	if g.frame == 60 {
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
	}
	if g.frame == 60+g.options.Frames {
		g.err = g.finish()
		g.done = true
		return
	}
	start := time.Now()
	step := 0.0
	if phase, interpolated := g.tickPhase(); !interpolated {
		g.step()
		step = benchmarkMS(start)
	} else {
		// One authoritative step every fourth draw, then the four presented
		// frames at fractions 0, ¼, ½ and ¾ (§13.5). The fraction stands in for
		// the clock's carry, which the benchmark's stepped millisecond source
		// does not produce.
		if phase == 0 {
			g.step()
			step = benchmarkMS(start)
		}
		g.c.SetTickFraction(float32(phase) / float32(benchmarkStepEvery))
	}
	now := time.Now()
	cadence := 0.0
	if !g.last.IsZero() {
		cadence = float64(now.Sub(g.last)) / 1e6
	}
	g.last = now
	start = time.Now()
	var record, compose, replay float64
	var stats gpurender.ModelStats
	if g.gpu != nil {
		list := g.c.RecordFrame()
		record = benchmarkMS(start)
		start = time.Now()
		g.gpu.SetDisplayPalette(g.c.DisplayPalette())
		g.img = g.gpu.Execute(list, 1920, 1080)
		stats = g.gpu.ModelStats()
	} else {
		pixels, a, b := g.c.BenchmarkFrame()
		compose, replay = a, b
		record = benchmarkMS(start)
		start = time.Now()
		g.img.WritePixels(pixels)
	}
	screen.DrawImage(g.img, nil)
	submit := benchmarkMS(start)
	if g.frame >= 60 {
		g.rows = append(g.rows, benchmarkRow{Frame: g.frame, Step: step, Record: record, Submit: submit, Cadence: cadence, CPUCompose: compose, CPUReplay: replay, CPURender: compose + replay, Stats: stats, Census: g.census()})
	}
	g.frame++
}

// BattleBenchmark runs one authoritative step per draw. It exercises production
// simulation and renderers, but does not model the interactive catch-up scheduler.
func BattleBenchmark(c *client.Client, step func(), census func() any, options BenchmarkOptions) error {
	g := &battleBenchmark{c: c, step: step, census: census, options: options, rows: make([]benchmarkRow, 0, options.Frames)}
	if options.Renderer == "modern" {
		g.gpu = gpurender.New(c.PaletteTables(), 1920, 1080)
		c.SetEnhanced(true)
		// Only the Enhanced executor blends; the classic rows keep
		// committed-tick sampling at every draw rate (§13.5) [I6].
		if options.TPS == benchmarkInterpolatedTPS {
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
	tps := options.TPS
	if tps <= 0 {
		tps = 30
	}
	ebiten.SetTPS(tps)
	ebiten.SetScreenClearedEveryFrame(false)
	if err := ebiten.RunGame(g); err != nil {
		return err
	}
	if !g.done {
		return fmt.Errorf("nanolathe: battle benchmark closed before measurement completed")
	}
	return g.err
}
