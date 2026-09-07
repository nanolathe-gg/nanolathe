package gpurender

import (
	"fmt"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

var deviceFixtureResult error

// TestMain owns the optional device loop so Ebiten starts on the process main
// goroutine. macOS graphics backends reject RunGame from a testing worker
// goroutine; ordinary tests still do no device work unless explicitly opted in.
func TestMain(m *testing.M) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") == "1" {
		game := &modelFixtureGame{}
		ebiten.SetWindowVisible(false)
		ebiten.SetWindowSize(40, 12)
		deviceFixtureResult = ebiten.RunGame(game)
		if deviceFixtureResult == nil {
			deviceFixtureResult = game.err
		}
	}
	os.Exit(m.Run())
}

// TestModelDeviceFixtures is opt-in because ordinary tests must not require a
// graphics device. It runs all ownership cases in one hidden Ebitengine loop,
// so the assertions inspect expanded/index pixels from the real backend.
func TestModelDeviceFixtures(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device model fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}

type modelFixtureGame struct {
	done bool
	err  error
}

func (g *modelFixtureGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}

func (g *modelFixtureGame) Draw(screen *ebiten.Image) {
	if g.done {
		return
	}
	defer func() { g.done = true }()
	pal := fixturePalette()
	r, err := NewChecked(&pal, 40, 12)
	if err != nil {
		g.err = fmt.Errorf("compile fixture shaders: %w", err)
		return
	}
	list := fixtureModelList()
	img := r.Execute(&list, 40, 12)
	if img == nil {
		g.err = fmt.Errorf("fixture renderer returned no image")
		return
	}
	var pixels = make([]byte, 40*12*4)
	img.ReadPixels(pixels)
	check := func(name string, x, y int, want byte) {
		got := pixels[(y*40+x)*4]
		if got != want && g.err == nil {
			g.err = fmt.Errorf("%s at (%d,%d): index %d, want %d", name, x, y, got, want)
		}
	}
	check("equal key later face", 1, 2, 4)
	check("transparent texture leaves background", 6, 2, 7)
	check("wrapped gradient wins", 13, 2, 5)
	check("wrapped gradient loses", 16, 2, 2)
	check("keyless painter order", 22, 2, 9)
	check("folded positive span", 31, 3, 6)
	check("folded negative lobe", 27, 1, 7)
	check("keyed sprite transparent skip", 36, 0, 7)
	stats := r.ModelStats()
	if stats.GPU != 5 && g.err == nil {
		g.err = fmt.Errorf("fixture GPU count = %d, want 5", stats.GPU)
	}
	if stats.CPUFallback != 1 && g.err == nil {
		g.err = fmt.Errorf("fixture CPU fallback count = %d, want 1", stats.CPUFallback)
	}
	if stats.MissingSource != 1 && g.err == nil {
		g.err = fmt.Errorf("fixture missing source count = %d, want 1", stats.MissingSource)
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
}

func (g *modelFixtureGame) Layout(int, int) (int, int) { return 40, 12 }

func fixturePalette() palette.Tables {
	var p palette.Tables
	for i := 0; i < 256; i++ {
		p.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
		p.Logical[i] = byte(i)
		p.Alpha[i*256+i] = byte(i)
		p.Gray[i], p.Blue[i] = byte(i), byte(i)
		for row := 0; row < 32; row++ {
			p.Light[row*256+i] = byte(i)
			p.Shade[row][i] = byte(i)
		}
	}
	return p
}

func fixtureModelList() drawlist.List {
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: 40, H: 12}, Index: 7, Style: drawlist.FillSolid})
	texture := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{1}, Transparent: []bool{true}}
	// The same transparent-marked index-1 texel is also sent through the keyed
	// sprite path. Its green admission flag must leave the background untouched.
	list.RecordSprite(drawlist.Sprite{Frame: texture, X: 36, Y: 0, Kind: drawlist.BlitKeyed})
	// Equal key: the later color 4 owns the tie.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true,
		fixtureFace(0, 0, 4, 4, 20, 3), fixtureFace(0, 0, 4, 4, 20, 4))})
	// Texture index 1 owns the key but is the model composition transparent
	// index, so the prior background remains visible.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true,
		fixtureFace(5, 0, 4, 4, 20, 3), drawlist.ModelFace{
			Vertices: []drawlist.ModelVertex{
				{X: 5, Y: 0, Key: 30, U: 0, V: 0}, {X: 9, Y: 0, Key: 30, U: 0, V: 0},
				{X: 9, Y: 4, Key: 30, U: 0, V: 0}, {X: 5, Y: 4, Key: 30, U: 0, V: 0},
			}, Texture: texture,
		})})
	// The interpolated key is 253 at pixel center x=13.5 and 3 at x=16.5.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true,
		fixtureFace(12, 0, 8, 4, 100, 2), drawlist.ModelFace{
			Vertices: []drawlist.ModelVertex{
				{X: 12, Y: 0, Key: 250}, {X: 20, Y: 0, Key: 266},
				{X: 20, Y: 4, Key: 266}, {X: 12, Y: 4, Key: 250},
			}, Color: 5,
		})})
	// Keyless subjects use later painter order even when its key is smaller.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, false,
		fixtureFace(22, 0, 4, 4, 200, 8), fixtureFace(22, 0, 4, 4, 100, 9))})
	// Authored crossed ring: only its positive row strips paint.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true, drawlist.ModelFace{Vertices: []drawlist.ModelVertex{
		{X: 26, Y: 0, Key: 20}, {X: 34, Y: 4, Key: 20}, {X: 32, Y: 6, Key: 20}, {X: 28, Y: 0, Key: 20},
	}, Color: 6})})
	// Unsupported geometry has an explicit fallback and no model source. It
	// contributes diagnostics without claiming a successful body image.
	list.RecordModel(drawlist.Model{Geometry: &drawlist.ModelGeometry{Fallback: drawlist.ModelFallbackWaterlineOrDigger}})
	list.RecordExpand()
	return list
}

func fixtureGeometry(anchor int32, keyPlane bool, faces ...drawlist.ModelFace) *drawlist.ModelGeometry {
	return &drawlist.ModelGeometry{Eligible: true, Faces: faces, AnchorX: anchor, AnchorY: 0, Scale: 1, KeyPlane: keyPlane}
}

func fixtureFace(x, y, w, h, key int32, color uint8) drawlist.ModelFace {
	return drawlist.ModelFace{Color: color, Vertices: []drawlist.ModelVertex{
		{X: x, Y: y, Key: key}, {X: x + w, Y: y, Key: key}, {X: x + w, Y: y + h, Key: key}, {X: x, Y: y + h, Key: key},
	}}
}
