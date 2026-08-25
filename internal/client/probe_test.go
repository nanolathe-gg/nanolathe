package client

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/vfs"
)

// Temporary diagnostic: project every armcom triangle with the shot camera
// and report faces with extreme screen extents or covering the artifact.
func TestProbeArmcomTriangles(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		t.Skip("NANOLATHE_TA_ROOT not set")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Width: 640, Height: 480, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetModelFS(fs)
	c.cam = &camera.Camera{X: 3184, Z: 64, ViewW: 640, ViewH: 480, MapW: 3584, MapH: 3584}
	name := os.Getenv("PROBE_MODEL")
	if name == "" {
		name = "armcom"
	}
	m := c.unitModelFor(name)
	if m == nil {
		t.Fatalf("no model %q (load failed)", name)
	}
	textured, flat := 0, 0
	for i := range m.tris {
		if m.tris[i].frame != nil {
			textured++
		} else {
			flat++
		}
	}
	t.Logf("expanded %d tris: %d textured, %d flat", len(m.tris), textured, flat)
	v := snapshot.UnitView{Model: name, Owner: 0, X: 3506 << 16, Z: 172 << 16}
	pieceBounds := map[string][4]int32{}
	min := func(a, b int32) int32 {
		if a < b {
			return a
		}
		return b
	}
	max := func(a, b int32) int32 {
		if a > b {
			return a
		}
		return b
	}
	cos, sin := headingCosSin(v.Heading)
	ux, uy, uz := int32(v.X>>16), int32(v.Y>>16), int32(v.Z>>16)
	for i := range m.tris {
		tr := &m.tris[i]
		var xs, ys [3]int32
		for k := 0; k < 3; k++ {
			cn := &tr.c[k]
			rx := cos*cn.x - sin*cn.z
			rz := sin*cn.x + cos*cn.z
			wx := ux + int32(rx)
			wy := uy + int32(cn.y)
			wz := uz + int32(rz)
			xs[k] = wx - c.cam.X + camera.OriginX
			ys[k] = wz - (wy >> 1) - c.cam.Z + camera.OriginY
		}
		minX, maxX := xs[0], xs[0]
		minY, maxY := ys[0], ys[0]
		for k := 1; k < 3; k++ {
			if xs[k] < minX {
				minX = xs[k]
			}
			if xs[k] > maxX {
				maxX = xs[k]
			}
			if ys[k] < minY {
				minY = ys[k]
			}
			if ys[k] > maxY {
				maxY = ys[k]
			}
		}
		w, h := maxX-minX, maxY-minY
		// Faces covering the artifact region (right of the body).
		if minX <= 458 && maxX >= 452 && minY <= 152 && maxY >= 146 {
			t.Logf("HIT piece=%q tex=%v color=%d screen (%d,%d)..(%d,%d) nverts-model(%.1f,%.1f,%.1f)",
				tr.piece, tr.frame != nil, tr.color, minX, minY, maxX, maxY, tr.c[0].x, tr.c[0].y, tr.c[0].z)
		}
		if tr.frame != nil {
			b := pieceBounds[tr.piece]
			if b[2] == 0 && b[3] == 0 {
				pieceBounds[tr.piece] = [4]int32{minX, minY, maxX, maxY}
			} else {
				pieceBounds[tr.piece] = [4]int32{min(b[0], minX), min(b[1], minY), max(b[2], maxX), max(b[3], maxY)}
			}
		}
		if w > 25 || h > 25 {
			t.Logf("BIG piece=%q tex=%v color=%d screen (%d,%d)..(%d,%d) %dx%d",
				tr.piece, tr.frame != nil, tr.color, minX, minY, maxX, maxY, w, h)
			for k := 0; k < 3; k++ {
				t.Logf("   v%d model(%.2f,%.2f,%.2f) screen(%d,%d)", k, tr.c[k].x, tr.c[k].y, tr.c[k].z, xs[k], ys[k])
			}
		}
	}
	for name, b := range pieceBounds {
		t.Logf("piece %-12q screen bbox (%d,%d)..(%d,%d)", name, b[0], b[1], b[2], b[3])
	}
	_ = formats.LoadThreeDO
}
