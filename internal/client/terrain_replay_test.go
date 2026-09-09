package client

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A retained terrain command must not follow the next frame's live camera.
// Exercise the classic executor, including its zoom and nil-camera branches.
func TestClonedTerrainSurvivesNextCamera(t *testing.T) {
	terrain := &world.Terrain{CellW: 4, CellH: 4, TileIndices: []uint16{0, 0, 0, 0}, TileSet: make([][1024]byte, 1)}
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			terrain.TileSet[0][y*32+x] = byte(1 + x + 3*y)
		}
	}
	for _, scale := range []int32{0, 1, 2} {
		t.Run(map[int32]string{0: "nil", 1: "native", 2: "detail"}[scale], func(t *testing.T) {
			c := &Client{width: 32, height: 24, indexed: make([]byte, 32*24)}
			var cam *camera.Camera
			if scale != 0 {
				cam = &camera.Camera{X: 3, Z: 5, Scale: scale}
			}
			c.list.RecordTerrain(drawlist.Terrain{Terrain: terrain, Cam: cam, DstW: 32, DstH: 24})
			c.list.Replay(c.classicSink())
			want := append([]byte(nil), c.indexed...)
			saved := c.list.Clone()
			if cam != nil {
				cam.X, cam.Z, cam.Scale = 17, 11, 1
			}
			c.list.Reset()
			c.list.RecordTerrain(drawlist.Terrain{Terrain: terrain, Cam: &camera.Camera{X: 17, Z: 11}, DstW: 32, DstH: 24})
			c.list.Replay(c.classicSink())
			if bytes.Equal(c.indexed, want) {
				t.Fatal("fixture B did not change terrain pixels")
			}
			saved.Replay(c.classicSink())
			if !bytes.Equal(c.indexed, want) {
				t.Fatal("cloned A followed B's camera")
			}
			if path := os.Getenv("NANOLATHE_CAMERA_REPLAY_SHOT"); path != "" && scale == 2 {
				img := image.NewGray(image.Rect(0, 0, 32, 24))
				for y := 0; y < 24; y++ {
					for x := 0; x < 32; x++ {
						img.SetGray(x, y, color.Gray{Y: c.indexed[y*32+x]})
					}
				}
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, img); err != nil {
					f.Close()
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
