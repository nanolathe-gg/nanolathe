package gpurender

// waterFieldSource is the Enhanced water surface's field, shared by the water
// pass and the underwater model commit so a submerged hull moves and shades
// with the water drawn around it (GPU design §26.3). Selected presentation
// values, not retail behavior.
const waterFieldSource = `
const patternSize = 0.5
const surfaceOpacity = 0.5
const currentScale = 3.0
const rippleDeformation = 5.0
const surfaceEnergy = 0.5

func noise(p vec2) float {
 f := fract(p)
 // Time and the integrated wind drift scroll this lattice without bound, so
 // the hashed cell index has to be wrapped before it reaches the sine: an
 // unbounded argument loses all float precision over a long game and the
 // pattern degrades. Wrapping every corner on one period keeps the lattice
 // continuous instead of jumping — the field simply repeats every 289 cells,
 // which at the scales used here (0.0055 gust, 0.0161 warp, 0.07 broad,
 // 0.166 fine and 0.025 shore cells per world pixel) is about 1,700 world
 // pixels for the finest layer and tens of thousands for the coarse ones
 // (GPU design §26.3).
 a := floor(p)
 a = a-floor(a/289.0)*289.0
 b := a+vec2(1.0)
 b = b-floor(b/289.0)*289.0
 f = f*f*(3.0-2.0*f)
 h := vec4(dot(a,vec2(43.17,97.53)),dot(vec2(b.x,a.y),vec2(43.17,97.53)),dot(vec2(a.x,b.y),vec2(43.17,97.53)),dot(b,vec2(43.17,97.53)))
 h = fract(sin(h)*17341.23)
 return mix(mix(h.x,h.y,f.x),mix(h.z,h.w,f.x),f.y)
}
// waterField returns the broad and fine lattices and the gust envelope at a
// pattern-space point, for the drift the current has integrated and the phase.
func waterField(pattern vec2, drift vec2, t float) (float, float, float) {
 gust := smoothstep(0.30,0.80,noise((pattern-drift*22.0)*0.0055+vec2(3.0,7.0)))
 // Bounded in-place deformation keeps zero-tidal surfaces alive without
 // introducing a directional scroll unrelated to the current.
 p := (pattern-drift*6.0)*0.07
 a := noise(pattern*0.018+vec2(7.0,13.0))*6.283185
 b := noise(pattern*0.023+vec2(31.0,3.0))*6.283185
 warp := vec2(0.5)+vec2(sin(t*0.65+a),sin(t*0.83+b))*0.5*rippleDeformation
 p += (warp-vec2(0.5))*1.6
 broad := noise(p)
 // The fine lattice is rotated 37 degrees about the map origin — a fixed
 // rotation, applied once, not a wind-following one — so its cell rows never
 // line up with the broad lattice and the pair stops reading as a grid.
 q := (pattern-drift*11.0)*0.166
 q = vec2(q.x*0.7986-q.y*0.6018,q.x*0.6018+q.y*0.7986)+(warp-vec2(0.5))*0.9
 fine := noise(q)
 return broad, fine, gust
}

// waterOffset is the refraction displacement in world pixels; callers scale it
// to their own pixels and by the shore depth.
func waterOffset(broad float, fine float, gust float) vec2 {
 return vec2(broad-0.5,fine-0.5)*(3.2+surfaceEnergy*2.4)*(0.7+0.5*gust)
}

// waterShade shades a refracted colour with the ripple and its moving crest
// highlights. Moving highlights make the surface readable even when the
// painted detail is too fine to reveal displacement.
func waterShade(c vec3, broad float, fine float, gust float, deep float, colour float) vec3 {
 ripple := broad*0.65+fine*0.35-0.5
 shade := 1.0+(ripple*(0.112+surfaceEnergy*0.08)*(0.7+0.5*gust)*deep-0.05*surfaceEnergy*gust*deep)
 crest := smoothstep(0.10,0.32,ripple)
 return mix(c*shade,vec3(0.40,0.67,0.78),clamp(colour*crest*(0.04+surfaceEnergy*0.04)*deep,0,1))
}
`
