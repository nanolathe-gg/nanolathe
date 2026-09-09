package client

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestNanoOriginMatchesDrawnPiece is the visual and numeric evidence for the
// nanolathe emitter origin. It draws a stock builder's model through the
// production composition path and paints the world points that
// construction.Service.QueryNanoPiece returns; a correct origin lands on the
// nano pieces the model pass drew, a mirrored Z lands the same distance on the
// wrong side of the unit [05 R-P0-06 §2][03 R-RAST-01 §2].
//
// It also locks the emitter count: the stock scripts alternate their spray
// piece per call, so consecutive queries must not return the same piece for a
// two-emitter builder [05 R-P0-06 §2].
func TestNanoOriginMatchesDrawnPiece(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fs.Close()
	pal, err := palette.Load(fs)
	if err != nil {
		t.Skipf("palette unavailable: %v", err)
	}

	for _, unit := range []string{"armap", "armack"} {
		t.Run(unit, func(t *testing.T) {
			mdl, err := compiledmodel.Load(fs, "objects3d/"+unit+".3do")
			if err != nil {
				t.Skipf("%s model unavailable: %v", unit, err)
			}
			names := make([]string, len(mdl.Pieces))
			for i, p := range mdl.Pieces {
				names[i] = p.Name
			}
			binding, err := cob.BindStrict(fs, cob.BindingRequest{UnitName: unit, Model: mdl, ModelPieces: names})
			if err != nil {
				t.Skipf("%s script unavailable: %v", unit, err)
			}

			const originX, originY, originZ = 320, 0, 240
			u := &units.Unit{
				X: numeric.Fixed(originX * 65536),
				Y: numeric.Fixed(originY * 65536),
				Z: numeric.Fixed(originZ * 65536),
			}
			u.ScriptState = &units.ScriptState{VM: binding.VM, Binding: binding}
			svc := &construction.Service{}

			type emitter struct {
				piece   int32
				x, y, z numeric.Fixed
			}
			var emitters []emitter
			for i := 0; i < 4; i++ {
				piece, src, ok := svc.QueryNanoPiece(u)
				if !ok {
					t.Fatalf("%s: QueryNanoPiece call %d refused", unit, i)
				}
				emitters = append(emitters, emitter{piece, src.X(), src.Y(), src.Z()})
			}
			// Retail emits one segment per accepted work step and the script
			// picks the piece, so a two-emitter builder alternates
			// [05 R-P0-06 §2][05 R-WORK-01 §8].
			if emitters[0].piece == emitters[1].piece {
				t.Errorf("%s: consecutive nano queries both returned piece %d; the script's alternation is not reaching the emitter", unit, emitters[0].piece)
			}
			if emitters[0].piece != emitters[2].piece || emitters[1].piece != emitters[3].piece {
				t.Errorf("%s: nano piece rotation is not stable: %d %d %d %d", unit,
					emitters[0].piece, emitters[1].piece, emitters[2].piece, emitters[3].piece)
			}
			// The emitter sits above the unit origin: an origin that landed at
			// ground level is the defect this test exists for.
			for i, e := range emitters[:2] {
				if e.y <= u.Y {
					t.Errorf("%s: emitter %d Y %d is not above the unit origin %d", unit, i, int64(e.y)>>16, int64(u.Y)>>16)
				}
			}

			c := newTestClient(t)
			c.SetPalette(pal)
			c.SetModelFS(fs)
			c.cam = &camera.Camera{X: originX - 320, Z: originZ - 240, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
			for i := range c.indexed {
				c.indexed[i] = 0
			}
			view := frame.UnitView{Slot: 1, Model: unit, X: u.X, Y: u.Y, Z: u.Z}
			sx, sy := c.cam.WorldToScreen(view.X, view.Y, view.Z)
			if !c.drawUnitModel(view, sx, sy) {
				t.Fatalf("%s: model did not compose", unit)
			}
			// Paint a cross at every emitter origin through the same projection
			// the nano particles use.
			for i, e := range emitters[:2] {
				ex, ey := c.cam.WorldToScreen(e.x, e.y, e.z)
				colour := uint8(0xa3)
				if i == 1 {
					colour = 0xa6
				}
				for d := -6; d <= 6; d++ {
					c.fillIndexedRect(int(ex-camera.OriginX)+d, int(ey-camera.OriginY), 1, 1, colour)
					c.fillIndexedRect(int(ex-camera.OriginX), int(ey-camera.OriginY)+d, 1, 1, colour)
				}
				t.Logf("%s emitter %d piece=%d world=(%d,%d,%d) screen=(%d,%d)", unit, i, e.piece,
					int64(e.x)>>16, int64(e.y)>>16, int64(e.z)>>16, ex, ey)
			}
			out := os.Getenv("NANOLATHE_SHOT_DIR")
			if out == "" {
				return
			}
			img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
			c.convertIndexedToRGBA()
			copy(img.Pix, c.rgba)
			f, err := os.Create(out + "/nano-origin-" + unit + ".png")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			defer f.Close()
			if err := png.Encode(f, img); err != nil {
				t.Fatalf("encode: %v", err)
			}
		})
	}
}
