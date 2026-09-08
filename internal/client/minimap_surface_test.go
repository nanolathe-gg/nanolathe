package client

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/render"
)

// Preserve the former point producer as the regression oracle for the two
// truncating divisions and untouched bars [03 R-MM-01 §1].
func referenceMinimapPoints(c *Client, surf *render.RadarSurface, dst hud.Rect, layout camera.Minimap) {
	dl, dt, dr, db := dst.Ordered()
	dw, dh := dr-dl+1, db-dt+1
	off := len(c.pointArena)
	for y := int32(0); y < dh; y++ {
		cy := y * camera.MinimapLongSide / dh
		if cy < layout.PadY || cy > layout.Bottom() {
			continue
		}
		sy := (cy - layout.PadY) * int32(surf.H) / layout.H
		for x := int32(0); x < dw; x++ {
			cx := x * camera.MinimapLongSide / dw
			if cx < layout.PadX || cx > layout.Right() {
				continue
			}
			sx := (cx - layout.PadX) * int32(surf.W) / layout.W
			c.appendMinimapPoint(dl+x, dt+y, surf.Bits[int(sy)*surf.W+int(sx)])
		}
	}
	c.emitPoints(off, drawlist.PointPlain)
}

type minimapSurfaceObserver struct {
	drawlist.Sink
	surfaces []drawlist.Surface
	points   int
}

func (s *minimapSurfaceObserver) Surface(v drawlist.Surface) {
	s.surfaces = append(s.surfaces, v)
	s.Sink.Surface(v)
}
func (s *minimapSurfaceObserver) Points(v drawlist.Points) { s.points++; s.Sink.Points(v) }

func TestMinimapSurfacePreservesSamplingAndOwnership(t *testing.T) {
	for _, aspect := range [][2]int32{{512, 512}, {1024, 384}, {384, 1024}} {
		for _, dst := range []hud.Rect{{X1: 7, Y1: 9, X2: 132, Y2: 134}, {X1: 8, Y1: 5, X2: 90, Y2: 71}, {X1: -17, Y1: -9, X2: 151, Y2: 144}, {X1: 91, Y1: 77, X2: 4, Y2: 3}, {X1: 160, Y1: 0, X2: 180, Y2: 20}} {
			t.Run(fmt.Sprint(aspect, dst), func(t *testing.T) {
				const w, h = 144, 140
				fresh := func() *Client { return &Client{width: w, height: h, indexed: bytes.Repeat([]byte{251}, w*h)} }
				got, want := fresh(), fresh()
				surf := &render.RadarSurface{W: 19, H: 13, Bits: make([]byte, 19*13)}
				for i := range surf.Bits {
					surf.Bits[i] = byte(i % 239)
				}
				layout := camera.LayoutMinimap(aspect[0], aspect[1])
				referenceMinimapPoints(want, surf, dst, layout)
				want.replayForTest()
				got.DrawMinimapLayout(surf, dst, layout)
				saved := got.list.Clone()
				clear(surf.Bits)
				observer := &minimapSurfaceObserver{Sink: got.classicSink()}
				got.list.Replay(observer)
				if !bytes.Equal(got.indexed, want.indexed) {
					t.Fatal("surface changed sampling, zero pixels, bars or clipping")
				}
				if observer.points != 0 || len(observer.surfaces) > 1 {
					t.Fatal("picture expanded into point commands")
				}
				if !bytes.Equal(want.indexed, bytes.Repeat([]byte{251}, w*h)) && len(observer.surfaces) != 1 {
					t.Fatal("visible picture has no surface")
				}
				if len(observer.surfaces) > 0 {
					clear(observer.surfaces[0].Pixels)
				}
				copy(got.indexed, bytes.Repeat([]byte{251}, w*h))
				saved.Replay(got.classicSink())
				if !bytes.Equal(got.indexed, want.indexed) {
					t.Fatal("retained surface aliases original packet")
				}
				if path := os.Getenv("NANOLATHE_MINIMAP_SURFACE_SHOT"); path != "" && aspect == [2]int32{1024, 384} && dst.X1 == 8 {
					f, err := os.Create(path)
					if err != nil {
						t.Fatal(err)
					}
					img := &image.Gray{Pix: got.indexed, Stride: w, Rect: image.Rect(0, 0, w, h)}
					err = png.Encode(f, img)
					closeErr := f.Close()
					if err != nil {
						t.Fatal(err)
					}
					if closeErr != nil {
						t.Fatal(closeErr)
					}
				}
			})
		}
	}
}

func BenchmarkMinimapPictureSubmission(b *testing.B) {
	surf := &render.RadarSurface{W: 126, H: 126, Bits: make([]byte, 126*126)}
	layout := camera.LayoutMinimap(1024, 1024)
	dst := hud.Rect{X2: 125, Y2: 125}
	for _, legacy := range []bool{true, false} {
		b.Run(fmt.Sprint("points=", legacy), func(b *testing.B) {
			c := &Client{width: 126, height: 126}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.list.Reset()
				c.pointArena = c.pointArena[:0]
				c.surfaceArena = c.surfaceArena[:0]
				if legacy {
					referenceMinimapPoints(c, surf, dst, layout)
				} else {
					c.DrawMinimapLayout(surf, dst, layout)
				}
			}
		})
	}
}
