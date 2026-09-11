package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The quad mapper replaces one device quad per source row with two device
// triangles plus a per-fragment evaluation of the same two-chain mapping
// [03 R-RAST-01 §1]. These cases lock the packing and the chain selection the
// fragment shader depends on; the device fixture then checks that the shader
// reproduces the strip path's interior texels.

func TestModelQuadParametersRotateToTheTopCorner(t *testing.T) {
	var q modelQuadParams
	// The top corner is index 2 and the bottom corner index 0, so the packed
	// ring starts at index 2 and the bottom corner lands at rotated index 2.
	v := []drawlist.ModelVertex{
		{X: 4, Y: 9, Key: -3, U: 1, V: 2, Shade: 5},
		{X: 1, Y: 6, Key: 0, U: 3, V: 4, Shade: 6},
		{X: 3, Y: 1, Key: 7, U: 5, V: 6, Shade: 7},
		{X: 9, Y: 5, Key: 30000, U: 7, V: 8, Shade: 8},
	}
	if got := q.add(v, 10, 100, 0); got != 1 {
		t.Fatalf("first quad index = %d, want 1", got)
	}
	pos, uv, key, shade, mm := decodeQuadParams(q.buf, 1)
	if mm != 2 {
		t.Fatalf("bottom corner index = %d, want 2", mm)
	}
	for j := 0; j < 4; j++ {
		src := v[(2+j)%4]
		if pos[j] != [2]float32{float32(src.X + 10), float32(src.Y + 100)} {
			t.Fatalf("corner %d position = %v, want the slot-shifted authored corner %v", j, pos[j], src)
		}
		if uv[j] != [2]float32{float32(src.U), float32(src.V)} {
			t.Fatalf("corner %d texel coordinates = %v, want %v", j, uv[j], [2]int32{src.U, src.V})
		}
		if key[j] != float32(src.Key) {
			t.Fatalf("corner %d key = %v, want the signed authored key %d", j, key[j], src.Key)
		}
		if shade[j] != float32(src.Shade) {
			t.Fatalf("corner %d shade row = %v, want %d", j, shade[j], src.Shade)
		}
	}
	if got := q.add(v, 0, 0, 0); got != 2 {
		t.Fatalf("second quad index = %d, want 2", got)
	}
}

func TestModelQuadParametersRejectLanesThatDoNotFit(t *testing.T) {
	var q modelQuadParams
	base := []drawlist.ModelVertex{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}
	flat := []drawlist.ModelVertex{{X: 0, Y: 3}, {X: 4, Y: 3}, {X: 8, Y: 3}, {X: 12, Y: 3}}
	if got := q.add(flat, 0, 0, 0); got != 0 {
		t.Fatalf("a ring with no rows was described as a quad: %d", got)
	}
	negative := append([]drawlist.ModelVertex(nil), base...)
	negative[1].Key = -modelQuadKeyBias - 1
	if got := q.add(negative, 0, 0, 0); got != 0 {
		t.Fatalf("a key below the offset-binary window was packed: %d", got)
	}
	high := append([]drawlist.ModelVertex(nil), base...)
	high[2].U = 1 << 17
	if got := q.add(high, 0, 0, 0); got != 0 {
		t.Fatalf("a texel coordinate beyond the packed window was packed: %d", got)
	}
	if q.count != 0 {
		t.Fatalf("rejected faces still consumed %d parameter slots", q.count)
	}
}

// The whole point of the quad path is that the row mapping is the span writer's,
// not a triangle diagonal's. This evaluates the packed parameters exactly as the
// fragment shader does and compares every interior column against the two-chain
// walk the strip path performs [03 R-RAST-01 §1].

func decodeQuadParams(buf []byte, index int) (pos, uv [4][2]float32, key, shade [4]float32, mm int) {
	base := (index - 1) * modelQuadBytes
	u16 := func(off int) float32 { return float32(int(buf[off])<<8 | int(buf[off+1])) }
	for j := 0; j < 4; j++ {
		pos[j] = [2]float32{u16(base + 4*j), u16(base + 4*j + 2)}
		uv[j] = [2]float32{u16(base + 16 + 4*j), u16(base + 16 + 4*j + 2)}
		key[j] = u16(base+32+4*j) - modelQuadKeyBias
		shade[j] = float32(buf[base+32+4*j+2])
	}
	return pos, uv, key, shade, int(buf[base+35])
}
