package gpurender

// setMetalGlint is the executor gate the player's Finish switch drives
// (GPU design §23.7, §30). It never changes a draw list.
func (r *Renderer) setMetalGlint(on bool) { r.metalGlint = on }

// metalFaceGlint is a deliberately cheap, artistic directional specular lobe.
// The fixed unit half-vector is in the same world X/Z/height axes as Normal.
// Five squarings make a narrow power-32 lobe without a sqrt, pow, clock or RNG.
// Turning geometry supplies motion; translating the camera cannot make it swim.
func metalFaceGlint(normal [3]float32) float32 {
	x := max(normal[0]*-0.35+normal[1]*-0.15+normal[2]*0.9246621, 0)
	x = min(x, 1)
	x *= x
	x *= x
	x *= x
	x *= x
	x *= x
	return x
}

// The constant ColorG lane carries the flat palette byte plus an eight-bit
// glint weight, equally on every corner. At most sixteen numeric bits leave
// ample interpolation headroom. No extra vertex, texture, uniform or pass.
func metalGlintColor(index byte, glint float32) float32 {
	return float32(index) + float32(uint32(glint*255+0.5))*256
}

const metalGlintShaderSource = `
func metalGlint(albedo vec3, lit vec3, amount float) vec3 {
 if amount < 0.5 { return lit }
 // This is an aesthetic texture mask, not recovered material metadata.
 // Neutral midtones catch the light; dark seams and saturated paint keep detail.
 peak := max(albedo.r, max(albedo.g, albedo.b))
 low := min(albedo.r, min(albedo.g, albedo.b))
 saturation := (peak-low)/max(peak, 0.001)
 mask := smoothstep(0.12, 0.35, peak)*(1.0-smoothstep(0.2, 0.65, saturation))
 tint := mix(vec3(1.0), albedo, 0.65)
 return min(lit + tint*(amount/255.0)*0.48*mask, vec3(1.0))
}
`
