package gpurender

import "github.com/nanolathe-gg/nanolathe/internal/drawlist"

// Numeric digits extend the existing flat-index/glint lane; this is not a
// float bitcast. The low 16 bits retain index/glint, followed by two material
// bits and three response bits. All corners receive the same integer (21 bits
// maximum). Coefficients are reviewed artistic choices (GPU design §29).
func (r *Renderer) modelFinishColor(f *drawlist.ModelFace, base float32) float32 {
	if !r.materialsEnabled || f.Material == drawlist.ModelMaterialDefault || f.Material > drawlist.ModelMaterialPaint {
		return base
	}
	r.modelStats.MaterialFaces++
	response := uint32(min(max(f.Normal[0]*-0.35+f.Normal[1]*-0.15+f.Normal[2]*0.9246621, 0), 1)*7 + 0.5)
	return base + float32(uint32(f.Material)+response*4)*65536
}

const modelFinishShaderSource = `
func modelFinish(albedo vec3, lit vec3, finish float) vec3 {
 if finish < 0.5 { return lit }
 material := mod(finish, 4.0)
 response := floor(finish/4.0)/7.0
 if material > 0.5 && material < 1.5 {
  // Brushed steel carries a broad cool reflection under the approved glint.
  // Dark seams retain their original contrast (GPU design §29).
  lobe := response*response
  lobe *= lobe
  peak := max(albedo.r, max(albedo.g, albedo.b))
  lit = lit*0.87 + albedo*vec3(0.05, 0.10, 0.16)*(1.0-response) + vec3(0.72, 0.84, 1.0)*lobe*0.42*smoothstep(0.06, 0.3, peak)
 } else if material > 1.5 {
  // Paint has a rough, weak white highlight; authored hue remains legible.
  lit = lit*0.93 + albedo*0.035 + vec3(0.075)*response*response
 }
 return min(lit, vec3(1.0))
}
`
