// Command gen writes the dense-pack-fringe probe's binary data.
//
// Run from probes/dense-pack-fringe: `go run ./gen`. Outputs:
//
//	anims/ta_probe_dense.gaf   three 32×32 solid sprites: blocka (maroon),
//	                           blockb (green), blockc (white)
//	maps/ta_probe_dense.tnt    flat 2048×2048 px map with three TNT-placed
//	                           2×2 features (anchor + three fringe cells)
//
// The TNT features are the *earlier* stamps; the OTA's [features] block
// (authored by hand next to this generator) stamps *later* through the
// same single stamping service ([03 §5.1.2]) so that its footprints
// overlap the TNT anchors and fringe cells in controlled ways. Layout, in
// attribute cells (16 px):
//
//	pair 1  TNT  ta_probe_dense_a   anchor (40,40)  destructible
//	pair 2  TNT  ta_probe_dense_c   anchor (60,40)  indestructible
//	pair 4  TNT  ta_probe_dense_a   anchor (40,60)  destructible
//
// The OTA places ta_probe_dense_b so that pair 1 and pair 2 have the later
// footprint covering the earlier *anchor* cell, pair 3 (cell 80,40) has no
// earlier feature at all (control), and pair 4 has the later footprint
// covering only an earlier *fringe* cell.
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
		grey   = 7
		maroon = 1
		green  = 2
		white  = 255
	)
	sprite := func(name string, color byte) author.GAFEntry {
		f := author.NewFrame(32, 32, 0, 0, 10)
		f.Fill(0, 0, 32, 32, color)
		return author.GAFEntry{Name: name, Frames: []author.GAFFrame{f}}
	}
	gaf := author.GAFBytes([]author.GAFEntry{
		sprite("blocka", maroon), sprite("blockb", green), sprite("blockc", white),
	})
	author.Must(author.WriteFile(filepath.Join(*out, "anims", "ta_probe_dense.gaf"), gaf))

	t := author.NewTNT(128, 128, 20, author.SolidTile(grey))
	a := t.AddFeature("ta_probe_dense_a")
	c := t.AddFeature("ta_probe_dense_c")
	t.StampFeature(a, 40, 40, 2, 2) // pair 1
	t.StampFeature(c, 60, 40, 2, 2) // pair 2 (indestructible)
	t.StampFeature(a, 40, 60, 2, 2) // pair 4 (fringe-only overlap)
	author.Must(author.WriteFile(filepath.Join(*out, "maps", "ta_probe_dense.tnt"), t.Bytes()))
}
