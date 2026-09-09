// Command gen writes maps/ta_probe_edge.tnt for the map-edge-capture probe.
//
// Run from probes/map-edge-capture: `go run ./gen`.
//
// A flat 2048×2048 px map (128×128 cells, height 20) whose tile art
// colour-codes every band the engine treats differently at the edges
// ([03 R-TERR-01 §2] void strips; [03 §2.2] play insets), so a screenshot
// taken with the camera against an edge shows which band each pixel
// belongs to:
//
//	interior                          green   (2)
//	east: cells 120–121, authored     purple  (5)  attribute feature word 0xFFFC
//	      in-map void band
//	east: cells 122–125               teal    (6)  ordinary cells past the void band
//	east: cells 126–127               olive   (3)  the engine's own right-column
//	                                              void strip (rule 2)
//	north: cell row 0                 teal    (6)  voided by the north rule at
//	                                              height 20 (row 0 only)
//	south: cell rows 120–127          maroon  (1)  the south rule voids rows
//	                                              H−2..H−8 at height 20; the
//	                                              bottom row itself is never voided
//
// PlayRight is 2048−32 = 2016 px (cell 126) and PlayBottom 2048−128 =
// 1920 px (cell 120), so the camera clamp stops at the olive/maroon bands.
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
		green  = 2
		purple = 5
		teal   = 6
		olive  = 3
		maroon = 1
	)
	t := author.NewTNT(128, 128, 20, author.SolidTile(green))
	tPurple := t.AddTile(author.SolidTile(purple))
	tTeal := t.AddTile(author.SolidTile(teal))
	tOlive := t.AddTile(author.SolidTile(olive))
	tMaroon := t.AddTile(author.SolidTile(maroon))

	// Tiles are 2×2 cells; tile column = cell/2.
	t.FillTiles(60, 0, 61, 64, tPurple) // cells 120–121
	t.FillTiles(61, 0, 63, 64, tTeal)   // cells 122–125
	t.FillTiles(63, 0, 64, 64, tOlive)  // cells 126–127
	t.FillTiles(0, 0, 64, 1, tTeal)     // cell rows 0–1 (row 0 is voided)
	t.FillTiles(0, 60, 64, 64, tMaroon) // cell rows 120–127

	for cz := 0; cz < 128; cz++ {
		for cx := 120; cx <= 121; cx++ {
			t.Cell(cx, cz).Feature = author.VoidCell
		}
	}
	author.Must(author.WriteFile(filepath.Join(*out, "maps", "ta_probe_edge.tnt"), t.Bytes()))
}
