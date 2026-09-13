package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Optional staged comparison: actual weapon definitions, GAF frames and holds,
// palette, and map tiles through the production GPU executor. It isolates the
// wave; it is not a simulated combat scene (GPU design §25.2).
func captureDynamicBlastExamples() error {
	dir := os.Getenv("NANOLATHE_DYNAMIC_BLAST_SHOTS")
	groundComparison := os.Getenv("NANOLATHE_GROUND_FLASH_SHOTS") != ""
	if groundComparison {
		dir = os.Getenv("NANOLATHE_GROUND_FLASH_SHOTS")
	}
	if dir == "" {
		return nil
	}
	root := os.Getenv(testsupport.RetailAssetsEnv)
	if root == "" {
		return fmt.Errorf("blast captures require retail assets")
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(root); err != nil {
		return err
	}
	pal, err := palette.Load(fs)
	if err != nil {
		return err
	}
	weapons, _, err := content.CompileWeaponsWithDuplicates(fs)
	if err != nil {
		return err
	}
	bank, err := formats.LoadGAFFile(fs, "anims/fx.gaf")
	if err != nil {
		return err
	}
	tnt, err := formats.LoadTNTFile(fs, "maps/Great Divide.tnt")
	if err != nil {
		return err
	}
	terrain := &world.Terrain{CellW: int32(tnt.Width), CellH: int32(tnt.Height), TileIndices: tnt.TileIndices, TileSet: make([][1024]byte, tnt.Tiles)}
	for i := range terrain.TileSet {
		copy(terrain.TileSet[i][:], tnt.TileGraphics[i*1024:(i+1)*1024])
	}
	const w, h = 560, 400
	r, err := NewChecked(pal, w, h)
	if err != nil {
		return err
	}
	cam := &camera.Camera{X: 2048, Z: 2048}
	for _, key := range []string{"arm_lightlaser", "arm_ham", "cor_gol", "big_unitex", "large_buildingex", "arm_berthacannon"} {
		weapon := weapons[key]
		if weapon == nil {
			return fmt.Errorf("missing weapon %s", key)
		}
		entry, ok := bank.Find(weapon.ExplosionArt)
		if !ok {
			return fmt.Errorf("missing art %s", weapon.ExplosionArt)
		}
		var size float32
		holds := make([]int32, len(entry.Frames))
		for i, ref := range entry.Frames {
			size = max(size, float32(max(ref.Frame.Width, ref.Frame.Height)))
			holds[i] = max(int32(ref.Value), 1)
		}
		player := render.EffectAnimPlayer{Active: true, Frames: len(holds), Durations: holds, Countdown: holds[0]}
		outDir := filepath.Join(dir, key)
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return err
		}
		fmt.Printf("blast example %s: art=%s size=%v AoE=%d damage=%d\n", key, entry.Name, size, weapon.AreaOfEffect, weapon.DamageDefault)
		for n := 0; n < 90; n++ {
			age := float32(n) / 2
			var list drawlist.List
			list.RecordClear()
			list.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit, Step: camera.ViewScaleNative, RecordW: w, RecordH: h})
			list.RecordTerrain(drawlist.Terrain{Terrain: terrain, Cam: cam, DstW: w, DstH: h, Scale: camera.ViewScaleNative})
			if player.Active {
				f := entry.Frames[player.Idx].Frame
				list.RecordSprite(drawlist.Sprite{Frame: f, X: w / 2, Y: h / 2, Kind: drawlist.BlitKeyed, Anchored: true, Emissive: true, LightingKind: drawlist.SpriteLightingExplosion, LightingScale: 1, LightingSize: size, LightingAge: age, HasLightingAge: true, BlastSize: size, BlastAge: age, HasBlastProfile: true, BlastAreaOfEffect: weapon.AreaOfEffect, BlastDamage: weapon.DamageDefault})
			}
			list.RecordWorld(drawlist.WorldSpace{})
			list.RecordExpand()
			sheet := image.NewRGBA(image.Rect(0, 0, w*2, h))
			var before []byte
			for col, on := range []bool{false, true} {
				if groundComparison {
					r.SetDynamicBlastDistortion(true)
					r.SetExplosionGroundFlash(on)
				} else {
					r.SetDynamicBlastDistortion(on)
				}
				pix := make([]byte, w*h*4)
				r.Execute(&list, w, h).ReadPixels(pix)
				if col == 0 {
					before = pix
				} else if !groundComparison && (size < 48 || age >= blastTicks) && !bytes.Equal(before, pix) {
					return fmt.Errorf("tiny/expired blast changed pixels: %s age=%v", key, age)
				}
				draw.Draw(sheet, image.Rect(col*w, 0, (col+1)*w, h), &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, image.Point{}, draw.Src)
			}
			f, err := os.Create(filepath.Join(outDir, fmt.Sprintf("frame-%03d.png", n)))
			if err != nil {
				return err
			}
			err = png.Encode(f, sheet)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if n%2 == 1 {
				player.Step()
			}
		}
	}
	return nil
}
