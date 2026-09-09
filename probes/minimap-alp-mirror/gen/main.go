// Command gen writes the minimap-alp-mirror probe's binary data.
//
// Run from probes/minimap-alp-mirror: `go run ./gen`. Outputs:
//
//	maps/ta_probe_alp.tnt        2048×2048 px map, no embedded minimap,
//	                             four solid quadrant colours plus a white
//	                             marker block inside the north-west quadrant
//	palettes-left/PALETTE.ALP    ALP[a][b] = a  (left operand wins)
//	palettes-right/PALETTE.ALP   ALP[a][b] = b  (right operand wins)
//
// With the minimap-present bit clear the radar picture is generated from
// the tiles through the 2× supersample and the three-lookup ALP blend
// ([03 §3.7]); a table that returns one operand turns that blend into a
// pure subsample, so the radar reproduces the quadrant layout without
// colour mixing and the question of screen orientation is answered by
// where the white block lands.
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
		green  = 2 // north-west
		navy   = 4 // north-east
		olive  = 3 // south-west
		maroon = 1 // south-east
		white  = 255
	)
	// 128×128 attribute cells = 64×64 tiles = 2048×2048 px; flat height 20.
	t := author.NewTNT(128, 128, 20, author.SolidTile(green))
	tNavy := t.AddTile(author.SolidTile(navy))
	tOlive := t.AddTile(author.SolidTile(olive))
	tMaroon := t.AddTile(author.SolidTile(maroon))
	tWhite := t.AddTile(author.SolidTile(white))
	t.FillTiles(32, 0, 64, 32, tNavy)
	t.FillTiles(0, 32, 32, 64, tOlive)
	t.FillTiles(32, 32, 64, 64, tMaroon)
	// White 8×8-tile marker block near the north-west corner, offset from
	// the corner so it cannot be confused with the map border.
	t.FillTiles(4, 4, 12, 12, tWhite)
	author.Must(author.WriteFile(filepath.Join(*out, "maps", "ta_probe_alp.tnt"), t.Bytes()))

	left := author.ALPBytes(func(a, b byte) byte { return a })
	right := author.ALPBytes(func(a, b byte) byte { return b })
	author.Must(author.WriteFile(filepath.Join(*out, "palettes-left", "PALETTE.ALP"), left))
	author.Must(author.WriteFile(filepath.Join(*out, "palettes-right", "PALETTE.ALP"), right))
}
