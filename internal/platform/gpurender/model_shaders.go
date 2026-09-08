package gpurender

import "github.com/hajimehoshi/ebiten/v2"

// The model passes deliberately retain the source key as a varying until the
// fragment.  Reducing already-wrapped vertex bytes is observably different at
// a signed interpolation crossing [03 R-REN-03A §2][03 R-RAST-01 §1].
//
// Every model shader takes its per-subject parameters from vertex lanes rather
// than uniforms, so one draw can carry every subject of one slot atlas page
// [DESIGN_GPU_RENDERER.md §11.2].
const modelKeyShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	key := floor(color.r)
	key = key - floor(key/256.0)*256.0
	return vec4(key/255.0, 0.0, 0.0, 1.0)
}
`

// Source 0 is the slot atlas key page, source 1 is SHD and source 2 is the
// model texture page.  The destination is the same page's composed plane, so
// the fragment reads the key stored at its own texel: index in red, that key in
// green, and body coverage in blue for the reveal pass that may follow.
//
// Texture transparency is intentionally ignored: keyed texels own a composition
// pixel and the composition boundary applies transparency later
// [03 R-REN-03A §5].  color.a selects the key test, so a keyless subject stays
// in painter order [03 R-REN-03A §2].
const modelBodyShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	key := floor(color.r)
	key = key - floor(key/256.0)*256.0
	d := floor(dstPos.xy - imageDstOrigin())
	stored := floor(imageSrc0At(imageSrc0Origin()+d+vec2(0.5, 0.5)).r*255.0+0.5)
	if color.a > 0.5 && key != stored {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	idx := floor(color.b)
	if custom.z != 0.0 {
		// The span mapper narrows U/V with an arithmetic 16.16 shift before
		// loading a texel [03 R-RAST-01 §1]. Keep the +0.5 source-image
		// texel-centre address, but floor the interpolated lane first; feeding a
		// fraction directly to nearest sampling would round it instead.
		// The centre-sample endpoint correction can leave an intended integer
		// lane infinitesimally below that integer in backend float arithmetic.
		// A 2^-16 texel guard is GPU implementation policy: it corrects observed
		// endpoint residue and may bias values this close to a texel boundary.
		// It is one 16.16 unit, not an additional retail constant.
		uv := floor(custom.xy + vec2(1.0/65536.0))
		// A positive lane packs the texture's atlas slot dimensions; a negative
		// one is a frame too large to pack, drawn from its own image instead.
		size := imageSrc2Size()
		offset := vec2(0.0, 0.0)
		if custom.z > 0.0 {
			size = vec2(floor(custom.z/4096.0), custom.z-floor(custom.z/4096.0)*4096.0)
			offset = floor(srcPos-imageSrc0Origin()+vec2(0.5, 0.5))
		}
		idx = 0.0
		if uv.x >= 0.0 && uv.y >= 0.0 && uv.x < size.x && uv.y < size.y {
			idx = floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+offset+uv+vec2(0.5, 0.5)).r*255.0+0.5)
		}
	}
	if custom.w > 0.5 {
		row := floor(color.g)
		idx = floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, row+0.5)).r*255.0+0.5)
	}
	return vec4(idx/255.0, stored/255.0, 1.0, 1.0)
}
`

// The nanoframe reveal runs over the finished body plane of one slot, before
// the outline pass, exactly where the per-subject colour pass used to apply it.
// The stored key is the byte that pass compared, so the verdict is the one it
// would have reached; a subject under construction always owns a key plane
// [03 R-REN-03A §2]. Blue is the body pass's coverage flag, so an untouched
// composition background keeps its transparent index. Verdict -2 erases to that
// background and -1 keeps the material index [03 §5.2][03 R-COMP-01 §3].
const modelRevealShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	src := imageSrc0At(srcPos)
	idx := floor(src.r*255.0+0.5)
	key := floor(src.g*255.0+0.5)
	if src.b > 0.5 {
		verdict := floor(custom.y+0.5)
		if key < floor(color.g+0.5) {
			verdict = floor(custom.x+0.5)
		} else if key >= floor(color.r+0.5) {
			verdict = floor(custom.z+0.5)
		}
		if verdict == -2.0 {
			idx = 1.0
		} else if verdict != -1.0 {
			idx = verdict
		}
	}
	return vec4(idx/255.0, key/255.0, 0.0, 1.0)
}
`

// modelCopy returns one processed slot region to its page. Nearest sampling at
// matched rectangles copies the stored bytes exactly (C-G4).
const modelCopyShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	return imageSrc0At(srcPos)
}
`

func newModelKeyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelKeyShaderSource))
}
func newModelBodyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelBodyShaderSource))
}
func newModelRevealShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelRevealShaderSource))
}
func newModelCopyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelCopyShaderSource))
}

// TODO(question): The researched model blit keys composition index 1, while
// the current CPU modelTarget coverage path can retain a covered index 1.
// Resolve this mismatch by reconciling the classic coverage writer with the
// researched keyed blit before treating this comparison exception as parity.
const modelCommitShaderSource = `//kage:unit pixels
package main
func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	idx := floor(imageSrc0At(srcPos).r*255.0+0.5)
	if idx == 1.0 { return vec4(0.0, 0.0, 0.0, 0.0) }
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}`

func newModelCommitShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelCommitShaderSource))
}

// The shadow is one completed silhouette. Read a separate destination snapshot
// and punch at the body's framebuffer coordinates before the ALP lookup
// [03 R-REN-03D §4–§5][03 R-RAST-01 §4]. custom.xy is the shadow slot's
// framebuffer offset from the body slot's.
const modelShadowCommitShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	shadow := floor(imageSrc0At(srcPos).r*255.0+0.5)
	p := srcPos-imageSrc0Origin()+custom.xy
	body := 1.0
	if p.x >= 0.0 && p.y >= 0.0 && p.x < imageSrc1Size().x && p.y < imageSrc1Size().y {
		body = floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+p).r*255.0+0.5)
	}
	if shadow == 1.0 || body != 1.0 { return vec4(0.0) }
	dest := floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+floor(dstPos.xy-imageDstOrigin())+vec2(0.5, 0.5)).r*255.0+0.5)
	idx := floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(dest+0.5, shadow+0.5)).r*255.0+0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

func newModelShadowCommitShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelShadowCommitShaderSource))
}

// Waterline and Digger processing follow reveal/outline and preserve the key
// plane. Erasure writes the composition background, never the framebuffer
// [03 R-WATER-01 §2][03 R-REN-03A §8]. Source 0 is the slot page (index in red,
// stored key in green) and source 1 is BLUE; the thresholds ride the vertex.
const modelClipShaderSource = `//kage:unit pixels
package main
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	src := imageSrc0At(srcPos)
	idx := floor(src.r*255.0+0.5)
	key := floor(src.g*255.0+0.5)
	mode := floor(color.r+0.5)
	if idx != 1.0 && key <= floor(color.g+0.5) {
		if mode == 1.0 { idx = 1.0 }
		if mode == 2.0 { idx = floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, 0.5)).r*255.0+0.5) }
	}
	if color.b > 0.5 && key <= floor(color.a+0.5) { idx = 1.0 }
	return vec4(idx/255.0, key/255.0, 0.0, 1.0)
}
`

func newModelClipShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelClipShaderSource))
}

// Source 0 is the finished child slot (index in red, stored key in green) and
// source 1 the staging image the previous merge produced, aligned with this
// draw's destination. custom.x is the signed height delta, compared at signed
// width before the byte is stored [03 R-REN-03A §4].
const modelChildShaderSource = `//kage:unit pixels
package main
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	q := floor(dstPos.xy-imageDstOrigin())+vec2(0.5, 0.5)
	prior := imageSrc1AtFromSrc0Pos(imageSrc0Origin()+q)
	src := imageSrc0At(srcPos)
	idx := floor(src.r*255.0+0.5)
	key := floor(src.g*255.0+0.5)+floor(custom.x+0.5)
	if idx == 1.0 || floor(prior.g*255.0+0.5) > key { return prior }
	stored := key-floor(key/256.0)*256.0
	return vec4(idx/255.0, stored/255.0, 0.0, 1.0)
}
`

func newModelChildShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelChildShaderSource))
}

// ALP is ordered: left then right within a row, top then bottom between rows.
// The key comes only from the top-left sample, including under erased pixels
// [03 R-REN-03A §6–§7]. Background indices participate in all three lookups.
// Source 0 is the doubled slot page and source 1 is ALP; color.r selects the
// key-plane output the native key page needs for the outline pass.
const modelResolveShaderSource = `//kage:unit pixels
package main
func blend(a, b float) float {
	return floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(b+0.5, a+0.5)).r*255.0+0.5)
}
func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	p := imageSrc0Origin()+2.0*floor((srcPos-imageSrc0Origin())/2.0)+vec2(0.5, 0.5)
	top := imageSrc0At(p)
	if color.r > 0.5 { return vec4(top.g, 0.0, 0.0, 1.0) }
	a := floor(top.r*255.0+0.5)
	b := floor(imageSrc0At(p+vec2(1.0, 0.0)).r*255.0+0.5)
	c := floor(imageSrc0At(p+vec2(0.0, 1.0)).r*255.0+0.5)
	d := floor(imageSrc0At(p+vec2(1.0, 1.0)).r*255.0+0.5)
	idx := blend(blend(a, b), blend(c, d))
	return vec4(idx/255.0, top.g, 0.0, 1.0)
}
`

func newModelResolveShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelResolveShaderSource))
}
