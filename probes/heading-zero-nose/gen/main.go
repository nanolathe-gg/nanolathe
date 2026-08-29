// Command gen writes the heading-zero-nose probe's binary data.
//
// Run from probes/heading-zero-nose: `go run ./gen`. Outputs:
//
//	objects3d/ta_probe_xz.3do   the asymmetric probe model (below)
//	scripts/TA_PROBE_XZ.COB     minimal Create/Killed script naming the pieces
//	maps/ta_probe_xz.tnt        flat 2048×2048 px green map
//
// The model is the `ta_probe_xz` fixture doc 03 §2.4 names: a grey body
// with three coloured child pieces whose translations are each nonzero on
// exactly one source axis, so a screenshot of the unit at a known heading
// reads the sign of every axis directly:
//
//	nose  white   translation (0, 6, −24)  — source −Z, the model front
//	px    maroon  translation (+24, 6, 0)  — source +X
//	py    green   translation (0, +22, 0)  — source +Y (up; marks the origin)
//
// Sizes are world units (one unit ≈ one map pixel; a 2×2-cell footprint is
// 32 px). Piece geometry is authored in each piece's own space.
package main

import (
	"flag"
	"path/filepath"

	"github.com/nanolathe/nanolathe/probes/kit/author"
)

func main() {
	out := flag.String("out", ".", "probe directory to write into")
	flag.Parse()

	const (
		grey   = 7
		white  = 255
		maroon = 1
		green  = 2
	)
	root := &author.Piece{Name: "base", Selection: 0}
	root.Plate(16, 16, 0, grey) // primitive 0: selection/ground plate
	root.Box(author.Vec{X: -8, Y: 0, Z: -8}, author.Vec{X: 8, Y: 12, Z: 8}, grey)

	nose := &author.Piece{Name: "nose", Offset: author.Vec{X: 0, Y: 6, Z: -24}, Selection: -1}
	nose.Box(author.Vec{X: -3, Y: -3, Z: -8}, author.Vec{X: 3, Y: 3, Z: 8}, white)

	px := &author.Piece{Name: "px", Offset: author.Vec{X: 24, Y: 6, Z: 0}, Selection: -1}
	px.Box(author.Vec{X: -8, Y: -3, Z: -3}, author.Vec{X: 8, Y: 3, Z: 3}, maroon)

	py := &author.Piece{Name: "py", Offset: author.Vec{X: 0, Y: 22, Z: 0}, Selection: -1}
	py.Box(author.Vec{X: -3, Y: -10, Z: -3}, author.Vec{X: 3, Y: 10, Z: 3}, green)

	root.Children = []*author.Piece{nose, px, py}
	author.Must(author.WriteFile(filepath.Join(*out, "objects3d", "ta_probe_xz.3do"), author.ThreeDOBytes(root)))

	cob := author.COBBytes(author.MinimalScripts(), []string{"base", "nose", "px", "py"})
	author.Must(author.WriteFile(filepath.Join(*out, "scripts", "TA_PROBE_XZ.COB"), cob))

	t := author.NewTNT(128, 128, 20, author.SolidTile(green))
	author.Must(author.WriteFile(filepath.Join(*out, "maps", "ta_probe_xz.tnt"), t.Bytes()))
}
