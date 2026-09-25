package gpurender

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The quad mapper replaces one device quad per source row with two device
// triangles plus a per-fragment evaluation of the same two-chain mapping
// [03 R-RAST-01 §1]: the colour pass walks the quad's corner entry, the key
// pass reads its key entry, an edge setup the CPU makes once per quad
// (model_quads.go). These cases lock the packing, the chain selection and the
// edge arithmetic the fragment shaders depend on;
// checkModelQuadMapperDevicePixels then checks, on the device, that both
// reproduce the corner walk bit for bit.

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
	// The corner entry is the one the colour pass has always walked, byte for
	// byte.
	if want := oldModelQuadAdd(nil, v, 10, 100); !bytes.Equal(q.buf[:modelQuadBytes], want) {
		t.Fatalf("corner entry % x, want % x", q.buf[:modelQuadBytes], want)
	}
	e := decodeQuadEntry(q.buf, 1)
	if e.mm != 2 {
		t.Fatalf("bottom corner index = %d, want 2", e.mm)
	}
	for j := 0; j < 4; j++ {
		if src := v[(2+j)%4]; e.rows[j] != int(src.Y)+100 {
			t.Fatalf("corner %d row = %d, want the slot-shifted authored row %d", j, e.rows[j], src.Y+100)
		}
	}
	// Every edge is stored in the direction its chain walks it: the right
	// chain owns edges 0 and 1 (from e to e+1), the left chain edges 2 and 3
	// (from e+1 to e).
	for edge, want := range [4][2]int{{0, 1}, {1, 2}, {3, 2}, {0, 3}} {
		r := e.edges[edge]
		from, to := v[(2+want[0])%4], v[(2+want[1])%4]
		if r.xFrom != int(from.X)+10 {
			t.Fatalf("edge %d from column = %d, want %d", edge, r.xFrom, from.X+10)
		}
		if r.keyFrom != int(from.Key) || r.keyTo != int(to.Key) {
			t.Fatalf("edge %d keys = %d, %d, want the signed authored keys %d, %d", edge, r.keyFrom, r.keyTo, from.Key, to.Key)
		}
		if dy := to.Y - from.Y; dy > 0 {
			if want := (int(to.X-from.X) << 16) / int(dy); r.step != want {
				t.Fatalf("edge %d slope = %d, want %d", edge, r.step, want)
			}
		}
	}
	if got := q.add(v, 0, 0, 0); got != 1+modelQuadSlots {
		t.Fatalf("second quad index = %d, want %d", got, 1+modelQuadSlots)
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
	if got := q.add(base, 0, modelQuadRowLimit, 0); got != 0 {
		t.Fatalf("a top row sharing bits with the bottom index was packed: %d", got)
	}
	wide := append([]drawlist.ModelVertex(nil), base...)
	wide[1].X, wide[2].X = 1<<15, 1<<15
	if got := q.add(wide, 0, 0, 0); got != 0 {
		t.Fatalf("a slope past the fragment's 32-bit headroom was packed: %d", got)
	}
	if q.count != 0 {
		t.Fatalf("rejected faces still consumed %d parameter slots", q.count)
	}
	// A quad takes two slots: with one left it is refused, as a verdict entry
	// would not be.
	q.count = modelDirectParamCap - 1
	if got := q.add(base, 0, 0, 0); got != 0 {
		t.Fatalf("a quad was packed into the image's last slot: %d", got)
	}
}

// The key entry's precomputed edge setup must give the key pass exactly what
// the corner walk computes: for every row, both chains' columns and the key at
// both ends of each chain's edge, including a folded chain's later edge, a row
// no edge owns, and a non-parallelogram. The old walk is a Go port of the
// corner walk (modelQuadEdgeX and the loop over the corners), the new one a
// port of modelQuadKey over the packed bytes, and the CPU outline walk
// (chainAt with biasedEdgeX) is the span writer's own reference
// [03 R-RAST-01 §1].
func TestModelQuadEdgeSetupMatchesTheWalk(t *testing.T) {
	quads := [][4][2]int32{
		{{46, 1}, {58, 3}, {55, 10}, {44, 8}},   // the fixture's non-parallelogram
		{{0, 0}, {8, 0}, {8, 5}, {0, 5}},        // flat top and bottom
		{{5, 0}, {9, 7}, {2, 3}, {3, 12}},       // bow-tie: both chains fold
		{{0, 0}, {6, 9}, {3, 4}, {1, 10}},       // right chain folds
		{{10, 0}, {9, 10}, {14, 3}, {11, 12}},   // left chain folds
		{{3, 4}, {3, 4}, {9, 4}, {6, 11}},       // repeated corner
		{{0, 5}, {7, 0}, {7, 5}, {0, 0}},        // ties at the top and bottom
		{{-3, -2}, {40, 1}, {20, 33}, {-9, 17}}, // reaches past the box
	}
	rng := rand.New(rand.NewPCG(3, 5))
	for range 400 {
		var c [4][2]int32
		for i := range c {
			c[i] = [2]int32{rng.Int32N(40) - 8, rng.Int32N(24) - 4}
		}
		quads = append(quads, c)
	}
	for qi, c := range quads {
		for rot := range 4 {
			v := make([]drawlist.ModelVertex, 4)
			for i := range v {
				p := c[(i+rot)%4]
				v[i] = drawlist.ModelVertex{X: p[0], Y: p[1], Key: int32(20 + 7*i - 3*int(p[1]))}
			}
			var q modelQuadParams
			index := q.add(v, modelQuadLocalBias, modelQuadLocalBias, 0)
			top, bot := 0, 0
			for i := 1; i < 4; i++ {
				if v[i].Y < v[top].Y {
					top = i
				}
				if v[i].Y > v[bot].Y {
					bot = i
				}
			}
			if v[top].Y == v[bot].Y {
				if index != 0 {
					t.Fatalf("quad %d: a ring without rows was packed", qi)
				}
				continue
			}
			if index == 0 {
				t.Fatalf("quad %d rotation %d was refused", qi, rot)
			}
			e := decodeQuadEntry(q.buf, index)
			// The corners rotated as the corner entry holds them, in the
			// packed frame.
			var old [4]drawlist.ModelVertex
			for j := range old {
				old[j] = v[(top+j)&3]
				old[j].X += modelQuadLocalBias
				old[j].Y += modelQuadLocalBias
			}
			mm := (bot - top) & 3
			for row := v[top].Y - 2; row < v[bot].Y+2; row++ {
				r := row + modelQuadLocalBias
				wantL, wantR := oldQuadWalk(old, mm, r)
				gotL, gotR := e.walk(r)
				if gotL != wantL || gotR != wantR {
					t.Fatalf("quad %d rotation %d row %d: edges %+v, %+v; the corner walk has %+v, %+v", qi, rot, row, gotL, gotR, wantL, wantR)
				}
				if row < v[top].Y || row >= v[bot].Y {
					continue
				}
				// Inside the ring both chains own the row, and their columns
				// are the CPU walk's.
				left, a := chainAt(old[:], 0, mm, -1, r)
				right, b := chainAt(old[:], 0, mm, 1, r)
				if !a || !b || int32(left.X) != gotL.x || int32(right.X) != gotR.x {
					t.Fatalf("quad %d rotation %d row %d: columns %d, %d; the CPU walk has %v, %v", qi, rot, row, gotL.x, gotR.x, left.X, right.X)
				}
			}
		}
	}
}

// quadEdgeAt is what one chain hands the key interpolation at a row: the
// edge's from and to rows, its key at both ends, and the column. A row no
// edge owns has the top corner at both ends and no column.
type quadEdgeAt struct {
	yf, yt   int32
	from, to int32
	x        int32
}

// oldQuadWalk is the corner walk: every edge's slope divided out at the row,
// later matches overwriting earlier ones.
func oldQuadWalk(c [4]drawlist.ModelVertex, mm int, row int32) (left, right quadEdgeAt) {
	edgeX := func(a, b drawlist.ModelVertex) int32 {
		dy := b.Y - a.Y
		step := ((b.X - a.X) * 65536) / dy
		return (a.X*65536 + 65535 + step*(row-a.Y)) / 65536
	}
	top := quadEdgeAt{from: c[0].Key, to: c[0].Key, x: -1}
	left, right = top, top
	for k := 0; k < 3; k++ {
		ls, le := (4-k)%4, 3-k
		if le >= mm && c[le].Y > c[ls].Y && row >= c[ls].Y && row < c[le].Y {
			left = quadEdgeAt{yf: c[ls].Y, yt: c[le].Y, from: c[ls].Key, to: c[le].Key, x: edgeX(c[ls], c[le])}
		}
		re := k + 1
		if k < mm && c[re].Y > c[k].Y && row >= c[k].Y && row < c[re].Y {
			right = quadEdgeAt{yf: c[k].Y, yt: c[re].Y, from: c[k].Key, to: c[re].Key, x: edgeX(c[k], c[re])}
		}
	}
	return left, right
}

// quadEdgeRecord and quadEntry are a quad's key entry decoded as the shader
// decodes it.
type quadEdgeRecord struct {
	step                  int
	xFrom, keyFrom, keyTo int
}

type quadEntry struct {
	rows  [4]int
	mm    int
	edges [4]quadEdgeRecord
}

func decodeQuadEntry(buf []byte, index int) quadEntry {
	base := index * modelQuadBytes
	u16 := func(off int) int { return int(buf[base+off])<<8 | int(buf[base+off+1]) }
	var e quadEntry
	top := u16(0)
	e.mm = top / modelQuadRowLimit
	e.rows = [4]int{top % modelQuadRowLimit, u16(2), u16(4), u16(6)}
	var key, x [4]int
	for c := range 4 {
		key[c] = u16(4*modelQuadKeyCornerTexel+2*c) - modelQuadKeyBias
		x[c] = u16(4*modelQuadKeyColumnTexel + 2*c)
	}
	for i := range e.edges {
		from, to := i, (i+1)&3
		if i >= e.mm {
			from, to = to, from
		}
		e.edges[i] = quadEdgeRecord{
			step:  int(int64(binary.BigEndian.Uint32(buf[base+4*(modelQuadKeyStepTexel+i):])) - 1<<31),
			xFrom: x[from], keyFrom: key[from], keyTo: key[to],
		}
	}
	return e
}

// walk is modelQuadKey up to the key interpolation: the edge each chain picks
// at the row by comparing it with the corner rows, and the column from the
// stored slope.
func (e quadEntry) walk(row int32) (left, right quadEdgeAt) {
	y, mm, r := e.rows, e.mm, int(row)
	at := func(edge, yf, yt int) quadEdgeAt {
		rec := e.edges[edge]
		x := rec.xFrom*65536 + 65535 + rec.step*(r-yf)
		return quadEdgeAt{yf: int32(yf), yt: int32(yt), x: int32(x / 65536), from: int32(rec.keyFrom), to: int32(rec.keyTo)}
	}
	bottom := y[3]
	if mm == 1 {
		bottom = y[1]
	} else if mm == 2 {
		bottom = y[2]
	}
	if r < y[0] || r >= bottom {
		top := quadEdgeAt{from: int32(e.edges[0].keyFrom), to: int32(e.edges[0].keyFrom), x: -1}
		return top, top
	}
	le, lyf, lyt := 3, y[0], y[3]
	if mm <= 2 && y[2] > y[3] && r >= y[3] {
		le, lyf, lyt = 2, y[3], y[2]
	}
	if mm == 1 && y[1] > y[2] && r >= y[2] {
		le, lyf, lyt = 1, y[2], y[1]
	}
	re, ryf, ryt := 0, y[0], y[1]
	if mm >= 2 && y[2] > y[1] && r >= y[1] {
		re, ryf, ryt = 1, y[1], y[2]
	}
	if mm == 3 && y[3] > y[2] && r >= y[2] {
		re, ryf, ryt = 2, y[2], y[3]
	}
	return at(le, lyf, lyt), at(re, ryf, ryt)
}

// checkModelQuadMapperDevicePixels draws the corner walk this lane has always
// used and the current shaders over the same quads, and compares, texel for
// texel, the bits of what each returns (docs/DESIGN_GPU_RENDERER.md §11.2).
// The colour pass's lanes come from the corner entry: u, v, key and shade,
// each through a shader that reads that lane alone, then all four as the
// colour pass consumes them. The key pass's key comes from the key entry
// (modelQuadKey) and must be, bit for bit, the key the corner walk returns
// where a shader reads the key alone — as the key pass always has. A lane is
// compared as its integer part and the first twenty-four bits of its
// fraction. Every quad is evaluated over a tile wider and taller than itself,
// so the rows neither chain owns — which only the water reflection's fattened
// geometry samples — are compared too.
func checkModelQuadMapperDevicePixels() error {
	const tile, tilesX, tilesY = 64, 32, 16
	quads := modelQuadProbeQuads(tilesX * tilesY)
	var oldBuf []byte
	var params modelQuadParams
	for i, v := range quads {
		oldBuf = oldModelQuadAdd(oldBuf, v, modelQuadLocalBias, modelQuadLocalBias)
		if got := params.add(v, modelQuadLocalBias, modelQuadLocalBias, 0); got != 1+i*modelQuadSlots {
			return fmt.Errorf("quad mapper probe: quad %d packed at %d", i, got)
		}
	}
	upload := func(buf []byte) *ebiten.Image {
		rows := (len(buf)/4 + modelQuadParamWidth - 1) / modelQuadParamWidth
		img := ebiten.NewImage(modelQuadParamWidth, rows)
		img.WritePixels(append(buf, make([]byte, rows*modelQuadParamWidth*4-len(buf))...))
		return img
	}
	oldParams, newParams := upload(oldBuf), upload(params.buf[:params.count*modelQuadBytes])
	defer oldParams.Deallocate()
	defer newParams.Deallocate()
	fill := ebiten.NewImage(4, 4)
	defer fill.Deallocate()
	w, h := tile*tilesX, tile*tilesY
	draw := func(src string, params *ebiten.Image) ([]byte, error) {
		shader, err := ebiten.NewShader([]byte(src))
		if err != nil {
			return nil, err
		}
		defer shader.Deallocate()
		dst := ebiten.NewImage(w, h)
		defer dst.Deallocate()
		verts := []ebiten.Vertex{{DstX: 0, DstY: 0}, {DstX: float32(w), DstY: 0}, {DstX: float32(w), DstY: float32(h)}, {DstX: 0, DstY: float32(h)}}
		dst.DrawTrianglesShader32(verts, []uint32{0, 1, 2, 0, 2, 3}, shader, &ebiten.DrawTrianglesShaderOptions{
			Images: [4]*ebiten.Image{fill, fill, fill, params}, Blend: ebiten.BlendCopy,
		})
		pixels := make([]byte, w*h*4)
		dst.ReadPixels(pixels)
		return pixels, nil
	}
	for lane := 0; lane <= 5; lane++ {
		oldCall, newCall := "modelQuadLanes(1.0+q, d)", "modelQuadLanes(1.0+q*"+fmt.Sprint(modelQuadSlots)+".0, d)"
		out := lane
		if lane == 5 {
			// The key pass: the key entry against the walk's key read alone.
			oldCall, newCall, out = "vec4(0.0, 0.0, modelQuadLanes(1.0+q, d).z, 0.0)", "vec4(0.0, 0.0, modelQuadKey(1.0+q*"+fmt.Sprint(modelQuadSlots)+".0, d), 0.0)", 2
		}
		want, err := draw(modelQuadProbeSource(oldModelQuadMapperSource, oldCall, out, tile, tilesX), oldParams)
		if err != nil {
			return fmt.Errorf("quad mapper probe: the corner walk: %w", err)
		}
		got, err := draw(modelQuadProbeSource(modelQuadMapperSource, newCall, out, tile, tilesX), newParams)
		if err != nil {
			return fmt.Errorf("quad mapper probe: the current mapper: %w", err)
		}
		for i := 0; i < len(got); i += 4 {
			if !bytes.Equal(got[i:i+4], want[i:i+4]) {
				x, y := (i/4)%w, (i/4)/w
				return fmt.Errorf("quad mapper probe %d: quad %d at tile texel (%d,%d) reads %v, the corner walk %v",
					lane, x/tile+tilesX*(y/tile), x%tile, y%tile, got[i:i+4], want[i:i+4])
			}
		}
	}
	return nil
}

// modelQuadProbeQuads is the probe's quads: the edge cases of
// TestModelQuadEdgeSetupMatchesTheWalk, then random rings, every one with
// rows, inside a tile's first 56 texels, with signed keys, texel coordinates
// and shade rows that differ corner to corner.
func modelQuadProbeQuads(n int) [][]drawlist.ModelVertex {
	rng := rand.New(rand.NewPCG(11, 13))
	shapes := [][4][2]int32{
		{{46, 1}, {55, 3}, {52, 10}, {44, 8}},
		{{0, 0}, {8, 0}, {8, 5}, {0, 5}},
		{{5, 0}, {9, 7}, {2, 3}, {3, 12}},
		{{0, 0}, {6, 9}, {3, 4}, {1, 10}},
		{{10, 0}, {9, 10}, {14, 3}, {11, 12}},
		{{3, 4}, {3, 4}, {9, 4}, {6, 11}},
		{{0, 5}, {7, 0}, {7, 5}, {0, 0}},
		{{1, 1}, {55, 2}, {40, 54}, {3, 30}},
	}
	var out [][]drawlist.ModelVertex
	for len(out) < n {
		var c [4][2]int32
		if len(out) < 4*len(shapes) {
			c = shapes[len(out)/4]
			r := len(out) % 4
			c = [4][2]int32{c[r], c[(r+1)%4], c[(r+2)%4], c[(r+3)%4]}
		} else {
			for i := range c {
				c[i] = [2]int32{rng.Int32N(56), rng.Int32N(56)}
			}
		}
		v := make([]drawlist.ModelVertex, 4)
		y0, y1 := c[0][1], c[0][1]
		for i := range v {
			v[i] = drawlist.ModelVertex{
				X: c[i][0], Y: c[i][1], Key: rng.Int32N(600) - 300,
				U: rng.Int32N(4000), V: rng.Int32N(300), Shade: uint8(rng.IntN(32)),
			}
			y0, y1 = min(y0, c[i][1]), max(y1, c[i][1])
		}
		if y0 == y1 {
			continue
		}
		out = append(out, v)
	}
	return out
}

// modelQuadProbeSource evaluates one mapper call over a grid of tiles, one quad
// a tile, and writes the requested lane's integer part and the first
// twenty-four bits of its fraction — or, for lane 4, the four lanes as the
// colour pass consumes them.
func modelQuadProbeSource(mapper, call string, lane, tile, tilesX int) string {
	out := `
	uv := floor(lanes.xy + vec2(1.0/65536.0, 1.0/65536.0))
	return vec4(mod(uv.x, 256.0), mod(uv.y, 256.0), mod(floor(lanes.z), 256.0), mod(floor(lanes.w), 256.0)) / 255.0`
	if lane < 4 {
		out = `
	x := lanes.` + string("xyzw"[lane]) + `
	hi := floor(x)
	f := x - hi
	return vec4(mod(hi, 256.0), floor(f*256.0), mod(floor(f*65536.0), 256.0), mod(floor(f*16777216.0), 256.0)) / 255.0`
	}
	return `//kage:unit pixels

package main
` + mapper + `
func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	p := floor(dstPos.xy - imageDstOrigin())
	cell := floor(p / ` + fmt.Sprint(tile) + `.0)
	q := cell.x + cell.y*` + fmt.Sprint(tilesX) + `.0
	d := p - cell*` + fmt.Sprint(tile) + `.0 + vec2(` + fmt.Sprint(modelQuadLocalBias-4) + `.0)
	lanes := ` + call + out + `
}
`
}

// oldModelQuadAdd is the one-slot entry the corner walk read before the key
// entry existed: twelve texels, the rotated corners' positions, texel
// coordinates and key/shade pairs, the bottom corner's index in the first
// pair's spare byte. It is written independently of add, so the check compares
// against the entry as it was rather than against itself.
func oldModelQuadAdd(buf []byte, v []drawlist.ModelVertex, dx, dy int) []byte {
	top, bot := 0, 0
	for i := 1; i < 4; i++ {
		if v[i].Y < v[top].Y {
			top = i
		}
		if v[i].Y > v[bot].Y {
			bot = i
		}
	}
	var packed [modelQuadBytes]byte
	for j := 0; j < 4; j++ {
		c := &v[(top+j)&3]
		putQuadLane(packed[4*j:], int(c.X)+dx, int(c.Y)+dy)
		putQuadLane(packed[16+4*j:], int(c.U), int(c.V))
		putQuadLane(packed[32+4*j:], int(c.Key)+modelQuadKeyBias, 0)
		packed[32+4*j+2] = c.Shade
	}
	packed[35] = byte((bot - top) & 3)
	return append(buf, packed[:]...)
}

// oldModelQuadMapperSource is the corner walk as it was before the key entry,
// kept verbatim so the check compares the current shaders against it rather
// than against themselves.
const oldModelQuadMapperSource = `
func modelQuadTexel(n float) vec4 {
	w := imageSrc3Size().x
	y := floor(n/w)
	return imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(n-y*w+0.5, y+0.5))
}

func modelQuadU16(hi float, lo float) float {
	return floor(hi*255.0+0.5)*256.0 + floor(lo*255.0+0.5)
}

func modelQuadEdgeX(a vec2, b vec2, row float) float {
	dy := int(b.y) - int(a.y)
	if dy <= 0 {
		return a.x
	}
	step := ((int(b.x) - int(a.x)) * 65536) / dy
	x := int(a.x)*65536 + 65535 + step*(int(row)-int(a.y))
	return float(x / 65536)
}

func modelQuadLanes(q float, d vec2) vec4 {
	base := (q-1.0)*12.0
	var pos [4]vec2
	var uv [4]vec2
	var ks [4]vec2
	mm := 0
	for i := 0; i < 4; i++ {
		a := modelQuadTexel(base+float(i))
		pos[i] = vec2(modelQuadU16(a.r, a.g), modelQuadU16(a.b, a.a))
		b := modelQuadTexel(base+4.0+float(i))
		uv[i] = vec2(modelQuadU16(b.r, b.g), modelQuadU16(b.b, b.a))
		c := modelQuadTexel(base+8.0+float(i))
		ks[i] = vec2(modelQuadU16(c.r, c.g)-32768.0, floor(c.b*255.0+0.5))
		if i == 0 {
			mm = int(floor(c.a*255.0+0.5))
		}
	}
	row := floor(d.y)
	col := floor(d.x)
	lx := pos[0].x
	rx := pos[0].x
	ll := vec4(uv[0].x, uv[0].y, ks[0].x, ks[0].y)
	rl := ll
	for k := 0; k < 3; k++ {
		ls := 4 - k
		if ls == 4 {
			ls = 0
		}
		le := 3 - k
		if le >= mm && pos[le].y > pos[ls].y && row >= pos[ls].y && row < pos[le].y {
			t := (row-pos[ls].y) / (pos[le].y-pos[ls].y)
			a := vec4(uv[ls].x, uv[ls].y, ks[ls].x, ks[ls].y)
			b := vec4(uv[le].x, uv[le].y, ks[le].x, ks[le].y)
			ll = a + (b-a)*t
			lx = modelQuadEdgeX(pos[ls], pos[le], row)
		}
		re := k + 1
		if k < mm && pos[re].y > pos[k].y && row >= pos[k].y && row < pos[re].y {
			t := (row-pos[k].y) / (pos[re].y-pos[k].y)
			a := vec4(uv[k].x, uv[k].y, ks[k].x, ks[k].y)
			b := vec4(uv[re].x, uv[re].y, ks[re].x, ks[re].y)
			rl = a + (b-a)*t
			rx = modelQuadEdgeX(pos[k], pos[re], row)
		}
	}
	t := 0.0
	if rx > lx {
		t = clamp((col-lx)/(rx-lx), 0.0, 1.0)
	}
	return ll + (rl-ll)*t
}
`
