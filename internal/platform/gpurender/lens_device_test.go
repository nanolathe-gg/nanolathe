package gpurender

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

// Authored scene, not a stock Mind Gun unit. Keep the paired classic fixture in
// client/projectile_lens_test.go identical. This constructor also supports the
// optional frozen-list CPU replay measurement in the existing device loop.
func lensDeviceFixture(scale camera.ViewScale) (drawlist.List, palette.Tables, []byte, int, int) {
	n32, _ := scale.Whole()
	n := int(n32)
	w, h := 128*n, 80*n
	var p palette.Tables
	for i := range p.Base {
		v := byte(i)
		p.Base[i] = [4]byte{v, v * 3, v * 7, 255}
		p.Logical[i] = v
	}
	pixels := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			pixels[y*w+x] = byte(1 + (x/n+17*(y/n))%254)
		}
	}
	for y := 30 * n; y < 31*n; y++ {
		for x := 75 * n; x < 76*n; x++ {
			pixels[y*w+x] = 0
		}
	}
	want := append([]byte(nil), pixels...)
	var list drawlist.List
	list.RecordClear()
	list.RecordSurface(drawlist.Surface{Pixels: pixels, SrcW: int32(w), SrcH: int32(h), Dst: drawlist.Rect{W: int32(w), H: int32(h)}})
	list.RecordLine(drawlist.Line{X0: 24 * n32, Y0: 30 * n32, X1: 31 * n32, Y1: 30 * n32, Index: 210})
	for x := 24 * n; x <= 31*n; x++ {
		want[30*n*w+x] = 210
	}
	clip := drawlist.Rect{W: int32(w), H: int32(h)}
	for _, xy := range [][2]int32{{30, 30}, {31, 30}, {75, 30}, {0, 60}, {127, 60}} {
		list.RecordLens(drawlist.Lens{X: xy[0] * n32, Y: xy[1] * n32, Scale: scale, Clip: clip})
		// Independent finite-map reduction: only the center five-by-five differs
		// from identity, and its coordinates truncate by two [03 R-FX-01 §4].
		before := append([]byte(nil), want...)
		for dy := -2; dy <= 2; dy++ {
			for dx := -2; dx <= 2; dx++ {
				for by := 0; by < n; by++ {
					for bx := 0; bx < n; bx++ {
						x, y := (int(xy[0])+dx)*n+bx, (int(xy[1])+dy)*n+by
						sx, sy := (int(xy[0])+dx/2)*n+bx, (int(xy[1])+dy/2)*n+by
						if x >= 0 && y >= 0 && x < w && y < h && sx >= 0 && sy >= 0 && sx < w && sy < h && before[sy*w+sx] != 0 {
							want[y*w+x] = before[sy*w+sx]
						}
					}
				}
			}
		}
	}
	list.RecordLine(drawlist.Line{X0: 26 * n32, Y0: 33 * n32, X1: 35 * n32, Y1: 33 * n32, Index: 220})
	for x := 26 * n; x <= 35*n; x++ {
		want[33*n*w+x] = 220
	}
	return list, p, want, w, h
}

func checkLensDevicePixels() error {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		list, p, want, w, h := lensDeviceFixture(scale)
		r, err := NewChecked(&p, w, h)
		if err != nil {
			return err
		}
		img := r.Execute(&list, w, h)
		got := make([]byte, w*h*4)
		img.ReadPixels(got)
		for i, v := range want {
			c := p.Base[v]
			for k := 0; k < 4; k++ {
				if got[i*4+k] != c[k] {
					return fmt.Errorf("lens %s pixel(%d,%d) channel%d=%d want%d (index%d)", scale, i%w, i/w, k, got[i*4+k], c[k], v)
				}
			}
		}
		if dir := os.Getenv("NANOLATHE_LENS_SHOTS"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
			path := filepath.Join(dir, "lens-modern-"+scale.String()+".png")
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			err = png.Encode(f, &image.RGBA{Pix: got, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			// When paired classic captures are present, compare actual executor
			// outputs in addition to the independent finite-map reference above.
			f, err = os.Open(filepath.Join(dir, "lens-classic-"+scale.String()+".png"))
			if err == nil {
				classic, decodeErr := png.Decode(f)
				_ = f.Close()
				if decodeErr != nil {
					return decodeErr
				}
				diff, mismatch, err := lensPairDiff(got, classic, w, h)
				if err != nil {
					return err
				}
				f, err = os.Create(filepath.Join(dir, "lens-diff-"+scale.String()+".png"))
				if err != nil {
					return err
				}
				err = png.Encode(f, diff)
				_ = f.Close()
				if err != nil {
					return err
				}
				if mismatch != 0 {
					return fmt.Errorf("paired lens %s classic/modern differs at %d pixels; see diff image", scale, mismatch)
				}
			}
		}

	}
	return nil
}

func TestLensDeviceFixture(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for Metal lens fixture")
	}
	if deviceFixtureResult != nil {
		t.Fatal(deviceFixtureResult)
	}
}

// The optional cadence probe uses separate host Draw callbacks, not a loop of
// readbacks. It shares the authored correctness list, keeps readback outside
// measurement, and reports CPU submission and host frame intervals separately.
type lensFrozenGame struct {
	scale              camera.ViewScale
	list               drawlist.List
	renderer           *Renderer
	w, h, frame, count int
	last               time.Time
	cpu, interval      []float64
	allocStart         runtime.MemStats
	prepareMS          float64
	results            []lensFrozenResult
	done               bool
	err                error
}
type lensFrozenResult struct {
	Scale                                                          string
	Frames, WarmupFrames                                           int
	PreparationMS                                                  float64
	CPUmeanMS, CPUmedianMS, CPUp95MS, CPUp99MS                     float64
	IntervalMeanMS, IntervalMedianMS, IntervalP95MS, IntervalP99MS float64
	CPUSamplesMS, IntervalSamplesMS                                []float64
	HostAllocatedBytesPerFrame                                     float64
}

func runLensFrozenProfile() error {
	count, _ := strconv.Atoi(os.Getenv("NANOLATHE_LENS_PROFILE_FRAMES"))
	count = max(1, min(count, 10000))
	g := &lensFrozenGame{scale: camera.ViewScaleNative, count: count}
	ebiten.SetWindowVisible(true)
	ebiten.SetVsyncEnabled(true)
	ebiten.SetWindowSize(256, 160)
	if err := ebiten.RunGame(g); err != nil {
		return err
	}
	return g.err
}
func (g *lensFrozenGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}
func (g *lensFrozenGame) Layout(int, int) (int, int) { return 256, 160 }
func (g *lensFrozenGame) Draw(screen *ebiten.Image) {
	if g.done {
		return
	}
	if g.renderer == nil {
		prepareStart := time.Now()
		list, p, _, w, h := lensDeviceFixture(g.scale)
		g.list = list
		g.w, g.h = w, h
		g.renderer, g.err = NewChecked(&p, w, h)
		if g.err != nil {
			g.done = true
			return
		}
		g.cpu = make([]float64, 0, g.count)
		g.interval = make([]float64, 0, g.count)
		g.prepareMS = float64(time.Since(prepareStart)) / float64(time.Millisecond)
	}
	start := time.Now()
	img := g.renderer.Execute(&g.list, g.w, g.h)
	screen.DrawImage(img, nil)
	cost := float64(time.Since(start)) / float64(time.Millisecond)
	if g.frame >= 20 {
		g.cpu = append(g.cpu, cost)
		g.interval = append(g.interval, float64(start.Sub(g.last))/float64(time.Millisecond))
	}
	g.last = start
	g.frame++
	if g.frame == 20 {
		runtime.ReadMemStats(&g.allocStart)
	}
	if g.frame < 20+g.count {
		return
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	stats := func(raw []float64) (float64, float64, float64, float64) {
		v := append([]float64(nil), raw...)
		var sum float64
		for _, x := range v {
			sum += x
		}
		sort.Float64s(v)
		return sum / float64(len(v)), v[(len(v)-1)*50/100], v[(len(v)-1)*95/100], v[(len(v)-1)*99/100]
	}
	result := lensFrozenResult{Scale: g.scale.String(), Frames: g.count, WarmupFrames: 20, PreparationMS: g.prepareMS, CPUSamplesMS: g.cpu, IntervalSamplesMS: g.interval, HostAllocatedBytesPerFrame: float64(mem.TotalAlloc-g.allocStart.TotalAlloc) / float64(g.count)}
	result.CPUmeanMS, result.CPUmedianMS, result.CPUp95MS, result.CPUp99MS = stats(g.cpu)
	result.IntervalMeanMS, result.IntervalMedianMS, result.IntervalP95MS, result.IntervalP99MS = stats(g.interval)
	g.results = append(g.results, result)
	encoded, _ := json.Marshal(result)
	fmt.Printf("lens frozen cadence: %s\n", encoded)
	if dir := os.Getenv("NANOLATHE_LENS_SHOTS"); dir != "" {
		g.err = os.MkdirAll(dir, 0755)
		if g.err != nil {
			g.done = true
			return
		}
		pixels := make([]byte, g.w*g.h*4)
		img.ReadPixels(pixels)
		f, err := os.Create(filepath.Join(dir, "lens-frozen-"+g.scale.String()+".png"))
		if err != nil {
			g.err = err
			g.done = true
			return
		}
		g.err = png.Encode(f, &image.RGBA{Pix: pixels, Stride: g.w * 4, Rect: image.Rect(0, 0, g.w, g.h)})
		_ = f.Close()
		data, _ := json.MarshalIndent(g.results, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "lens-frozen-profile.json"), data, 0644); g.err == nil {
			g.err = err
		}
		if g.err != nil {
			g.done = true
			return
		}
	}
	if g.scale == camera.ViewScaleDetail {
		g.done = true
		return
	}
	g.scale = camera.ViewScaleDetail
	g.renderer = nil
	g.frame = 0
}

// lensPairDiff compares all four RGBA bytes exactly; alpha differences appear
// in every RGB diff channel so they remain visible in the saved diagnostic.
func lensPairDiff(got []byte, classic image.Image, w, h int) (*image.RGBA, int, error) {
	if len(got) != w*h*4 || classic.Bounds() != image.Rect(0, 0, w, h) {
		return nil, 0, fmt.Errorf("paired lens image dimensions differ")
	}
	diff := image.NewRGBA(image.Rect(0, 0, w, h))
	mismatch := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := classic.At(x, y).RGBA()
			want := [4]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8), byte(a >> 8)}
			i := (y*w + x) * 4
			var delta [4]byte
			changed := false
			for k := range delta {
				delta[k] = max(got[i+k], want[k]) - min(got[i+k], want[k])
				changed = changed || delta[k] != 0
			}
			if changed {
				mismatch++
			}
			for k := 0; k < 3; k++ {
				diff.Pix[i+k] = max(delta[k], delta[3])
			}
			diff.Pix[i+3] = 255
		}
	}
	return diff, mismatch, nil
}

func TestLensPairComparatorRejectsWrongImage(t *testing.T) {
	classic := image.NewRGBA(image.Rect(0, 0, 2, 1))
	classic.Pix = []byte{7, 9, 11, 255, 13, 15, 17, 255}
	got := append([]byte(nil), classic.Pix...)
	if _, n, err := lensPairDiff(got, classic, 2, 1); err != nil || n != 0 {
		t.Fatalf("equal image: mismatch=%d err=%v", n, err)
	}
	got[6]++
	diff, n, err := lensPairDiff(got, classic, 2, 1)
	if err != nil || n != 1 || diff.Pix[6] != 1 {
		t.Fatalf("one-byte wrong image accepted: mismatch=%d err=%v", n, err)
	}
}
