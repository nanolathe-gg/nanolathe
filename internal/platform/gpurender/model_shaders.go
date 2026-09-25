package gpurender

import "fmt"

// The model passes deliberately retain the source key as a varying until the
// fragment.  Reducing already-wrapped vertex bytes is observably different at
// a signed interpolation crossing [03 R-REN-03A §2][03 R-RAST-01 §1].
//
// Every model shader takes its per-subject parameters from vertex lanes rather
// than uniforms, so one draw can carry every subject of one slot atlas page
// [DESIGN_GPU_RENDERER.md §11.2].
// modelQuadMapperSource is shared by the key and colour passes. A textured quad
// draws as two device triangles and recovers every lane here
// (docs/DESIGN_GPU_RENDERER.md §11.2 "Textured quads without strips").
//
// Source 3 is the frame's quad parameter image. A mapped quad's entry is two
// slots (model_quads.go). The first is twelve RGBA8 texels holding two 16-bit
// values each — four corner positions, four corner texel coordinates, then four
// corner key/shade pairs with the bottom corner's rotated index in the first
// pair's spare byte — and modelQuadLanes, which the colour pass and the water
// reflection call, walks it. The second is the span writer's edge setup for
// the key alone, made once on the CPU, and modelQuadKey, which the key pass
// calls, reads it: the same key without a division per fragment and from
// fewer texels.
//
// Corner positions are subject-local plus modelQuadLocalBias (model_quads.go),
// so a caller shifts the fragment's atlas position into the same frame first:
// modelQuadFrame reads the subject's origin from its verdict entry and returns
// what to subtract. Every operand of the edge walk then stays non-negative and
// the arithmetic is the one the absolute positions gave, translated.
var modelQuadMapperSource = `
func modelQuadTexel(n float) vec4 {
	w := imageSrc3Size().x
	y := floor(n/w)
	return imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(n-y*w+0.5, y+0.5))
}

// modelQuadFrame is the atlas texel a subject's packed frame begins at: its
// local origin texel, stored offset-binary in texel 8 of its verdict entry,
// less the local bias. A fragment at atlas texel d is at d - modelQuadFrame(e)
// in the frame the subject's corners were packed in.
func modelQuadFrame(entry float) vec2 {
	t := modelQuadTexel((entry-1.0)*12.0 + 8.0)
	return vec2(modelQuadU16(t.r, t.g), modelQuadU16(t.b, t.a)) - vec2(` + fmt.Sprint(modelQuadKeyBias+modelQuadLocalBias) + `.0)
}

func modelQuadU16(hi float, lo float) float {
	return floor(hi*255.0+0.5)*256.0 + floor(lo*255.0+0.5)
}

// modelQuadEdgeX is the span writer's fixed-point edge setup: one signed 16.16
// slope truncated toward zero and the +65535 bias, so the row's first covered
// column is the ceiling of the edge position [03 R-RAST-01 §1]. Every operand
// is a whole page pixel, and the running term is bounded by the edge's own
// horizontal extent, so the walk stays inside a signed 32-bit integer.
func modelQuadEdgeX(a vec2, b vec2, row float) float {
	dy := int(b.y) - int(a.y)
	if dy <= 0 {
		return a.x
	}
	step := ((int(b.x) - int(a.x)) * 65536) / dy
	x := int(a.x)*65536 + 65535 + step*(int(row)-int(a.y))
	return float(x / 65536)
}

// modelQuadLanes evaluates the two-chain mapping of [03 R-RAST-01 §1] at one
// fragment: the edge the decreasing-index chain and the increasing-index chain
// are on at the fragment's row, each lane interpolated along its edge by
// (y-yA)/(yB-yA), then interpolated across the row by (x-L)/(R-L). Corners
// arrive rotated so index 0 holds the minimum Y and index mm the maximum, which
// makes the two chains index ranges rather than a search. Rows are half-open,
// and a row a folded chain crosses twice keeps the later edge, exactly as the
// edge tables do. It returns (u, v, key, shade).
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
		// The left chain steps from the top corner towards decreasing indices
		// until it reaches the bottom corner; the right chain steps the other
		// way. Later matches overwrite earlier ones, which is the edge table's
		// "a folded chain's later edge wins" rule.
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
		// The span writer's first pixel takes the left lane exactly and its
		// last is one step short of the right one, so a fragment the device
		// covers just outside the ceiling span clamps rather than extrapolating.
		t = clamp((col-lx)/(rx-lx), 0.0, 1.0)
	}
	return ll + (rl-ll)*t
}

// modelQuadRows decodes a key entry's four corner rows, top corner first, and
// the rotated index of its bottom corner (model_quads.go).
func modelQuadRows(base float) (vec4, int) {
	a := modelQuadTexel(base)
	b := modelQuadTexel(base+1.0)
	top := modelQuadU16(a.r, a.g)
	mm := floor(top/` + fmt.Sprint(modelQuadRowLimit) + `.0)
	return vec4(top-mm*` + fmt.Sprint(modelQuadRowLimit) + `.0, modelQuadU16(a.b, a.a), modelQuadU16(b.r, b.g), modelQuadU16(b.b, b.a)), int(mm)
}

// modelQuadBottom is the bottom corner's row: the chains own the rows from the
// top corner's up to, and not including, this one.
func modelQuadBottom(y vec4, mm int) float {
	if mm == 1 {
		return y.y
	}
	if mm == 2 {
		return y.z
	}
	return y.w
}

// modelQuadLeft is the left chain's edge at a row inside the ring, and that
// edge's from and to rows. The chain steps from the top corner towards
// decreasing indices until it reaches the bottom corner, over ring edges 3, 2
// and 1; an edge owns the rows from its from row up to its to row when it
// descends, and a row a folded chain crosses twice keeps the later edge, as the
// span writer's edge table does [03 R-RAST-01 §1]. The edges that descend start
// in chain order at non-decreasing rows, so the later edge owning a row is the
// last one starting at or above it.
func modelQuadLeft(y vec4, mm int, row float) (int, float, float) {
	e := 3
	yf := y.x
	yt := y.w
	if mm <= 2 && y.z > y.w && row >= y.w {
		e = 2
		yf = y.w
		yt = y.z
	}
	if mm == 1 && y.y > y.z && row >= y.z {
		e = 1
		yf = y.z
		yt = y.y
	}
	return e, yf, yt
}

// modelQuadRight is modelQuadLeft for the right chain, which steps towards
// increasing indices over ring edges 0, 1 and 2.
func modelQuadRight(y vec4, mm int, row float) (int, float, float) {
	e := 0
	yf := y.x
	yt := y.y
	if mm >= 2 && y.z > y.y && row >= y.y {
		e = 1
		yf = y.y
		yt = y.z
	}
	if mm == 3 && y.w > y.z && row >= y.z {
		e = 2
		yf = y.z
		yt = y.w
	}
	return e, yf, yt
}

// modelQuadStep decodes an edge's 16.16 slope, stored offset by 2^31.
func modelQuadStep(t vec4) int {
	return (int(modelQuadU16(t.r, t.g))-32768)*65536 + int(modelQuadU16(t.b, t.a))
}

// modelQuadStepX is the span writer's fixed-point edge walk at a row: the from
// column promoted to 16.16 with the +65535 bias, advanced by the stored slope
// once per row, so the row's first covered column is the ceiling of the edge
// position [03 R-RAST-01 §1]. It is modelQuadEdgeX with the division made once
// per quad on the CPU; the operands and the result are the same integers.
func modelQuadStepX(xf float, step int, yf float, row float) float {
	x := int(xf)*65536 + 65535 + step*(int(row)-int(yf))
	return float(x / 65536)
}

// modelQuadPick is one of a key entry's four corner values.
func modelQuadPick(v vec4, i int) float {
	if i == 0 {
		return v.x
	}
	if i == 1 {
		return v.y
	}
	if i == 2 {
		return v.z
	}
	return v.w
}

// modelQuadKey is modelQuadLanes' key, for the key pass: the two-chain mapping
// of [03 R-RAST-01 §1] at one fragment from the quad's key entry, the chains'
// edges chosen by comparing the row with the corner rows and each edge's slope
// read once. Its value at every fragment is the key modelQuadLanes returns
// where a shader reads the key alone, as the key pass and the reflection do;
// that is what keeps the key plane where it was
// (docs/DESIGN_GPU_RENDERER.md §11.2).
//
// The arithmetic keeps the shape of modelQuadLanes' walk because the shader
// compiler rounds by shape: each chain starts from the top corner's key, its
// first edge (which starts at that corner) updates it when the row is on it,
// and a later edge overrides that. Written so, the compiler rounds the first
// edge's product separately and fuses a later edge's interpolation into one
// multiply-add, as it does in the walk; written straight, every edge fuses,
// which moves some keys by a unit in the last place. A later edge's operands
// are picked by index, so it shares no operand with the first edge's for the
// compiler to merge into a third rounding.
func modelQuadKey(q float, d vec2) float {
	base := q*` + fmt.Sprint(modelQuadTexels) + `.0
	y, mm := modelQuadRows(base)
	row := floor(d.y)
	col := floor(d.x)
	a := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyCornerTexel) + `.0)
	b := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyCornerTexel+1) + `.0)
	k := vec4(modelQuadU16(a.r, a.g), modelQuadU16(a.b, a.a), modelQuadU16(b.r, b.g), modelQuadU16(b.b, b.a)) - vec4(32768.0)
	top := k.x
	if row < y.x || row >= modelQuadBottom(y, mm) {
		// Neither chain owns the row: the walk leaves both at the top corner.
		return top
	}
	f := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyColumnTexel) + `.0)
	g := modelQuadTexel(base + ` + fmt.Sprint(modelQuadKeyColumnTexel+1) + `.0)
	xs := vec4(modelQuadU16(f.r, f.g), modelQuadU16(f.b, f.a), modelQuadU16(g.r, g.g), modelQuadU16(g.b, g.a))
	le, lyf, lyt := modelQuadLeft(y, mm, row)
	re, ryf, ryt := modelQuadRight(y, mm, row)
	// The left chain walks edge e from corner e+1 to corner e, the right chain
	// from corner e to corner e+1.
	lf := le + 1
	if lf == 4 {
		lf = 0
	}
	lx := modelQuadStepX(modelQuadPick(xs, lf), modelQuadStep(modelQuadTexel(base+` + fmt.Sprint(modelQuadKeyStepTexel) + `.0+float(le))), lyf, row)
	rx := modelQuadStepX(modelQuadPick(xs, re), modelQuadStep(modelQuadTexel(base+` + fmt.Sprint(modelQuadKeyStepTexel) + `.0+float(re))), ryf, row)
	// Each chain's first edge runs from the top corner: to corner 3 on the
	// left, to corner 1 on the right.
	lk := top
	if y.w > y.x && row >= y.x && row < y.w {
		lk = top + (k.w-top)*((row-y.x)/(y.w-y.x))
	}
	if le < 3 {
		la := modelQuadPick(k, lf)
		lk = la + (modelQuadPick(k, le)-la)*((row-lyf)/(lyt-lyf))
	}
	rk := top
	if y.y > y.x && row >= y.x && row < y.y {
		rk = top + (k.y-top)*((row-y.x)/(y.y-y.x))
	}
	if re > 0 {
		ra := modelQuadPick(k, re)
		rk = ra + (modelQuadPick(k, re+1)-ra)*((row-ryf)/(ryt-ryf))
	}
	t := 0.0
	if rx > lx {
		t = clamp((col-lx)/(rx-lx), 0.0, 1.0)
	}
	return lk + (rk-lk)*t
}
`
