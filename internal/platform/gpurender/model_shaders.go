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
// draws as two device triangles and recovers every lane here, so both passes
// narrow the same key at the same fragment and a mapped face is never rejected
// by a key its own key pass did not write
// (docs/DESIGN_GPU_RENDERER.md §11.2 "Textured quads without strips").
//
// Source 3 is the frame's quad parameter image: twelve RGBA8 texels per quad
// holding two 16-bit values each — four corner positions, four corner texel
// coordinates, then four corner key/shade pairs with the bottom corner's
// rotated index in the first pair's spare byte.
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
`
