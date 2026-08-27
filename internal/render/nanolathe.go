package render

// Nanolathe segment presentation [03 §5.5][05 "R-P0-06 §5 addendum"].
//
// A construction/reclaim work step publishes one nano segment record. The
// record is not a line: it is a short-lived particle emitter. Each record
// carries a source box (the QueryNanoPiece world point, so a degenerate box)
// and a target box (the target's world bounding box), both narrowed at
// construction to the span between the 4/11 and 7/11 interpolants, stored as
// origin+extent. Every tick the record spawns five particles, each one a
// random point in the source box travelling to a random point in the target
// box at four world units per tick, cycling through the green nanolathe ramp.
// The narrowed target box is what gives the spray its cone shape.
//
// All randomness comes from the CRT presentation stream — six draws per
// particle, five particles per record per tick — so nano presentation can
// never perturb the simulation stream [05 "R-P0-06 §5 addendum"].

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

const (
	// NanoParticlesPerTick is the established spawn count per record per tick.
	NanoParticlesPerTick = 5
	// NanoParticleSpeed is the per-tick travel distance in whole world units;
	// a particle lives trunc(distance/NanoParticleSpeed) ticks.
	NanoParticleSpeed = 4
	// NanoColorBase and NanoColorSpan bound the nanolathe green ramp. A
	// particle's colour is the base plus a nibble that starts at
	// 1+(index mod 7) and advances by one each tick, wrapping back to 1 past
	// seven [05 "R-P0-06 §5 addendum"].
	NanoColorBase = 0xa0
	NanoColorSpan = 7
	// NanoParticleSize is the mark one particle leaves. The record's draw fills
	// the rectangle from the particle's pixel to one pixel right and down, and
	// retail's rectangle fill is inclusive on both edges, so the mark is two by
	// two [03 §5.5].
	NanoParticleSize = 2
	// NanoStripCap is the shared effect-strip bound: the oldest record is
	// evicted once the pre-insert count exceeds 400 [01 §6.1][03 §1].
	NanoStripCap = 400
	// nanoNear and nanoFar are the interpolants that narrow both boxes.
	nanoNear, nanoFar, nanoDenom = 4, 7, 11
)

// NanoParticle is one travelling nanolathe pixel.
type NanoParticle struct {
	X, Y, Z    numeric.Fixed
	VX, VY, VZ numeric.Fixed
	Color      uint8
	ExpiryTick uint32
}

// NanoRecord is one published work step's emitter.
type NanoRecord struct {
	SrcOrigin, SrcExtent [3]numeric.Fixed
	DstOrigin, DstExtent [3]numeric.Fixed
	EndTick              uint32 // last tick that may spawn
	NextSpawn            uint32
	Particles            []NanoParticle
}

// NanoField is the presentation-owned strip of live nano records.
type NanoField struct {
	Records []NanoRecord
}

// narrow reduces one axis pair to the 4/11..7/11 span as origin and extent.
func narrow(lo, hi numeric.Fixed) (origin, extent numeric.Fixed) {
	span := hi - lo
	origin = lo + span*nanoNear/nanoDenom
	return origin, lo + span*nanoFar/nanoDenom - origin
}

// Add appends a record for one accepted work step. The source is the nano
// piece world position; min/max are the target's world bounding box. Records
// spawn on their creation tick and the tick after it [05 "R-P0-06 §5
// addendum"].
func (f *NanoField) Add(src, min, max [3]numeric.Fixed, tick uint32) {
	if f == nil {
		return
	}
	var r NanoRecord
	for a := 0; a < 3; a++ {
		r.SrcOrigin[a], r.SrcExtent[a] = narrow(src[a], src[a])
		r.DstOrigin[a], r.DstExtent[a] = narrow(min[a], max[a])
	}
	// The geometry initializer sets the record's final spawn tick one tick
	// past creation, so a record spawns on two ticks.
	r.EndTick = tick + 1
	r.NextSpawn = tick
	if len(f.Records) > NanoStripCap {
		f.Records = append(f.Records[:0], f.Records[1:]...)
	}
	f.Records = append(f.Records, r)
}

// Tick advances every live record: existing particles move and recolour, the
// expired ones are dropped, then a record still inside its spawn window emits
// five more. rand must return the CRT stream's [0,0x7fff] value.
func (f *NanoField) Tick(now uint32, rand func() int32) {
	if f == nil || rand == nil {
		return
	}
	live := f.Records[:0]
	for i := range f.Records {
		r := &f.Records[i]
		r.advance(now)
		if r.NextSpawn <= r.EndTick && r.NextSpawn <= now {
			r.spawn(now, rand)
			r.NextSpawn = now + 1
		}
		if len(r.Particles) != 0 {
			live = append(live, *r)
		}
	}
	f.Records = live
}

// advance moves each particle one tick and drops the ones that arrived.
func (r *NanoRecord) advance(now uint32) {
	kept := r.Particles[:0]
	for _, p := range r.Particles {
		p.X += p.VX
		p.Y += p.VY
		p.Z += p.VZ
		p.Color = NanoColorBase | nextNanoNibble(p.Color)
		if now > p.ExpiryTick {
			continue
		}
		kept = append(kept, p)
	}
	r.Particles = kept
}

// nextNanoNibble advances the colour nibble, wrapping seven back to one.
func nextNanoNibble(color uint8) uint8 {
	n := (color & 0xf) + 1
	if n > NanoColorSpan {
		n = 1
	}
	return n
}

// spawn emits the record's five particles for one tick.
func (r *NanoRecord) spawn(now uint32, rand func() int32) {
	for i := 0; i < NanoParticlesPerTick; i++ {
		var src, dst [3]numeric.Fixed
		for a := 0; a < 3; a++ {
			src[a] = r.SrcOrigin[a] + numeric.Fixed(rand())*r.SrcExtent[a]/0x8000
		}
		for a := 0; a < 3; a++ {
			dst[a] = r.DstOrigin[a] + numeric.Fixed(rand())*r.DstExtent[a]/0x8000
		}
		steps := nanoTravelTicks(src, dst)
		if steps == 0 {
			continue // a zero-length hop is dropped before the record is written
		}
		r.Particles = append(r.Particles, NanoParticle{
			X: src[0], Y: src[1], Z: src[2],
			VX:         (dst[0] - src[0]) / numeric.Fixed(steps),
			VY:         (dst[1] - src[1]) / numeric.Fixed(steps),
			VZ:         (dst[2] - src[2]) / numeric.Fixed(steps),
			Color:      NanoColorBase | uint8(1+i%NanoColorSpan),
			ExpiryTick: now + uint32(steps),
		})
	}
}

// nanoTravelTicks is the particle lifetime: the whole-world-unit distance
// divided by the four-units-per-tick travel speed, truncated toward zero.
func nanoTravelTicks(src, dst [3]numeric.Fixed) int32 {
	dx := float64(dst[0]-src[0]) / 65536
	dy := float64(dst[1]-src[1]) / 65536
	dz := float64(dst[2]-src[2]) / 65536
	d2 := dx*dx + dy*dy + dz*dz
	if d2 <= 0 {
		return 0
	}
	return int32(int16(int32(math.Sqrt(d2)) / NanoParticleSpeed))
}

// ModelBounds is the model's bind-pose bounding box in model-relative
// fixed-point coordinates, walking the hierarchy the way the retail loader's
// model-top helper does but keeping all six extents rather than the top alone
// [03 §5.5][fmt 3do].
//
// TODO(question): the retail unit definition stores this box as two triples
// alongside the model top; the loader's exact accumulator for the five
// extents other than the top is not traced. Only the nanolathe spray's target
// box reads it, and the box is narrowed to its middle three elevenths before
// use, so a small difference moves particle landing points slightly.
func ModelBounds(m *model.Model) (min, max [3]numeric.Fixed) {
	if m == nil || len(m.Pieces) == 0 {
		return
	}
	first := true
	var walk func(index int, base [3]numeric.Fixed)
	walk = func(index int, base [3]numeric.Fixed) {
		if index < 0 || index >= len(m.Pieces) {
			return
		}
		p := &m.Pieces[index]
		var origin [3]numeric.Fixed
		for a := 0; a < 3; a++ {
			origin[a] = base[a] + p.Translate[a]
		}
		for _, v := range p.Vertices {
			for a := 0; a < 3; a++ {
				w := origin[a] + v[a]
				if first || w < min[a] {
					min[a] = w
				}
				if first || w > max[a] {
					max[a] = w
				}
			}
			first = false
		}
		for _, child := range p.Children {
			walk(child, origin)
		}
	}
	walk(m.Root, [3]numeric.Fixed{})
	return
}
