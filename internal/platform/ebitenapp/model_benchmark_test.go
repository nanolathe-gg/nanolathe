// gpu-bench measures the modern model executor on authored crowds of installed
// assets. It is a presentation microbenchmark, not a simulated battle or retail
// pose. Readback and PNG encoding happen once after all timing samples.
package ebitenapp

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const width, height, warmup = 1920, 1080, 8

type benchmark struct {
	preview                          *client.ModelPreviewRenderer
	renderer                         *gpurender.Renderer
	output                           *ebiten.Image
	options                          client.ModelPreviewOptions
	frozen                           drawlist.List
	mode, shot                       string
	count, frames, draws             int
	last                             time.Time
	preparation, submission, cadence []float64
	done                             bool
	err                              error
}

func (b *benchmark) scene(ordinal int) (drawlist.List, error) {
	var l drawlist.List
	l.RecordClear()
	l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: width, H: height}, Index: 7, Style: drawlist.FillSolid})
	columns := 1
	for columns*columns < b.count*width/height {
		columns++
	}
	rows := (b.count + columns - 1) / columns
	for i := 0; i < b.count; i++ {
		op := b.options
		op.Heading = uint16(i * 4096)
		if b.mode == "turn" {
			op.Heading += uint16(ordinal * 128)
		}
		record, err := b.preview.RecordGeometry(op)
		if err != nil {
			return l, err
		}
		for _, cmd := range record.List.ModelCommands() {
			dx := int32((i%columns)*width/columns + width/(2*columns) - width/2)
			dy := int32((i/columns)*height/rows + height/(2*rows) - height/2)
			if b.mode == "pan" {
				dx += int32(ordinal%64) - 32
			}
			translate(cmd.Geometry, dx, dy)
			l.RecordModel(cmd)
		}
	}
	l.RecordExpand()
	return l, nil
}

func translate(g *drawlist.ModelGeometry, dx, dy int32) {
	if g == nil {
		return
	}
	g.AnchorX += dx
	g.AnchorY += dy
	translate(g.Shadow, dx, dy)
	for _, child := range g.Children {
		translate(child.Geometry, dx, dy)
	}
}

func (b *benchmark) Update() error {
	if b.done {
		return ebiten.Termination
	}
	return nil
}
func (b *benchmark) Layout(int, int) (int, int) { return width, height }
func (b *benchmark) Draw(screen *ebiten.Image) {
	if b.done {
		return
	}
	start := time.Now()
	if b.draws > warmup {
		b.cadence = append(b.cadence, float64(start.Sub(b.last))/1e6)
	}
	b.last = start
	if b.draws == warmup+b.frames {
		b.report()
		if b.shot != "" {
			pixels := image.NewRGBA(image.Rect(0, 0, width, height))
			b.output.ReadPixels(pixels.Pix)
			f, err := os.Create(b.shot)
			if err == nil {
				err = png.Encode(f, pixels)
				if e := f.Close(); err == nil {
					err = e
				}
			}
			b.err = err
		}
		b.done = true
		return
	}
	l := b.frozen
	if b.mode != "frozen" {
		var err error
		l, err = b.scene(b.draws)
		if err != nil {
			b.err, b.done = err, true
			return
		}
	}
	prepared := time.Now()
	img := b.renderer.Execute(&l, width, height)
	screen.DrawImage(img, nil)
	b.output = img
	end := time.Now()
	if b.draws >= warmup {
		b.preparation = append(b.preparation, float64(prepared.Sub(start))/1e6)
		b.submission = append(b.submission, float64(end.Sub(prepared))/1e6)
	}
	stats := b.renderer.ModelStats()
	if stats.GPU == 0 || stats.Skipped != 0 {
		b.err, b.done = fmt.Errorf("crowd omitted model geometry: %+v", stats), true
		return
	}
	b.draws++
}

func metric(v []float64) map[string]float64 {
	v = append([]float64(nil), v...)
	sort.Float64s(v)
	return map[string]float64{"median_ms": v[(len(v)+1)/2-1], "p95_ms": v[(len(v)*95+99)/100-1], "p99_ms": v[(len(v)*99+99)/100-1]}
}
func (b *benchmark) report() {
	var debug ebiten.DebugInfo
	ebiten.ReadDebugInfo(&debug)
	result := map[string]any{
		"mode": b.mode, "model": b.options.Model, "count": b.count, "width": width, "height": height,
		"frames": b.frames, "warmup": warmup, "host": runtime.GOOS + "/" + runtime.GOARCH, "backend": fmt.Sprint(debug.GraphicsLibrary),
		"preparation": metric(b.preparation), "submission": metric(b.submission), "cadence": metric(b.cadence), "last_frame_models": b.renderer.ModelStats(),
		"policy": "vsync off; TPS SyncWithFPS; cadence includes CPU preparation, GPU backpressure and host presentation/scheduling; no terrain, shadows, simulation, input or audio; 16 repeated headings; screenshot readback/PNG excluded; poses are authored tool inputs",
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		b.err = err
	}
}

func run() error {
	b := &benchmark{}
	root := flag.String("assets", filepath.Join(os.Getenv("HOME"), "TotalAnnihilation"), "installed retail asset directory")
	model := flag.String("unit", "armsolar", "installed unit definition")
	flag.StringVar(&b.mode, "mode", "record", "frozen, record (unchanged poses), pan, or turn")
	flag.StringVar(&b.shot, "shot", "", "optional PNG after measurement")
	flag.IntVar(&b.count, "count", 100, "visible crowd size")
	flag.IntVar(&b.frames, "frames", 120, "measured frames after eight warmups")
	flag.Parse()
	if b.count < 1 || b.frames < 1 {
		return fmt.Errorf("count and frames must be positive")
	}
	if b.mode != "frozen" && b.mode != "record" && b.mode != "pan" && b.mode != "turn" {
		return fmt.Errorf("unknown mode %q", b.mode)
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(*root); err != nil {
		return err
	}
	cat, err := content.Compile(fs)
	if err != nil {
		return err
	}
	u := cat.Units[*model]
	if u == nil {
		return fmt.Errorf("unknown unit %q", *model)
	}
	b.preview, err = client.NewModelPreviewRenderer(fs)
	if err != nil {
		return err
	}
	b.options = client.ModelPreviewOptions{Model: u.ObjectName, Structure: u.BMCode == 0, KeyPlane: u.ZBuffer, Digger: u.Digger, Width: width, Height: height, Scale: 1, Background: 7}
	record, err := b.preview.RecordGeometry(b.options)
	if err != nil {
		return err
	}
	b.renderer, err = gpurender.NewChecked(record.Palette, width, height)
	if err != nil {
		return err
	}
	b.frozen, err = b.scene(0)
	if err != nil {
		return err
	}
	ebiten.SetWindowVisible(false)
	ebiten.SetVsyncEnabled(false)
	ebiten.SetTPS(ebiten.SyncWithFPS)
	ebiten.SetRunnableOnUnfocused(true)
	if err := ebiten.RunGame(b); err != nil {
		return err
	}
	return b.err
}

var modelBenchmarkError error

// TestMain keeps the benchmark's graphics loop on macOS's process main thread.
// Ordinary tests never load assets or start the loop.
func TestMain(m *testing.M) {
	if os.Getenv("NANOLATHE_GPU_BENCH") == "1" {
		modelBenchmarkError = run()
	}
	os.Exit(m.Run())
}

func TestGPUModelBenchmark(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_BENCH") != "1" {
		t.Skip("use tools/gpu-bench for the opt-in model benchmark")
	}
	if modelBenchmarkError != nil {
		t.Fatal(modelBenchmarkError)
	}
}
