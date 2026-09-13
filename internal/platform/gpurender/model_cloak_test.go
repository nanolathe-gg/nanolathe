package gpurender

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Exercise opaque/cloaked/decloaked commits using one resident body raster,
// plus opaque cargo composed into the carrier before its ALP blend
// [03 R-RAST-01 §7][03 R-REN-03A §4].
func checkModelCloakDevicePixels() error {
	pal := fixturePalette()
	const w, h = 80, 48
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	for _, scale := range []int32{1, 2} {
		g := directSubject(8, 8, 22*scale, 18*scale, directFace(0, 0, 20*scale, 16*scale, 200, 10, 10))
		g.Cache = drawlist.ModelCacheKey{Body: uint64(scale), Revision: 1}
		for _, cloak := range []bool{false, true, false} {
			g.Cloaked = cloak
			var list drawlist.List
			list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 40, Style: drawlist.FillSolid})
			list.RecordModel(drawlist.Model{Geometry: g})
			carrier := directSubject(56, 8, 18, 18, directFace(0, 0, 16, 16, 200, 10, 10))
			child := directSubject(56, 8, 18, 18, directFace(0, 0, 16, 16, 160, 20, 20))
			carrier.Cloaked, child.Cloaked = cloak, !cloak
			carrier.Children = []drawlist.ModelChild{{Geometry: child}}
			list.RecordModel(drawlist.Model{Geometry: carrier})
			list.RecordExpand()
			img := r.Execute(&list, w, h)
			if img == nil {
				return fmt.Errorf("cloak: no device image")
			}
			pix := make([]byte, w*h*4)
			img.ReadPixels(pix)
			want := 200
			if cloak {
				want = 120
			}
			got := int(pix[(12*w+12)*4])
			if got < want-1 || got > want+1 {
				return fmt.Errorf("cloak %v scale %d: pixel %d, want %d", cloak, scale, got, want)
			}
			cargoWant := 160
			if cloak {
				cargoWant = 100
			}
			cargoGot := int(pix[(12*w+60)*4])
			if cargoGot < cargoWant-1 || cargoGot > cargoWant+1 {
				return fmt.Errorf("carrier cloak %v: staged cargo %d, want %d", cloak, cargoGot, cargoWant)
			}
			if r.modelStats.DirectOverflow != 0 {
				return fmt.Errorf("cloak fixture overflowed")
			}
			if dir := os.Getenv("NANOLATHE_CLOAK_CAPTURE"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return err
				}
				path := filepath.Join(dir, fmt.Sprintf("modern-cloak-%v-scale-%d.png", cloak, scale))
				f, err := os.Create(path)
				if err != nil {
					return err
				}
				err = png.Encode(f, &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
	}
	return captureRetailCloakModels()
}

// Captures use stock art through the production preview and both executors;
// they are opt-in local evidence, never copied retail fixtures.
func captureRetailCloakModels() error {
	dir := os.Getenv("NANOLATHE_CLOAK_CAPTURE")
	root := os.Getenv("NANOLATHE_RETAIL_ASSETS")
	if dir == "" || root == "" {
		return nil
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		return err
	}
	defer fs.Close()
	preview, err := client.NewModelPreviewRenderer(fs)
	if err != nil {
		return err
	}
	for _, scale := range []float32{1, 2} {
		for _, cloak := range []bool{false, true} {
			opts := client.ModelPreviewOptions{Model: "armcom", Width: 192, Height: 160, Scale: scale, Background: 100, KeyPlane: true, Cloaked: cloak}
			classic, err := preview.RecordModel(opts)
			if err != nil {
				return err
			}
			modern, err := preview.RecordGeometry(opts)
			if err != nil {
				return err
			}
			renderer, err := NewChecked(modern.Palette, opts.Width, opts.Height)
			if err != nil {
				return err
			}
			img := renderer.Execute(&modern.List, opts.Width, opts.Height)
			if img == nil {
				return fmt.Errorf("stock cloak returned no device image")
			}
			if renderer.modelStats.GPU == 0 || renderer.modelStats.DirectOverflow != 0 {
				return fmt.Errorf("stock cloak did not use normal GPU model path")
			}
			pix := make([]byte, opts.Width*opts.Height*4)
			img.ReadPixels(pix)
			for label, pic := range map[string]*image.RGBA{"classic": classic.Image, "modern": {Pix: pix, Stride: opts.Width * 4, Rect: image.Rect(0, 0, opts.Width, opts.Height)}} {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("armcom-%s-cloak-%v-scale-%g.png", label, cloak, scale)))
				if err != nil {
					return err
				}
				err = png.Encode(f, pic)
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
	}
	return nil
}
