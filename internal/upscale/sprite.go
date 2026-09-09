package upscale

// The sprite synthesizer: the terrain upscaler's premise applied to sprite
// (GAF) art. Every authored 2x2 block of every frame in a GAF family, reduced
// through the retail palette blend, is an example of how a pixel of that
// colour looks one octave up. Transparency is carried as a fifth feature
// channel so silhouettes get authored edge detail instead of stair steps.
// See tools/mapupscale/featupscale/README.md for the cost terms and their
// calibration.

import (
	"math"
)

const (
	radius            = 2
	span              = 2*radius + 1
	chans             = 4 // R, G, B, opacity
	spritePatchValues = span * span * chans
	dims              = 8
	keyClear          = 256 // parent key of a transparent reduced pixel
	keys              = 257
)

// Sprite is one indexed raster with a parallel opacity mask: a GAF frame, a
// palette-indexed PNG, or a synthesized result. Alpha is true where the pixel
// is opaque, which is the inverse of formats.GAFFrame.Transparent.
type Sprite struct {
	Name  string
	Frame int
	W, H  int
	Pix   []byte
	Alpha []bool
}

// plane is one reduced phase of one database Sprite.
type plane struct {
	spriteIndex int
	phase       int
	w, h        int
	key         []int16
	begin       int // index of the plane's first example
}

type example struct {
	feat  [dims]int16
	key   int16
	count int8 // opaque pixels in the block
	sum   [3]int16
	block [4]byte
	alpha [4]bool
	plane int32
	x, y  int32
}

type spriteOptions struct {
	iterations                      int
	tone, deadzone, seam, coherence int32
	coverage, mismatch              int32
	closeness                       int32
	clampEdges                      bool
}

var spriteCoherenceOffsets = [16][2]int{{-1, -2}, {-1, -1}, {-1, 0}, {-1, 1}, {-1, 2}, {-1, 3}, {-2, -2}, {-2, -1}, {-2, 0}, {-2, 1}, {-2, 2}, {-2, 3}, {0, -1}, {1, -1}, {0, -2}, {1, -2}}

// SpriteParams are the sprite synthesizer's tuning knobs — the sprite tool's
// flags. DefaultSpriteParams returns the shipped defaults, which are what the
// engine synthesizes with. Each term is described in
// tools/mapupscale/featupscale/README.md.
type SpriteParams struct {
	Iterations int  // PatchMatch passes
	Tone       int  // tone weight, beyond the dead zone
	Deadzone   int  // tone dead zone per channel
	Seam       int  // seam weight between adjacent output blocks
	Coherence  int  // coherence weight of the authored surround
	Coverage   int  // coverage weight: opaque count against the query's density
	Mismatch   int  // coherence charge for an opacity mismatch
	Closeness  int  // colour closure, -1 disables
	Tie        int  // minimum opaque pixels for a reduced block to count opaque
	Relax      bool // admit stand-in parents one blend step away
	// ClampEdges treats pixels beyond a sprite's edge as copies of the edge
	// pixel instead of transparent, which is what opaque model textures want.
	ClampEdges bool
}

// DefaultSpriteParams returns the sprite tool's shipped defaults.
func DefaultSpriteParams() SpriteParams {
	return SpriteParams{
		Iterations: 8, Tone: 64, Deadzone: 0, Seam: 8, Coherence: 32, Coverage: 200_000,
		Mismatch: 3 * 64 * 64, Closeness: 3 * 24 * 24, Tie: 2, Relax: true,
	}
}

func (p SpriteParams) options() spriteOptions {
	return spriteOptions{
		iterations: p.Iterations, tone: int32(p.Tone), deadzone: int32(p.Deadzone),
		seam: int32(p.Seam), coherence: int32(p.Coherence), coverage: int32(p.Coverage),
		mismatch: int32(p.Mismatch), closeness: int32(p.Closeness), clampEdges: p.ClampEdges,
	}
}

// SpriteExamples is one example set: every authored 2x2 block of a family of
// sprites, at all four phase offsets, with the PCA basis fitted to them and
// the parent-key index the search draws from. Building it is the expensive
// part (0.2-1 s for a retail feature bank); each frame then costs 10-20 ms.
// It is immutable once built and safe to use from several goroutines.
type SpriteExamples struct {
	db            []*Sprite
	planes        []plane
	examples      []example
	index         exampleIndex
	contributions []float32
	pairDistance  []int32
	pal           [256][3]int32
	params        SpriteParams
	options       spriteOptions
}

// Count is the number of authored example blocks in the set.
func (e *SpriteExamples) Count() int { return len(e.examples) }

// Frames is the number of source sprites the examples were reduced from.
func (e *SpriteExamples) Frames() int { return len(e.db) }

// BuildSpriteExamples reduces every sprite of db through the palette blend at
// the four phase offsets and fits the feature basis. db is retained: the
// coherence term reads the authored pixels around a candidate block, so the
// caller must not mutate those sprites afterwards.
func BuildSpriteExamples(db []*Sprite, pal [256][3]uint8, alp []byte, params SpriteParams) *SpriteExamples {
	set := &SpriteExamples{db: db, params: params, options: params.options()}
	for index := range 256 {
		set.pal[index] = [3]int32{int32(pal[index][0]), int32(pal[index][1]), int32(pal[index][2])}
	}
	set.pairDistance = make([]int32, 256*256)
	for a := range 256 {
		for b := range 256 {
			var total int32
			for c := range 3 {
				d := set.pal[a][c] - set.pal[b][c]
				total += d * d
			}
			set.pairDistance[a*256+b] = total
		}
	}
	set.planes, set.examples = buildExamples(db, params.Tie, alp, &set.pal)
	basis := fitBasis(set.planes, set.examples, set.pal)
	set.contributions = spriteContributions(basis, set.pal)
	for pi := range set.planes {
		p := &set.planes[pi]
		for y := range p.h {
			for x := range p.w {
				set.examples[p.begin+y*p.w+x].feat = windowFeature(func(dy, dx int) int16 {
					yy, xx := y+dy, x+dx
					if params.ClampEdges {
						yy, xx = min(max(yy, 0), p.h-1), min(max(xx, 0), p.w-1)
					}
					if yy < 0 || yy >= p.h || xx < 0 || xx >= p.w {
						return keyClear
					}
					return p.key[yy*p.w+xx]
				}, set.contributions)
			}
		}
	}
	set.index = indexExamples(set.examples, set.pairDistance, alp, &set.pal, params.Relax)
	return set
}

// SpriteStats are the per-frame summary numbers the sprite tool prints.
type SpriteStats struct {
	Luma1x, Luma2x     float64
	Opaque1x, Opaque2x float64
	MeanCost           float64
	Unmatched          int
}

// Upscale synthesizes the 2x sprite for one query frame. seed, when given,
// holds the previous frame's matches at the same pixels and is offered as a
// candidate at every visit, which keeps animated entries from flickering; it
// is the returned match slice of the previous frame of the same entry, and is
// only valid when that frame had the same dimensions. The returned matches are
// the seed for the next frame.
func (e *SpriteExamples) Upscale(query *Sprite, seed []int32) (*Sprite, []int32, SpriteStats) {
	return upscale(query, seed, e.db, e.planes, e.examples, e.index, e.contributions,
		e.pairDistance, &e.pal, e.options)
}

// EPX is the Scale2x rule on indexed pixels with transparency as its own
// value, kept for the tool's side-by-side comparison.
func EPX(s *Sprite) *Sprite { return epx(s) }

// buildExamples reduces every database Sprite at the four 2x2 phases. A
// block is opaque when at least tie of its pixels are; opaque blocks reduce
// their opaque pixels through the palette blend [03 §3.7].
func buildExamples(db []*Sprite, tie int, alp []byte, pal *[256][3]int32) ([]plane, []example) {
	var planes []plane
	var examples []example
	for si, s := range db {
		for phase := range 4 {
			oy, ox := phase/2, phase%2
			pw, ph := (s.W-ox)/2, (s.H-oy)/2
			if pw <= 0 || ph <= 0 {
				continue
			}
			p := plane{spriteIndex: si, phase: phase, w: pw, h: ph, key: make([]int16, pw*ph), begin: len(examples)}
			for y := range ph {
				for x := range pw {
					sy, sx := oy+2*y, ox+2*x
					var ex example
					ex.plane, ex.x, ex.y = int32(len(planes)), int32(x), int32(y)
					idx := [4]int{sy*s.W + sx, sy*s.W + sx + 1, (sy+1)*s.W + sx, (sy+1)*s.W + sx + 1}
					var opaque []byte
					for k, i := range idx {
						ex.block[k], ex.alpha[k] = s.Pix[i], s.Alpha[i]
						if s.Alpha[i] {
							opaque = append(opaque, s.Pix[i])
							for c := range 3 {
								ex.sum[c] += int16(pal[s.Pix[i]][c])
							}
						}
					}
					ex.count = int8(len(opaque))
					if len(opaque) < tie {
						ex.key = keyClear
					} else {
						ex.key = int16(reduce(opaque, alp))
					}
					p.key[y*pw+x] = ex.key
					examples = append(examples, ex)
				}
			}
			planes = append(planes, p)
		}
	}
	return planes, examples
}

// exampleIndex groups example positions by parent key and maps keys with no
// examples to the nearest palette entry that has some.
type exampleIndex struct {
	counts   [keys]int
	offsets  [keys + 1]int
	byKey    []int32
	fallback [keys]int16
	// standIns[p] lists parent p itself plus keys one palette-blend step
	// away whose blocks average closer to p's colour than p's own blocks do,
	// as the terrain upscaler's relaxation: the nearest-entry blend makes
	// the blocks under a sparse-palette colour systematically brighter than
	// it, and the neighbouring entry's blocks carry the same texture at the
	// right mean [03 §3.7].
	standIns [keys][]int16
	admitted [keys][keys]bool
}

func indexExamples(examples []example, pairDistance []int32, alp []byte, pal *[256][3]int32, relax bool) exampleIndex {
	var ix exampleIndex
	var sum [keys][3]float64
	for _, ex := range examples {
		ix.counts[ex.key]++
		for c := range 3 {
			if ex.count > 0 {
				sum[ex.key][c] += float64(ex.sum[c]) / float64(ex.count)
			}
		}
	}
	distance := func(parent, key int) float64 {
		t := 0.0
		for c := range 3 {
			d := sum[key][c]/float64(max(ix.counts[key], 1)) - float64(pal[parent][c])
			t += d * d
		}
		return t
	}
	for parent := range keys {
		ix.standIns[parent] = append(ix.standIns[parent], int16(parent))
		ix.admitted[parent][parent] = true
		if !relax || parent == keyClear {
			continue
		}
		for key := range 256 {
			if key == parent || ix.counts[key] == 0 {
				continue
			}
			blend := int(alp[parent*256+key])
			if (blend == parent || blend == key) && distance(parent, key) < distance(parent, parent) {
				ix.standIns[parent] = append(ix.standIns[parent], int16(key))
				ix.admitted[parent][key] = true
			}
		}
	}
	for k := range keys {
		ix.offsets[k+1] = ix.offsets[k] + ix.counts[k]
	}
	ix.byKey = make([]int32, len(examples))
	cursor := ix.offsets
	for i, ex := range examples {
		ix.byKey[cursor[ex.key]] = int32(i)
		cursor[ex.key]++
	}
	for k := range keys {
		ix.fallback[k] = int16(k)
		if ix.counts[k] > 0 || k == keyClear {
			continue
		}
		best, bestD := int16(k), int32(math.MaxInt32)
		for j := range 256 {
			if ix.counts[j] == 0 {
				continue
			}
			if d := pairDistance[k*256+j]; d < bestD {
				best, bestD = int16(j), d
			}
		}
		ix.fallback[k] = best
	}
	return ix
}

// upscale synthesizes the 2x Sprite for one query frame. seed, when given,
// holds the previous frame's matches at the same pixels and is offered as a
// candidate at every visit, which keeps animated entries from flickering.
func upscale(query *Sprite, seed []int32, db []*Sprite, planes []plane, examples []example, ix exampleIndex,
	contributions []float32, pairDistance []int32, pal *[256][3]int32, opts spriteOptions) (*Sprite, []int32, SpriteStats) {
	qw, qh := query.W, query.H
	n := qw * qh
	qkey := make([]int16, n)
	for i := range qkey {
		if query.Alpha[i] {
			qkey[i] = int16(query.Pix[i])
		} else {
			qkey[i] = keyClear
		}
	}
	qfeat := make([][dims]int16, n)
	density := make([]int8, n)   // opaque pixels in the 3x3 around each query pixel
	edge := make([]bool, n)      // transparent pixel with an opaque 8-neighbour
	expected := make([]int32, n) // expected opaque count of the 2x2 block, in eighths
	for y := range qh {
		for x := range qw {
			qfeat[y*qw+x] = windowFeature(func(dy, dx int) int16 {
				yy, xx := y+dy, x+dx
				if opts.clampEdges {
					yy, xx = min(max(yy, 0), qh-1), min(max(xx, 0), qw-1)
				}
				if yy < 0 || yy >= qh || xx < 0 || xx >= qw {
					return keyClear
				}
				return qkey[yy*qw+xx]
			}, contributions)
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					yy, xx := y+dy, x+dx
					if yy >= 0 && yy < qh && xx >= 0 && xx < qw && query.Alpha[yy*qw+xx] {
						density[y*qw+x]++
						if !(dy == 0 && dx == 0) {
							edge[y*qw+x] = true
						}
					}
				}
			}
		}
	}
	// An opaque pixel covers one unit of area, so its block should be fully
	// opaque and a transparent pixel's block fully clear; the term is soft,
	// so authored edge blocks (three opaque pixels beside one) still win
	// where the feature and coherence terms prefer them. Anything weaker
	// erodes thin sprites: a lone speck given a half block loses half its
	// area at 2x.
	for i := range n {
		if query.Alpha[i] {
			expected[i] = 32
		}
	}
	var allowed [256]bool
	if opts.closeness < 0 {
		for i := range allowed {
			allowed[i] = true
		}
	} else {
		var used [256]bool
		for i, a := range query.Alpha {
			if a {
				used[query.Pix[i]] = true
			}
		}
		for c := range 256 {
			for q := range 256 {
				if used[q] && pairDistance[c*256+q] <= opts.closeness {
					allowed[c] = true
					break
				}
			}
		}
	}
	matches := make([]int32, n)
	costs := make([]int32, n)
	for i := range matches {
		matches[i] = -1
	}
	ow := 2*qw + 4
	synth := make([]byte, ow*(2*qh+4))
	synthA := make([]int8, ow*(2*qh+4)) // -1 unknown, 0 clear, 1 opaque
	for i := range synthA {
		synthA[i] = -1
	}
	rng := uint64(0x9e3779b97f4a7c15) ^ uint64(query.Frame)*0x9e3779b97f4a7c15
	next := func() uint64 { rng = splitmix64(rng); return rng }
	forward := true
	cost := func(pixel int, cand int32) int32 {
		ex := &examples[cand]
		want := qkey[pixel]
		if !ix.admitted[want][ex.key] && ex.key != ix.fallback[want] {
			return math.MaxInt32
		}
		if want == keyClear && !edge[pixel] && ex.count > 0 {
			return math.MaxInt32
		}
		for k := range 4 {
			if ex.alpha[k] && !allowed[ex.block[k]] {
				return math.MaxInt32
			}
		}
		c := spriteFeatureCost(&qfeat[pixel], &ex.feat)
		if opts.tone > 0 && want != keyClear && ex.count > 0 {
			for ch := range 3 {
				e := int32(ex.sum[ch]) - int32(ex.count)*pal[want][ch]
				if e < 0 {
					e = -e
				}
				if e > opts.deadzone {
					c += opts.tone * (e - opts.deadzone) * (e - opts.deadzone)
				}
			}
		}
		if opts.coverage > 0 {
			d := 8*int32(ex.count) - expected[pixel]
			c += opts.coverage * d * d / 64
		}
		y, x := pixel/qw, pixel%qw
		oy, ox := 2*y+2, 2*x+2
		if opts.seam > 0 {
			var t int32
			pair := func(a byte, aa bool, sy, sx int) {
				i := sy*ow + sx
				if synthA[i] < 0 || !aa || synthA[i] == 0 {
					return
				}
				t += pairDistance[int(a)*256+int(synth[i])]
			}
			pair(ex.block[0], ex.alpha[0], oy, ox-1)
			pair(ex.block[2], ex.alpha[2], oy+1, ox-1)
			pair(ex.block[1], ex.alpha[1], oy, ox+2)
			pair(ex.block[3], ex.alpha[3], oy+1, ox+2)
			pair(ex.block[0], ex.alpha[0], oy-1, ox)
			pair(ex.block[1], ex.alpha[1], oy-1, ox+1)
			pair(ex.block[2], ex.alpha[2], oy+2, ox)
			pair(ex.block[3], ex.alpha[3], oy+2, ox+1)
			c += opts.seam * t / 8
		}
		if opts.coherence > 0 {
			p := &planes[ex.plane]
			s := db[p.spriteIndex]
			sy, sx := p.phase/2+2*int(ex.y), p.phase%2+2*int(ex.x)
			var t int32
			for _, off := range spriteCoherenceOffsets {
				dy, dx := off[0], off[1]
				if !forward {
					dy, dx = 1-dy, 1-dx
				}
				i := (oy+dy)*ow + ox + dx
				if synthA[i] < 0 {
					continue
				}
				ay, ax := sy+dy, sx+dx
				aOpaque := ay >= 0 && ay < s.H && ax >= 0 && ax < s.W && s.Alpha[ay*s.W+ax]
				switch {
				case aOpaque && synthA[i] == 1:
					t += pairDistance[int(s.Pix[ay*s.W+ax])*256+int(synth[i])]
				case aOpaque != (synthA[i] == 1):
					t += opts.mismatch
				}
			}
			c += opts.coherence * t / 16
		}
		return c
	}
	commit := func(pixel int, cand int32, c int32) {
		matches[pixel], costs[pixel] = cand, c
		ex := &examples[cand]
		y, x := pixel/qw, pixel%qw
		oy, ox := 2*y+2, 2*x+2
		for k, o := range [4][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}} {
			i := (oy+o[0])*ow + ox + o[1]
			synth[i] = ex.block[k]
			if ex.alpha[k] {
				synthA[i] = 1
			} else {
				synthA[i] = 0
			}
		}
	}
	draw := func(pixel int) int32 {
		k := qkey[pixel]
		if stand := ix.standIns[k]; len(stand) > 1 {
			k = int16(stand[int(next()%uint64(len(stand)))])
		}
		if ix.counts[k] == 0 {
			k = ix.fallback[k]
		}
		if ix.counts[k] == 0 {
			return -1
		}
		return ix.byKey[ix.offsets[k]+int(next()%uint64(ix.counts[k]))]
	}
	shifted := func(m int32, dy, dx int) int32 {
		if m < 0 {
			return -1
		}
		ex := &examples[m]
		p := &planes[ex.plane]
		y, x := int(ex.y)+dy, int(ex.x)+dx
		if y < 0 || y >= p.h || x < 0 || x >= p.w {
			return -1
		}
		return int32(p.begin + y*p.w + x)
	}
	consider := func(pixel int, cands []int32) {
		best, bestC := matches[pixel], int32(math.MaxInt32)
		if best >= 0 {
			bestC = cost(pixel, best)
		}
		for _, c := range cands {
			if c < 0 {
				continue
			}
			if v := cost(pixel, c); v < bestC {
				best, bestC = c, v
			}
		}
		if best >= 0 {
			commit(pixel, best, bestC)
		}
	}
	var cands []int32
	for pixel := range qkey {
		cands = cands[:0]
		if seed != nil {
			cands = append(cands, seed[pixel])
		}
		cands = append(cands, draw(pixel), draw(pixel), draw(pixel), draw(pixel))
		consider(pixel, cands)
	}
	for it := range opts.iterations {
		forward = it%2 == 0
		for scan := range n {
			pixel, step := scan, 1
			if !forward {
				pixel, step = n-1-scan, -1
			}
			y, x := pixel/qw, pixel%qw
			cands = cands[:0]
			if forward && x > 0 || !forward && x < qw-1 {
				cands = append(cands, shifted(matches[pixel-step], 0, step))
			}
			if forward && y > 0 || !forward && y < qh-1 {
				cands = append(cands, shifted(matches[pixel-step*qw], step, 0))
			}
			if seed != nil {
				cands = append(cands, seed[pixel])
			}
			m := matches[pixel]
			for _, r := range [...]int{8, 4, 2, 1} {
				v := next()
				sp := uint64(2*r + 1)
				cands = append(cands, shifted(m, int(v%sp)-r, int((v>>32)%sp)-r))
			}
			cands = append(cands, draw(pixel), draw(pixel))
			consider(pixel, cands)
		}
	}
	result := &Sprite{Name: query.Name, Frame: query.Frame, W: 2 * qw, H: 2 * qh, Pix: make([]byte, 4*n), Alpha: make([]bool, 4*n)}
	var stats SpriteStats
	for pixel, m := range matches {
		y, x := pixel/qw, pixel%qw
		for k, o := range [4][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}} {
			i := (2*y+o[0])*result.W + 2*x + o[1]
			if m < 0 {
				result.Pix[i], result.Alpha[i] = query.Pix[pixel], query.Alpha[pixel]
				continue
			}
			result.Pix[i], result.Alpha[i] = examples[m].block[k], examples[m].alpha[k]
		}
		if m < 0 {
			stats.Unmatched++
		} else {
			stats.MeanCost += float64(costs[pixel]) / float64(n)
		}
	}
	stats.Luma1x, stats.Opaque1x = measure(query, pal)
	stats.Luma2x, stats.Opaque2x = measure(result, pal)
	return result, matches, stats
}

// measure returns the mean opaque luma and the opaque share of a Sprite.
func measure(s *Sprite, pal *[256][3]int32) (float64, float64) {
	var t float64
	n := 0
	for i, a := range s.Alpha {
		if a {
			c := pal[s.Pix[i]]
			t += float64(c[0])*0.2126 + float64(c[1])*0.7152 + float64(c[2])*0.0722
			n++
		}
	}
	return t / float64(max(n, 1)), float64(n) / float64(max(len(s.Alpha), 1))
}

func reduce(opaque []byte, alp []byte) byte {
	switch len(opaque) {
	case 1:
		return opaque[0]
	case 2:
		return alp[int(opaque[0])*256+int(opaque[1])]
	case 3:
		return alp[int(alp[int(opaque[0])*256+int(opaque[1])])*256+int(opaque[2])]
	default:
		top := alp[int(opaque[0])*256+int(opaque[1])]
		bottom := alp[int(opaque[2])*256+int(opaque[3])]
		return alp[int(top)*256+int(bottom)]
	}
}

// epx is the Scale2x rule on indexed pixels with transparency as its own value.
func epx(s *Sprite) *Sprite {
	at := func(y, x int) int {
		if y < 0 || y >= s.H || x < 0 || x >= s.W || !s.Alpha[y*s.W+x] {
			return keyClear
		}
		return int(s.Pix[y*s.W+x])
	}
	r := &Sprite{W: 2 * s.W, H: 2 * s.H, Pix: make([]byte, 4*s.W*s.H), Alpha: make([]bool, 4*s.W*s.H)}
	put := func(y, x, v int) {
		if v != keyClear {
			r.Pix[y*r.W+x], r.Alpha[y*r.W+x] = byte(v), true
		}
	}
	for y := range s.H {
		for x := range s.W {
			p, a, b, c, d := at(y, x), at(y-1, x), at(y, x+1), at(y, x-1), at(y+1, x)
			p1, p2, p3, p4 := p, p, p, p
			if c == a && c != d && a != b {
				p1 = a
			}
			if a == b && a != c && b != d {
				p2 = b
			}
			if d == c && d != b && c != a {
				p3 = c
			}
			if b == d && b != a && d != c {
				p4 = d
			}
			put(2*y, 2*x, p1)
			put(2*y, 2*x+1, p2)
			put(2*y+1, 2*x, p3)
			put(2*y+1, 2*x+1, p4)
		}
	}
	return r
}

func windowFeature(key func(dy, dx int) int16, contributions []float32) [dims]int16 {
	var sums [dims]float32
	pos := 0
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			k := int(key(dy, dx))
			base := (pos*keys + k) * dims
			for d := range dims {
				sums[d] += contributions[base+d]
			}
			pos++
		}
	}
	var out [dims]int16
	for d := range dims {
		out[d] = int16(math.Round(float64(sums[d])))
	}
	return out
}

func keyValues(k int, pal *[256][3]int32) [chans]float64 {
	if k == keyClear {
		return [chans]float64{0, 0, 0, 0}
	}
	return [chans]float64{float64(pal[k][0]), float64(pal[k][1]), float64(pal[k][2]), 255}
}

func fitBasis(planes []plane, examples []example, pal [256][3]int32) []float32 {
	cov := make([]float64, spritePatchValues*spritePatchValues)
	var values [spritePatchValues]float64
	stride := max(1, len(examples)/60000)
	for pi := range planes {
		p := &planes[pi]
		for i := 0; i < p.w*p.h; i += stride {
			y, x := i/p.w, i%p.w
			comp := 0
			for dy := -radius; dy <= radius; dy++ {
				for dx := -radius; dx <= radius; dx++ {
					k := keyClear
					if yy, xx := y+dy, x+dx; yy >= 0 && yy < p.h && xx >= 0 && xx < p.w {
						k = int(p.key[yy*p.w+xx])
					}
					v := keyValues(k, &pal)
					copy(values[comp:comp+chans], v[:])
					comp += chans
				}
			}
			for l := range spritePatchValues {
				for r := 0; r <= l; r++ {
					cov[l*spritePatchValues+r] += values[l] * values[r]
				}
			}
		}
	}
	for l := range spritePatchValues {
		for r := 0; r < l; r++ {
			cov[r*spritePatchValues+l] = cov[l*spritePatchValues+r]
		}
	}
	vectors := make([]float64, dims*spritePatchValues)
	for i := range vectors {
		vectors[i] = float64(int64(splitmix64(uint64(i)+0xb6c8e9cf570932bd)>>11)&0xfffff)/524288 - 1
	}
	spriteOrthonormalize(vectors)
	nextV := make([]float64, len(vectors))
	for range 32 {
		for d := range dims {
			in := vectors[d*spritePatchValues : (d+1)*spritePatchValues]
			outV := nextV[d*spritePatchValues : (d+1)*spritePatchValues]
			for row := range spritePatchValues {
				s := 0.0
				for col, v := range in {
					s += cov[row*spritePatchValues+col] * v
				}
				outV[row] = s
			}
		}
		spriteOrthonormalize(nextV)
		vectors, nextV = nextV, vectors
	}
	r := make([]float32, len(vectors))
	for i, v := range vectors {
		r[i] = float32(v)
	}
	return r
}

func spriteOrthonormalize(v []float64) {
	for cur := range dims {
		vec := v[cur*spritePatchValues : (cur+1)*spritePatchValues]
		for prior := 0; prior < cur; prior++ {
			o := v[prior*spritePatchValues : (prior+1)*spritePatchValues]
			dot := 0.0
			for i, x := range vec {
				dot += x * o[i]
			}
			for i := range vec {
				vec[i] -= dot * o[i]
			}
		}
		n := 0.0
		for _, x := range vec {
			n += x * x
		}
		n = math.Sqrt(n)
		if n < 1e-20 {
			clear(vec)
			vec[cur%spritePatchValues] = 1
			continue
		}
		for i := range vec {
			vec[i] /= n
		}
	}
}

func spriteContributions(basis []float32, pal [256][3]int32) []float32 {
	r := make([]float32, span*span*keys*dims)
	for pos := range span * span {
		for k := range keys {
			v := keyValues(k, &pal)
			for d := range dims {
				var s float32
				for c := range chans {
					s += float32(v[c]) * basis[d*spritePatchValues+pos*chans+c]
				}
				r[(pos*keys+k)*dims+d] = s
			}
		}
	}
	return r
}

func spriteFeatureCost(a, b *[dims]int16) int32 {
	var t int32
	for i := range dims {
		d := int32(a[i]) - int32(b[i])
		t += d * d
	}
	return t
}
