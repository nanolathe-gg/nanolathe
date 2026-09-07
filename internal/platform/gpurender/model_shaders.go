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
		idx = floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+custom.xy+vec2(0.5, 0.5)).r*255.0+0.5)
	}
	if custom.w > 0.5 {
		row := floor(color.g)
		idx = floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, row+0.5)).r*255.0+0.5)
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
