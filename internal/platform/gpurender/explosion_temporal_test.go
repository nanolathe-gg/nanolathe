package gpurender

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func loadTemporalExplosion(root string) (*palette.Tables, *formats.GAFEntry, float32, error) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		return nil, nil, 0, err
	}
	defer fs.Close()
	pal, err := palette.Load(fs)
	if err != nil {
		return nil, nil, 0, err
	}
	bank, err := formats.LoadGAFFile(fs, "anims/fx.gaf")
	if err != nil {
		return nil, nil, 0, err
	}
	entry, ok := bank.Find("Explosion")
	if !ok {
		return nil, nil, 0, fmt.Errorf("installed fx/Explosion missing")
	}
	var extent uint16
	for _, ref := range entry.Frames {
		extent = max(extent, ref.Frame.Width, ref.Frame.Height)
	}
	return pal, entry, float32(extent), nil
}

// Enhanced policy, not retail lighting: a growing fireball cannot delay its
// illumination until the bright art has faded (GPU design §23.2, §31.3).
// Height 79 occurs on the diagnostic scene's ground. The installed art's
// opening flash used to have a radius smaller than that elevation.
func TestExplosionLightingFollowsInstalledFlash(t *testing.T) {
	pal, entry, size, err := loadTemporalExplosion(testsupport.RetailRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, scale := range []float32{1, 2} {
		r := &Renderer{displayPalette: pal.Base}
		var list drawlist.List
		frames := make([]*formats.GAFFrame, len(entry.Frames))
		for i, ref := range entry.Frames {
			frames[i] = ref.Frame
			if scale == 2 {
				frames[i] = frames[i].Doubled()
			}
		}
		var early, late float32
		for _, i := range []int{2, 16, 2} { // Revisit the cached bright frame after the tail.
			f := frames[i]
			list.Reset()
			list.RecordSprite(drawlist.Sprite{Frame: f, LightingKind: drawlist.SpriteLightingExplosion, LightingScale: scale, LightingSize: size, WorldHeight: 79 * scale})
			r.prepareBattleLighting(&list)
			if len(r.lighting.lights) != 1 {
				t.Fatalf("frame %d: missing light", i)
			}
			l := r.lighting.lights[0]
			strength := l.color[0] * max(1-l.position[2]*l.position[2]/(l.radius*l.radius), 0)
			if i == 2 {
				early = strength
			} else {
				late = strength
			}
		}
		if early <= late || late <= 0 {
			t.Fatalf("scale %v: opening flash %v must exceed trailing light %v", scale, early, late)
		}
		list.Reset()
		r.prepareBattleLighting(&list)
		if len(r.lighting.lights) != 0 {
			t.Fatal("retired animation retained illumination")
		}
	}
}

// Run inside the existing hidden device loop. This sequence uses the installed
// art and palette, rendering every authored frame in order. Captures are
// optional and remain outside the repository.
func checkExplosionTemporalDevicePixels() error {
	root := os.Getenv(testsupport.RetailAssetsEnv)
	if root == "" {
		root = os.Getenv(testsupport.RetailAssetsEnvLegacy)
	}
	if root == "" {
		return nil
	}
	pal, entry, size, err := loadTemporalExplosion(root)
	if err != nil {
		return err
	}
	capture := os.Getenv("NANOLATHE_EXPLOSION_SHOTS")
	for _, scale := range []int{1, 2} {
		w, h := 320*scale, 200*scale
		r, err := NewChecked(pal, w, h)
		if err != nil {
			return err
		}
		step := camera.ViewScaleNative
		if scale == 2 {
			step = camera.ViewScaleDetail
		}
		terrain := groundFixtureTerrain(40)
		terrain.CellW, terrain.CellH = 24, 16
		terrain.TileIndices = make([]uint16, 12*8)
		sheet := image.NewRGBA(image.Rect(0, 0, w*7, h*2))
		var early, late int
		selected := []int{0, 2, 7, 12, 16, 20, 22}
		for i, ref := range entry.Frames {
			f := ref.Frame
			if scale == 2 {
				f = f.Doubled()
			}
			var list drawlist.List
			list.RecordClear()
			list.RecordTerrain(drawlist.Terrain{Terrain: terrain, Cam: &camera.Camera{}, DstW: int32(w), DstH: int32(h), Scale: step})
			sp := drawlist.Sprite{Frame: f, X: int32(160 * scale), Y: int32(100 * scale), Kind: drawlist.BlitKeyed, Anchored: true, LightingKind: drawlist.SpriteLightingExplosion, LightingScale: float32(scale), LightingSize: size, WorldHeight: 79 * float32(scale)}
			list.RecordSprite(sp)
			list.RecordExpand()
			pixels := make([]byte, w*h*4)
			r.Execute(&list, w, h).ReadPixels(pixels)
			// Outside the art, but in the ground pool, so source overdraw cannot
			// hide the timing difference. Ground at the same pixel without light
			// is invariant across the sequence.
			sample := int(pixels[((116*scale)*w+120*scale)*4])
			if i == 2 {
				early = sample
			}
			if i == 16 {
				late = sample
			}
			if capture != "" {
				for col, frame := range selected {
					if i == frame {
						draw.Draw(sheet, image.Rect(col*w, 0, (col+1)*w, h), &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, image.Point{}, draw.Src)
					}
				}
				// Same art with no sequence metadata reproduces the preceding radius.
				list.Reset()
				list.RecordClear()
				list.RecordTerrain(drawlist.Terrain{Terrain: terrain, Cam: &camera.Camera{}, DstW: int32(w), DstH: int32(h), Scale: step})
				sp.LightingSize = 0
				list.RecordSprite(sp)
				list.RecordExpand()
				r.Execute(&list, w, h).ReadPixels(pixels)
				for col, frame := range selected {
					if i == frame {
						draw.Draw(sheet, image.Rect(col*w, h, (col+1)*w, h*2), &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, image.Point{}, draw.Src)
					}
				}
			}
		}
		if early <= late {
			return fmt.Errorf("scale %d: device opening flash %d must exceed tail %d", scale, early, late)
		}
		if capture != "" {
			if err := os.MkdirAll(capture, 0755); err != nil {
				return err
			}
			out, err := os.Create(filepath.Join(capture, fmt.Sprintf("explosion-sequence-%dx.png", scale)))
			if err != nil {
				return err
			}
			err = png.Encode(out, sheet)
			closeErr := out.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}
