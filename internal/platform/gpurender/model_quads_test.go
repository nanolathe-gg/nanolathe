package gpurender

import (
	"fmt"
	"image"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
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
	if got := q.add(v, 10, 100); got != 1 {
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
	if got := q.add(v, 0, 0); got != 2 {
		t.Fatalf("second quad index = %d, want 2", got)
	}
}

func TestModelQuadParametersRejectLanesThatDoNotFit(t *testing.T) {
	var q modelQuadParams
	base := []drawlist.ModelVertex{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}
	flat := []drawlist.ModelVertex{{X: 0, Y: 3}, {X: 4, Y: 3}, {X: 8, Y: 3}, {X: 12, Y: 3}}
	if got := q.add(flat, 0, 0); got != 0 {
		t.Fatalf("a ring with no rows was described as a quad: %d", got)
	}
	negative := append([]drawlist.ModelVertex(nil), base...)
	negative[1].Key = -modelQuadKeyBias - 1
	if got := q.add(negative, 0, 0); got != 0 {
		t.Fatalf("a key below the offset-binary window was packed: %d", got)
	}
	high := append([]drawlist.ModelVertex(nil), base...)
	high[2].U = 1 << 17
	if got := q.add(high, 0, 0); got != 0 {
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
func TestModelQuadMappingMatchesTheTwoChainWalk(t *testing.T) {
	tex := &formats.GAFFrame{Width: 8, Height: 8}
	faces := map[string]drawlist.ModelFace{
		"skewed": {Texture: tex, Vertices: []drawlist.ModelVertex{
			{X: 2, Y: 1, U: 0, V: 0, Key: 20, Shade: 3},
			{X: 10, Y: 3, U: 7, V: 0, Key: 26, Shade: 9},
			{X: 7, Y: 9, U: 7, V: 7, Key: 31, Shade: 17},
			{X: 0, Y: 7, U: 0, V: 7, Key: 24, Shade: 11},
		}},
		"horizontal top edge": {Texture: tex, Vertices: []drawlist.ModelVertex{
			{X: 1, Y: 0, U: 0, V: 0, Key: 5, Shade: 1},
			{X: 9, Y: 0, U: 7, V: 0, Key: 9, Shade: 2},
			{X: 8, Y: 6, U: 7, V: 7, Key: 12, Shade: 3},
			{X: 2, Y: 5, U: 0, V: 7, Key: 7, Shade: 4},
		}},
		"top corner is not index zero": {Texture: tex, Vertices: []drawlist.ModelVertex{
			{X: 11, Y: 6, U: 7, V: 0, Key: 60, Shade: 20},
			{X: 3, Y: 9, U: 7, V: 7, Key: 61, Shade: 21},
			{X: 0, Y: 6, U: 0, V: 7, Key: 62, Shade: 22},
			{X: 6, Y: 1, U: 0, V: 0, Key: 63, Shade: 23},
		}},
		"concave": {Texture: tex, Vertices: []drawlist.ModelVertex{
			{X: 1, Y: 1, U: 0, V: 0, Key: 100, Shade: 5},
			{X: 12, Y: 4, U: 7, V: 0, Key: 104, Shade: 6},
			{X: 5, Y: 6, U: 7, V: 7, Key: 108, Shade: 7},
			{X: 2, Y: 11, U: 0, V: 7, Key: 112, Shade: 8},
		}},
	}
	for name, f := range faces {
		t.Run(name, func(t *testing.T) {
			if polygonCrosses(f.Vertices) {
				t.Fatal("fixture must be a simple ring")
			}
			var q modelQuadParams
			index := q.add(f.Vertices, 3, 5)
			if index == 0 {
				t.Fatal("fixture was rejected by the parameter packer")
			}
			rows := 0
			for _, s := range modelSpanStrips(f) {
				rows++
				row := int(s.Vertices[0].Y)
				xl, xr := int(s.Vertices[0].X), int(s.Vertices[1].X)
				left, okL := chainAt(f.Vertices, spanTop(f.Vertices), spanBottom(f.Vertices), -1, int32(row))
				right, okR := chainAt(f.Vertices, spanTop(f.Vertices), spanBottom(f.Vertices), 1, int32(row))
				if !okL || !okR {
					t.Fatalf("row %d has no two-chain span", row)
				}
				for c := xl; c < xr; c++ {
					t2 := float32(c-xl) / float32(xr-xl)
					wantU := left.U + (right.U-left.U)*t2
					wantV := left.V + (right.V-left.V)*t2
					wantKey := left.Key + (right.Key-left.Key)*t2
					wantShade := left.Shade + (right.Shade-left.Shade)*t2
					u, v, key, shade := modelQuadLanesReference(q.buf, index, c+3, row+5)
					if u != wantU || v != wantV || key != wantKey || shade != wantShade {
						t.Fatalf("row %d column %d = (u %v, v %v, key %v, shade %v), want the span writer's (%v, %v, %v, %v)",
							row, c, u, v, key, shade, wantU, wantV, wantKey, wantShade)
					}
				}
			}
			if rows == 0 {
				t.Fatal("fixture painted no rows")
			}
		})
	}
}

func TestModelQuadShadersCompile(t *testing.T) {
	if _, err := newModelKeyShader(); err != nil {
		t.Fatalf("key pass: %v", err)
	}
	if _, err := newModelBodyShader(); err != nil {
		t.Fatalf("colour pass: %v", err)
	}
}

func spanTop(v []drawlist.ModelVertex) int {
	top := 0
	for i := 1; i < len(v); i++ {
		if v[i].Y < v[top].Y {
			top = i
		}
	}
	return top
}

func spanBottom(v []drawlist.ModelVertex) int {
	bot := 0
	for i := 1; i < len(v); i++ {
		if v[i].Y > v[bot].Y {
			bot = i
		}
	}
	return bot
}

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

// modelQuadLanesReference mirrors modelQuadMapperSource on the CPU, over the
// same packed bytes the device samples, so the packing and the chain selection
// can be checked without a graphics device.
func modelQuadLanesReference(buf []byte, index, col, row int) (u, v, key, shade float32) {
	pos, uvs, keys, shades, mm := decodeQuadParams(buf, index)
	lane := func(i int) [4]float32 { return [4]float32{uvs[i][0], uvs[i][1], keys[i], shades[i]} }
	mix := func(a, b [4]float32, t float32) [4]float32 {
		var out [4]float32
		for i := range out {
			out[i] = a[i] + (b[i]-a[i])*t
		}
		return out
	}
	y := float32(row)
	lx, rx := pos[0][0], pos[0][0]
	ll, rl := lane(0), lane(0)
	for k := 0; k < 3; k++ {
		ls := 4 - k
		if ls == 4 {
			ls = 0
		}
		le := 3 - k
		if le >= mm && pos[le][1] > pos[ls][1] && y >= pos[ls][1] && y < pos[le][1] {
			ll = mix(lane(ls), lane(le), (y-pos[ls][1])/(pos[le][1]-pos[ls][1]))
			lx = referenceEdgeX(pos[ls], pos[le], row)
		}
		re := k + 1
		if k < mm && pos[re][1] > pos[k][1] && y >= pos[k][1] && y < pos[re][1] {
			rl = mix(lane(k), lane(re), (y-pos[k][1])/(pos[re][1]-pos[k][1]))
			rx = referenceEdgeX(pos[k], pos[re], row)
		}
	}
	t := float32(0)
	if rx > lx {
		t = (float32(col) - lx) / (rx - lx)
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
	}
	out := mix(ll, rl, t)
	return out[0], out[1], out[2], out[3]
}

func referenceEdgeX(a, b [2]float32, row int) float32 {
	dy := int(b[1]) - int(a[1])
	if dy <= 0 {
		return a[0]
	}
	step := ((int(b[0]) - int(a[0])) * 65536) / dy
	x := int(a[0])*65536 + 65535 + step*(row-int(a[1]))
	return float32(x / 65536)
}

// checkTexturedQuadInteriorMatchesStrips runs inside the opt-in device loop. It
// draws one translated textured quad through the two-triangle quad path and
// compares every interior pixel against the texel the CPU strip mapper would
// have sampled on the same geometry. Edge columns and the first and last row are
// excluded: coverage there is the approved raster approximation of §5.1, while
// the interior must agree exactly.
func checkTexturedQuadInteriorMatchesStrips() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	gradient := &formats.GAFFrame{Width: 8, Height: 8, Pixels: fixtureGradientTexture()}
	corners := [4][4]int32{{4, 3, 0, 0}, {28, 8, 7, 0}, {22, 27, 7, 7}, {2, 21, 0, 7}}
	place := func(dx, dy int32) drawlist.ModelFace {
		f := drawlist.ModelFace{Texture: gradient}
		for _, c := range corners {
			f.Vertices = append(f.Vertices, drawlist.ModelVertex{X: c[0] + dx, Y: c[1] + dy, U: c[2], V: c[3], Key: 40})
		}
		return f
	}
	var l drawlist.List
	l.RecordClear()
	l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
	shifts := [2]image.Point{{X: 0, Y: 0}, {X: 45, Y: 13}}
	for _, s := range shifts {
		l.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true, place(int32(s.X), int32(s.Y)))})
	}
	l.RecordExpand()
	out := r.Execute(&l, 80, 48)
	if out == nil {
		return fmt.Errorf("textured quad fixture returned no image")
	}
	if s := r.ModelStats(); s.TexturedQuadFaces != 2 || s.TexturedQuadStrips != 0 {
		return fmt.Errorf("textured quad fixture used strips: %d faces, %d strips", s.TexturedQuadFaces, s.TexturedQuadStrips)
	}
	pixels := make([]byte, 80*48*4)
	out.ReadPixels(pixels)
	compared, uncovered := 0, 0
	for _, s := range shifts {
		f := place(int32(s.X), int32(s.Y))
		top, bot := spanTop(f.Vertices), spanBottom(f.Vertices)
		strips := modelSpanStrips(f)
		for i, strip := range strips {
			if i == 0 || i == len(strips)-1 {
				continue
			}
			row := int(strip.Vertices[0].Y)
			xl, xr := int(strip.Vertices[0].X), int(strip.Vertices[1].X)
			left, okL := chainAt(f.Vertices, top, bot, -1, int32(row))
			right, okR := chainAt(f.Vertices, top, bot, 1, int32(row))
			if !okL || !okR {
				return fmt.Errorf("textured quad row %d has no two-chain span", row)
			}
			for c := xl + 1; c < xr-1; c++ {
				t := float32(c-xl) / float32(xr-xl)
				tu := int(left.U + (right.U-left.U)*t + 1.0/65536.0)
				tv := int(left.V + (right.V-left.V)*t + 1.0/65536.0)
				want := gradient.Pixels[tv*8+tu]
				got := pixels[(row*80+c)*4]
				if got == 7 {
					// The device covers a pixel when its centre is inside the
					// ring; the span writer covers the ceiling interval. Near a
					// shallow edge the two disagree by a pixel, which is the
					// approved raster approximation of §5.1. Every pixel the
					// device did draw must still carry the mapper's texel.
					uncovered++
					continue
				}
				if got != want {
					return fmt.Errorf("textured quad interior at (%d,%d): index %d, want the strip mapper's %d", c, row, got, want)
				}
				compared++
			}
		}
	}
	if compared < 200 {
		return fmt.Errorf("textured quad interior comparison covered only %d pixels", compared)
	}
	if uncovered*20 > compared {
		return fmt.Errorf("textured quad coverage differs from the span writer on %d of %d interior pixels", uncovered, compared+uncovered)
	}
	return nil
}
