package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The authored strip isolates each raw coverage channel, including RGB with
// stored alpha zero, which must survive upload as mask data (GPU design §18.5).
func checkStrategicIconDevicePixels() error {
	pal := fixturePalette()
	pal.Base[10] = [4]byte{240, 40, 20, 255}
	pal.Base[11] = [4]byte{20, 220, 80, 255}
	pal.Base[12] = [4]byte{40, 80, 240, 255}
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	atlas := &drawlist.MarkerAtlas{Width: 8, Height: 4, Pixels: make([]byte, 8*4*4)}
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			atlas.Pixels[(y*8+x)*4+x/2] = 255
		}
	}
	marker := func(x, y int32) drawlist.Marker {
		return drawlist.Marker{IconAtlas: atlas, IconRect: drawlist.Rect{W: 8, H: 4}, X: x, Y: y, Size: 8, Index: 10, Outline: 11, Alpha: 255}
	}
	var l drawlist.List
	l.RecordClear()
	l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 100})
	selected := marker(4, 4)
	selected.Selected = true
	unselected := marker(14, 4)
	fade := marker(24, 4)
	fade.Selected = true
	fade.Alpha = 128
	clipped := marker(34, 4)
	clipped.Selected = true
	clipped.HasClip = true
	clipped.Clip = drawlist.Rect{X: 32, Y: 0, W: 4, H: 8}
	first, second := marker(44, 4), marker(44, 4)
	second.Index = 12
	generic := drawlist.Marker{X: 54, Y: 4, Size: 4, Index: 10, Alpha: 255}
	// A scaled one-texel region tests filter clamping against adjacent channels.
	edge := marker(64, 4)
	edge.IconRect = drawlist.Rect{X: 2, W: 1, H: 4}
	malformed := marker(74, 4)
	malformed.IconRect.X = 100
	l.RecordMarkers(drawlist.Markers{Marks: []drawlist.Marker{selected, unselected, fade, clipped, first, second, generic, edge, malformed}})
	pix := make([]byte, 80*48*4)
	r.Execute(&l, 80, 48).ReadPixels(pix)
	check := func(name string, x, y int, want [4]byte) error {
		got := pix[(y*80+x)*4:][:4]
		for k := range got {
			d := int(got[k]) - int(want[k])
			if d < -1 || d > 1 {
				return fmt.Errorf("strategic icon %s at %d,%d: got %v want %v", name, x, y, got, want)
			}
		}
		return nil
	}
	for _, c := range []struct {
		name string
		x, y int
		rgba [4]byte
	}{
		{"team raw channel", 0, 3, pal.Base[10]}, {"white glyph", 2, 3, [4]byte{255, 255, 255, 255}},
		{"selection halo", 4, 3, pal.Base[11]}, {"black backing", 6, 3, [4]byte{0, 0, 0, 255}},
		{"hidden halo", 14, 3, pal.Base[100]}, {"fade team", 20, 3, [4]byte{170, 70, 60, 255}},
		{"fade white", 22, 3, [4]byte{178, 178, 178, 255}}, {"fade backing", 26, 3, [4]byte{50, 50, 50, 255}},
		{"clip exterior", 31, 3, pal.Base[100]}, {"clip UV", 32, 3, [4]byte{255, 255, 255, 255}},
		{"clip halo UV", 34, 3, pal.Base[11]}, {"overlap order", 40, 3, pal.Base[12]},
		{"generic compatibility", 53, 3, pal.Base[10]}, {"clamped tile left", 60, 3, [4]byte{255, 255, 255, 255}},
		{"clamped tile right", 67, 3, [4]byte{255, 255, 255, 255}}, {"invalid art skipped", 74, 3, pal.Base[100]},
	} {
		if err := check(c.name, c.x, c.y, c.rgba); err != nil {
			return err
		}
	}
	if r.markerWrites != 1 {
		return fmt.Errorf("icon atlas uploads=%d, want one shared upload", r.markerWrites)
	}
	// The clone keeps the source identity while the caller reuses its records.
	cloned := l.Clone()
	again := make([]byte, len(pix))
	r.Execute(&cloned, 80, 48).ReadPixels(again)
	if r.markerWrites != 1 || !bytes.Equal(pix, again) {
		return fmt.Errorf("icon atlas retained replay changed pixels or uploaded again")
	}
	if path := os.Getenv("NANOLATHE_ICON_FIXTURE_PNG"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		err = png.Encode(f, &image.RGBA{Pix: pix, Stride: 80 * 4, Rect: image.Rect(0, 0, 80, 48)})
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	// Palette changes resolve at draw time and must not trigger art upload.
	changed := pal.Base
	changed[10] = [4]byte{50, 90, 180, 255}
	r.SetDisplayPalette(changed)
	r.Execute(&cloned, 80, 48).ReadPixels(pix)
	if err := check("live palette", 0, 3, changed[10]); err != nil {
		return err
	}
	if r.markerWrites != 1 {
		return fmt.Errorf("palette change uploaded icon masks")
	}
	// Retained lists remain valid after the bounded cache evicts their source.
	for i := 0; i < 5; i++ {
		a := &drawlist.MarkerAtlas{Width: 1, Height: 1, Pixels: []byte{255, 0, 0, 0}}
		m := marker(4, 4)
		m.IconAtlas = a
		m.IconRect = drawlist.Rect{W: 1, H: 1}
		var frame drawlist.List
		frame.RecordMarkers(drawlist.Markers{Marks: []drawlist.Marker{m}})
		r.Execute(&frame, 80, 48)
	}
	r.Execute(&cloned, 80, 48).ReadPixels(pix)
	if err := check("replay after eviction", 0, 3, changed[10]); err != nil {
		return err
	}
	// Supersampled coverage is filtered before color resolution at the chosen
	// 20px display size. The boundary deliberately falls between output samples.
	large := &drawlist.MarkerAtlas{Width: 32, Height: 32, Pixels: make([]byte, 32*32*4)}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			channel := 0
			if x >= 15 {
				channel = 1
			}
			large.Pixels[(y*32+x)*4+channel] = 255
		}
	}
	m := marker(10, 30)
	m.IconAtlas = large
	m.IconRect = drawlist.Rect{W: 32, H: 32}
	m.Size = 20
	mixed := &drawlist.MarkerAtlas{Width: 1, Height: 1, Pixels: []byte{128, 64, 0, 0}}
	mixMark := marker(30, 30)
	mixMark.IconAtlas = mixed
	mixMark.IconRect = drawlist.Rect{W: 1, H: 1}
	var filtered drawlist.List
	filtered.RecordClear()
	filtered.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 100})
	filtered.RecordMarkers(drawlist.Markers{Marks: []drawlist.Marker{m, mixMark}})
	r.SetDisplayPalette(pal.Base)
	r.Execute(&filtered, 80, 48).ReadPixels(pix)
	if err := check("32px source filtered at 20px", 9, 30, [4]byte{251, 191, 185, 255}); err != nil {
		return err
	}
	if err := check("partial coverage source over", 30, 30, [4]byte{209, 109, 99, 255}); err != nil {
		return err
	}
	r.ResetSources()
	for _, c := range r.markerAtlases {
		if c.source != nil || c.image != nil {
			return fmt.Errorf("icon atlas retained after source reset")
		}
	}
	return checkMixedStrategicIconBatch()
}

// Mixed radar contacts and typed icons must not fragment into one Metal draw
// per contact. Compare against the legacy nil-atlas branch in singleton batches
// so corner alpha rounding and clipped outlines stay byte-identical (§18.5).
func checkMixedStrategicIconBatch() error {
	pal := fixturePalette()
	pal.Base[10] = [4]byte{240, 40, 20, 255}
	pal.Base[11] = [4]byte{20, 220, 80, 255}
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	atlas := &drawlist.MarkerAtlas{Width: 1, Height: 1, Pixels: []byte{128, 64, 0, 0}}
	marks := make([]drawlist.Marker, 100)
	for i := range marks {
		m := drawlist.Marker{X: int32(2 + (i%20)*4), Y: int32(2 + (i/20)*5), Size: 8, Alpha: uint8(63 + i), Index: 10, Outline: 11, Selected: i%3 == 0}
		if i%2 == 1 {
			m.IconAtlas = atlas
			m.IconRect = drawlist.Rect{W: 1, H: 1}
		}
		if i%7 == 0 {
			m.HasClip = true
			m.Clip = drawlist.Rect{X: m.X, Y: m.Y, W: 1, H: 1}
		}
		marks[i] = m
	}
	r.Markers(drawlist.Markers{Marks: marks})
	if r.sched.nphase != 1 || len(r.sched.phases[0].batch[schedDest].runs) != 1 {
		return fmt.Errorf("mixed icon layer compiled %d phases, want one phase and one run", r.sched.nphase)
	}
	var mixed, reference drawlist.List
	for _, l := range []*drawlist.List{&mixed, &reference} {
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 100})
	}
	mixed.RecordMarkers(drawlist.Markers{Marks: marks})
	for i := range marks {
		reference.RecordMarkers(drawlist.Markers{Marks: marks[i : i+1]})
	}
	got, want := make([]byte, 80*48*4), make([]byte, 80*48*4)
	r.Execute(&mixed, 80, 48).ReadPixels(got)
	mixedDraws := r.DeviceDraws()
	r.Execute(&reference, 80, 48).ReadPixels(want)
	if !bytes.Equal(got, want) {
		for i := range got {
			if got[i] != want[i] {
				return fmt.Errorf("mixed icon batching changed pixel %d channel %d: got %d want %d", i/4, i%4, got[i], want[i])
			}
		}
	}
	if r.DeviceDraws() <= mixedDraws {
		return fmt.Errorf("mixed batching draws=%d, singleton draws=%d", mixedDraws, r.DeviceDraws())
	}
	if r.markerWrites != 1 {
		return fmt.Errorf("mixed contacts uploaded icon atlas %d times, want once", r.markerWrites)
	}
	return nil
}
