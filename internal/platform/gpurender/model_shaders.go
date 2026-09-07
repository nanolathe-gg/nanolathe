package gpurender

import "github.com/hajimehoshi/ebiten/v2"

// The model passes deliberately retain the source key as a varying until the
// fragment.  Reducing already-wrapped vertex bytes is observably different at
// a signed interpolation crossing [03 R-REN-03A §2][03 R-RAST-01 §1].
const modelKeyShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	key := floor(color.r)
	key = key - floor(key/256.0)*256.0
	return vec4(key/255.0, 0.0, 0.0, 1.0)
}
`

// Source 0 is a screen-sized coordinate image, source 1 is the subject key
// plane, source 2 is SHD and source 3 is the resolved GAF frame.  Texture
// transparency is intentionally ignored: keyed texels own a composition pixel
// and the composition boundary applies transparency later [03 R-REN-03A §5].
const modelBodyShaderSource = `//kage:unit pixels

package main

var UseKey bool
var UseReveal bool
var RevealBounds vec2
var RevealColors vec3

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	key := floor(color.r)
	key = key - floor(key/256.0)*256.0
	if UseKey {
		d := floor(dstPos.xy-imageDstOrigin())
		stored := floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+d+vec2(0.5, 0.5)).r*255.0+0.5)
		if key != stored { return vec4(0.0, 0.0, 0.0, 0.0) }
	}
	idx := floor(color.b)
	if custom.z > 0.5 {
		// The span mapper narrows U/V with an arithmetic 16.16 shift before
		// loading a texel [03 R-RAST-01 §1]. Keep the +0.5 source-image
		// texel-centre address, but floor the interpolated lane first; feeding a
		// fraction directly to nearest sampling would round it instead.
		// The centre-sample endpoint correction can leave an intended integer
		// lane infinitesimally below that integer in backend float arithmetic.
		// A 2^-20 texel guard is GPU implementation policy: it corrects observed
		// endpoint residue and may bias values this close to a texel boundary.
		// It is smaller than a 16.16 unit, not an additional retail constant.
		uv := floor(custom.xy + vec2(1.0/1048576.0))
		idx = floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+uv+vec2(0.5, 0.5)).r*255.0+0.5)
	}
	if custom.w > 0.5 {
		row := floor(color.g)
		idx = floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, row+0.5)).r*255.0+0.5)
	}
	if UseReveal {
		verdict := RevealColors.y
		if key < RevealBounds.y { verdict = RevealColors.x } else if key >= RevealBounds.x { verdict = RevealColors.z }
		if verdict == -2.0 { idx = 1.0 } else if verdict != -1.0 { idx = verdict }
	}
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

func newModelKeyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelKeyShaderSource))
}
func newModelBodyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelBodyShaderSource))
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
// [03 R-REN-03D §4–§5][03 R-RAST-01 §4].
const modelShadowCommitShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	shadow := floor(imageSrc0At(srcPos).r*255.0+0.5)
	body := floor(imageSrc1AtFromSrc0Pos(srcPos).r*255.0+0.5)
	if shadow == 1.0 || body != 1.0 { return vec4(0.0) }
	dest := floor(imageSrc2AtFromSrc0Pos(srcPos).r*255.0+0.5)
	idx := floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(dest+0.5, shadow+0.5)).r*255.0+0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

func newModelShadowCommitShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelShadowCommitShaderSource))
}

// Waterline and Digger processing follow reveal/outline and preserve the key
// plane. Erasure writes the composition background, never the framebuffer
// [03 R-WATER-01 §2][03 R-REN-03A §8].
const modelClipShaderSource = `//kage:unit pixels
package main
var WaterlineMode float
var WaterlineKey float
var Digger bool
var DiggerKey float
func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	idx := floor(imageSrc0At(srcPos).r*255.0+0.5)
	key := floor(imageSrc1AtFromSrc0Pos(srcPos).r*255.0+0.5)
	if idx != 1.0 && key <= WaterlineKey {
		if WaterlineMode == 1.0 { idx = 1.0 }
		if WaterlineMode == 2.0 { idx = floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5,0.5)).r*255.0+0.5) }
	}
	if Digger && key <= DiggerKey { idx = 1.0 }
	return vec4(idx/255.0,0.0,0.0,1.0)
}
`

func newModelClipShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelClipShaderSource))
}

const modelPackShaderSource = `//kage:unit pixels
package main
func Fragment(dstPos vec4,srcPos vec2,color vec4) vec4 {
	return vec4(imageSrc0At(srcPos).r,imageSrc1AtFromSrc0Pos(srcPos).r,0.0,1.0)
}
`
const modelChildShaderSource = `//kage:unit pixels
package main
var KeyDelta float
func Fragment(dstPos vec4,srcPos vec2,color vec4) vec4 {
	prior := imageSrc2AtFromSrc0Pos(srcPos)
	idx := floor(imageSrc0At(srcPos).r*255.0+0.5)
	key := floor(imageSrc1AtFromSrc0Pos(srcPos).r*255.0+0.5)+KeyDelta
	if idx == 1.0 || floor(prior.g*255.0+0.5) > key { return prior }
	stored := key-floor(key/256.0)*256.0
	return vec4(idx/255.0,stored/255.0,0.0,1.0)
}
`

func newModelPackShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelPackShaderSource))
}
func newModelChildShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelChildShaderSource))
}
