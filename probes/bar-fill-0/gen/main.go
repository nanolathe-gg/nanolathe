// Command gen writes maps/ta_probe_bars.tnt for the bar-fill-0 probe.
//
// Run from probes/bar-fill-0: `go run ./gen`.
//
// A tall 1024×4096 px map (64×256 cells, height 20) with no embedded
// minimap, so the radar picture is generated from the tiles. Its play area
// is 992×3968 px, taller than wide, so the radar lens is RadarW =
// 992·126/3968 = 31 by RadarH = 126 with originX = (126−31)/2 = 47
// ([03 §3.7] "Aspect and letterbox"): 47 columns of letterbox bar on each
// side of the picture. The tile art is a white/black 4-tile checkerboard
// so the picture itself is unmistakable next to the bars.
package main

import (
	"flag"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/probes/kit/author"
)

func main() {
	out := flag.String("out", ".", "probe directory to write into")
	flag.Parse()

	const (
		white = 255
		grey  = 7
	)
	t := author.NewTNT(64, 256, 20, author.SolidTile(white))
	tGrey := t.AddTile(author.SolidTile(grey))
	for ty := 0; ty < 128; ty++ {
		for tx := 0; tx < 32; tx++ {
			if ((tx/4)+(ty/4))%2 == 1 {
				t.SetTile(tx, ty, tGrey)
			}
		}
	}
	author.Must(author.WriteFile(filepath.Join(*out, "maps", "ta_probe_bars.tnt"), t.Bytes()))
}
