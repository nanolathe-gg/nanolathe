// Command gen writes anims/fog.gaf for the fog-corner-gaf probe.
//
// Run from probes/fog-corner-gaf: `go run ./gen`.
//
// The stock fog.gaf carries eight entries (Black1..Black4, Gray1..Gray4)
// of fourteen raw frames each; frame n is drawn for fog-cache nibble value
// n+1 and its signed frame offsets select which 16×16 quarter(s) of the
// 32×32 fog cell the frame covers ([03 R-RR16-A §3], [03 R-FX-01 §1]).
// This file keeps that geometry exactly but replaces every quarter with a
// distinct glyph so a screenshot shows which nibble bit painted which
// quarter:
//
//	bit 1 → quarter at (0,0)   solid square
//	bit 2 → quarter at (16,0)  hollow square (3-px ring)
//	bit 4 → quarter at (0,16)  diagonal stripes
//	bit 8 → quarter at (16,16) 4×4 checkerboard
//
// Cloud pixels are palette index 0 and transparent pixels the colour key 9,
// as in every shipped frame.
package main

import (
	"flag"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/probes/kit/author"
)

func main() {
	out := flag.String("out", ".", "probe directory to write into")
	flag.Parse()

	const cloud = 0
	type quarter struct {
		bit  int
		x, y int
		draw func(f *author.GAFFrame, ox, oy int)
	}
	quarters := []quarter{
		{1, 0, 0, func(f *author.GAFFrame, ox, oy int) { f.Fill(ox, oy, ox+16, oy+16, cloud) }},
		{2, 16, 0, func(f *author.GAFFrame, ox, oy int) {
			f.Fill(ox, oy, ox+16, oy+16, cloud)
			f.Fill(ox+3, oy+3, ox+13, oy+13, author.GAFColorKey)
		}},
		{4, 0, 16, func(f *author.GAFFrame, ox, oy int) {
			for y := 0; y < 16; y++ {
				for x := 0; x < 16; x++ {
					if (x+y)%4 < 2 {
						f.Set(ox+x, oy+y, cloud)
					}
				}
			}
		}},
		{8, 16, 16, func(f *author.GAFFrame, ox, oy int) {
			for y := 0; y < 16; y++ {
				for x := 0; x < 16; x++ {
					if ((x/4)+(y/4))%2 == 0 {
						f.Set(ox+x, oy+y, cloud)
					}
				}
			}
		}},
	}

	frames := make([]author.GAFFrame, 0, 14)
	for value := 1; value <= 14; value++ {
		minX, minY, maxX, maxY := 32, 32, 0, 0
		for _, q := range quarters {
			if value&q.bit == 0 {
				continue
			}
			minX, minY = min(minX, q.x), min(minY, q.y)
			maxX, maxY = max(maxX, q.x+16), max(maxY, q.y+16)
		}
		// Destination is (x − XOffset, y − YOffset): a quarter at (16,0)
		// needs XOffset −16, matching the stock frames.
		f := author.NewFrame(maxX-minX, maxY-minY, int16(-minX), int16(-minY), 10)
		for _, q := range quarters {
			if value&q.bit != 0 {
				q.draw(&f, q.x-minX, q.y-minY)
			}
		}
		frames = append(frames, f)
	}

	var entries []author.GAFEntry
	for _, name := range []string{"Black1", "Black2", "Black3", "Black4", "Gray1", "Gray2", "Gray3", "Gray4"} {
		entries = append(entries, author.GAFEntry{Name: name, Frames: frames})
	}
	author.Must(author.WriteFile(filepath.Join(*out, "anims", "fog.gaf"), author.GAFBytes(entries)))
}
