package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestArrivalShaderCompiles(t *testing.T) {
	if _, err := newArrivalShader(); err != nil {
		t.Fatal(err)
	}
}

func TestArrivalTransformsOnceAndBoundsLifetime(t *testing.T) {
	r := &Renderer{w: 640, h: 480}
	w := drawlist.WorldSpace{Begin: true, Step: camera.ViewScaleDetail,
		Factor: 1.5, OffsetX: 3.25, OffsetY: -1.5,
		Viewport: drawlist.Rect{X: 20, Y: 30, W: 600, H: 420},
		Arrival:  drawlist.Arrival{Active: true, Seconds: 0.4, X: 200, Y: 120, GridX: -16, GridY: -24, Scale: 2, RevealRadius: 75, DropHeight: 84}}
	r.World(w)
	a := r.arrival.packet
	if !a.Active || a.X != 153.25 || a.Y != 88.5 || a.GridX != -8.75 || a.GridY != -19.5 || a.Scale != 1.5 || a.RevealRadius != 75 || a.DropHeight != 84 {
		t.Fatalf("arrival transform: %+v", a)
	}
	if r.arrival.clip != [4]float32{20, 30, 620, 450} {
		t.Fatalf("framebuffer viewport transformed twice: %v", r.arrival.clip)
	}
	for _, seconds := range []float32{0, drawlist.ArrivalImpactSeconds, drawlist.ArrivalDurationSeconds - 0.01} {
		w.Arrival.Seconds = seconds
		r.World(w)
		if !r.arrival.packet.Active {
			t.Fatalf("inactive inside authored lifetime: %v", seconds)
		}
	}
	for _, seconds := range []float32{-0.1, drawlist.ArrivalDurationSeconds, 12} {
		w.Arrival.Seconds = seconds
		r.World(w)
		if r.arrival.packet.Active {
			t.Fatalf("active outside authored lifetime: %v", seconds)
		}
	}
	w.Arrival.Active = false
	w.Arrival.Seconds = 0.4
	r.World(w)
	if r.arrival.packet.Active || r.arrival.opts.Uniforms != nil || r.frameDraws != 0 {
		t.Fatal("inactive metadata allocated resources or submitted a pass")
	}
}

func TestArrivalSourceResetDropsTransientBindings(t *testing.T) {
	shader := &ebiten.Shader{}
	r := &Renderer{arrival: arrivalLayer{shader: shader, packet: drawlist.Arrival{Active: true}}}
	r.arrival.opts.Images[0] = &ebiten.Image{}
	r.arrival.opts.Uniforms = map[string]any{"Arrival": r.arrival.params[:]}
	r.resetSources(func(*ebiten.Image) { t.Fatal("arrival should own no images") })
	if r.arrival.shader != shader || r.arrival.packet.Active || r.arrival.opts.Images[0] != nil || r.arrival.opts.Uniforms != nil {
		t.Fatal("source reset retained transient arrival state or lost shader")
	}
}

func TestArrivalRevealOnlyEndsAtRevealBoundary(t *testing.T) {
	r := &Renderer{w: 320, h: 240}
	w := drawlist.WorldSpace{Begin: true, Step: camera.ViewScaleNative,
		Arrival: drawlist.Arrival{Active: true, RevealOnly: true, Scale: 1}}
	for _, tc := range []struct {
		seconds float32
		active  bool
	}{
		{-0.1, false},
		{0, true},
		{math.Nextafter32(drawlist.ArrivalRevealSeconds, 0), true},
		{drawlist.ArrivalRevealSeconds, false},
		{drawlist.ArrivalImpactSeconds, false},
		{drawlist.ArrivalDurationSeconds, false},
	} {
		w.Arrival.Seconds = tc.seconds
		r.World(w)
		if a := r.arrival.packet; a.Active != tc.active || tc.active && !a.RevealOnly {
			t.Fatalf("reveal-only at %v: %+v, want active %v", tc.seconds, a, tc.active)
		}
	}
}

// Called by the shared optional device loop. Authored checker/feature/pond
// shapes exercise completed-scene sampling; no retail assets are needed.
func checkArrivalDevicePixels() error {
	const w, h = 320, 240
	pal := fixturePalette()
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	makeList := func(active, revealOnly bool, seconds float32) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordWorld(drawlist.WorldSpace{Begin: true, Step: camera.ViewScaleNative,
			Viewport: drawlist.Rect{X: 20, Y: 16, W: 280, H: 208},
			Arrival:  drawlist.Arrival{Active: active, RevealOnly: revealOnly, Seconds: seconds, X: 160, Y: 136, GridX: -8, GridY: -16, Scale: 1, RevealRadius: 190, DropHeight: 220}})
		for y := int32(16); y < 224; y += 8 {
			for x := int32(20); x < 300; x += 8 {
				l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: x, Y: y, W: 8, H: 8}, Index: uint8(60 + ((x/8+y/8)%2)*60)})
			}
		}
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 210, Y: 150, W: 60, H: 40}, Index: 160})
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 72, Y: 42, W: 14, H: 32}, Index: 195})
		// This final opaque black strip stands for already-composited unknown fog.
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 245, Y: 16, W: 55, H: 112}, Index: 0})
		l.RecordWorld(drawlist.WorldSpace{})
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 20, H: h}, Index: 230})
		l.RecordExpand()
		return l
	}
	readMode := func(active, revealOnly bool, seconds float32) []byte {
		list := makeList(active, revealOnly, seconds)
		pix := make([]byte, w*h*4)
		r.Execute(&list, w, h).ReadPixels(pix)
		return pix
	}
	read := func(active bool, seconds float32) []byte {
		return readMode(active, false, seconds)
	}
	before := read(false, 0)
	plainDraws := r.DeviceDraws()
	for _, seconds := range []float32{0.2, 0.75, 1.05, 1.18, 1.23, 1.35, 1.94} {
		after := read(true, seconds)
		// The settled map and a descent still above this small viewport can
		// legitimately be unchanged. Reveal and impact must affect pixels.
		if (seconds < 0.9 || seconds >= drawlist.ArrivalImpactSeconds && seconds < drawlist.ArrivalDurationSeconds-0.05) && bytes.Equal(before, after) {
			return fmt.Errorf("arrival at %v did not affect scene", seconds)
		}
		if !bytes.Equal(after, read(true, seconds)) {
			return fmt.Errorf("arrival at %v advanced during identical replay", seconds)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 4
				outside := x < 20 || x >= 300 || y < 16 || y >= 224
				black := before[i] == 0 && before[i+1] == 0 && before[i+2] == 0
				if (outside || black) && !bytes.Equal(before[i:i+4], after[i:i+4]) {
					return fmt.Errorf("arrival at %v changed fog or chrome at %d,%d", seconds, x, y)
				}
			}
		}
		if dir := os.Getenv("NANOLATHE_ARRIVAL_SHOTS"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("arrival-%04.2f.png", seconds)))
			if err != nil {
				return err
			}
			err = png.Encode(f, &image.RGBA{Pix: after, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	for _, active := range []bool{true, false} {
		if !bytes.Equal(before, read(active, drawlist.ArrivalDurationSeconds)) || r.DeviceDraws() != plainDraws {
			return fmt.Errorf("completed/inactive arrival changed ordinary pixels or draw count")
		}
		if r.arrival.opts.Uniforms != nil || r.arrival.opts.Images[0] != nil {
			return fmt.Errorf("completed arrival retained temporary bindings")
		}
	}
	// Before descent, save reveal reuses the same fade and bounce. The neutral
	// scene must remain neutral during descent time: any warm trail is a bug.
	if !bytes.Equal(read(true, 0.75), readMode(true, true, 0.75)) {
		return fmt.Errorf("reveal-only changed the existing fade and bounce")
	}
	for _, seconds := range []float32{0.2, 0.75, 1.18} {
		after := readMode(true, true, seconds)
		if seconds < 0.9 && bytes.Equal(before, after) {
			return fmt.Errorf("reveal-only at %v did not affect scene", seconds)
		}
		if r.DeviceDraws() <= plainDraws {
			return fmt.Errorf("reveal-only at %v retired before its endpoint", seconds)
		}
		if !bytes.Equal(after, readMode(true, true, seconds)) {
			return fmt.Errorf("reveal-only at %v advanced during identical replay", seconds)
		}
		for i := 0; i < len(after); i += 4 {
			if after[i] != after[i+1] || after[i] != after[i+2] {
				return fmt.Errorf("reveal-only at %v added warm descent colour", seconds)
			}
			x, y := (i/4)%w, (i/4)/w
			outside := x < 20 || x >= 300 || y < 16 || y >= 224
			black := before[i] == 0 && before[i+1] == 0 && before[i+2] == 0
			if (outside || black) && !bytes.Equal(before[i:i+4], after[i:i+4]) {
				return fmt.Errorf("reveal-only at %v changed fog or chrome at %d,%d", seconds, x, y)
			}
		}
		if seconds == 1.18 && bytes.Equal(after, read(true, seconds)) {
			return fmt.Errorf("descent fixture failed to distinguish full arrival from reveal-only")
		}
	}
	// A save resumes at the exact reveal endpoint. It never submits the later
	// flash, shockwave or recoil, and releases all temporary composite bindings.
	for _, seconds := range []float32{drawlist.ArrivalRevealSeconds, drawlist.ArrivalImpactSeconds, 1.35, drawlist.ArrivalDurationSeconds} {
		if !bytes.Equal(before, readMode(true, true, seconds)) || r.DeviceDraws() != plainDraws {
			return fmt.Errorf("completed reveal-only at %v changed ordinary pixels or draw count", seconds)
		}
		if r.arrival.opts.Uniforms != nil || r.arrival.opts.Images[0] != nil {
			return fmt.Errorf("completed reveal-only retained temporary bindings")
		}
	}
	return nil
}
