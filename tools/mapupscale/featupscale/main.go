// Command featupscale is a 2x upscaler for sprite (GAF) map features. It
// applies the terrain upscaler's premise to sprites: every authored 2x2 block
// of every frame in a GAF family, reduced through the retail palette blend,
// is an example of how a pixel of that colour looks one octave up.
// Transparency is carried as a fifth feature channel so silhouettes get
// authored edge detail instead of stair steps. Research tool for remastering;
// output is derived retail art and is never committed. See README.md.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nanolathe/nanolathe/formats"
	retailpalette "github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/vfs"
)

const (
	radius      = 2
	span        = 2*radius + 1
	chans       = 4 // R, G, B, opacity
	patchValues = span * span * chans
	dims        = 8
	keyClear    = 256 // parent key of a transparent reduced pixel
	keys        = 257
	// colorKey is the transparent index of raw GAF frames [fmt gaf]; output
	// PNGs use it for transparent pixels so they stay palette-indexed.
	colorKey = 9
)

type sprite struct {
	name  string
	frame int
	w, h  int
	pix   []byte
	alpha []bool
}

// plane is one reduced phase of one database sprite.
type plane struct {
	sprite int
	phase  int
	w, h   int
	key    []int16
	begin  int // index of the plane's first example
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

type options struct {
	iterations                      int
	tone, deadzone, seam, coherence int32
	coverage, mismatch              int32
	closeness                       int32
}

var coherenceOffsets = [16][2]int{{-1, -2}, {-1, -1}, {-1, 0}, {-1, 1}, {-1, 2}, {-1, 3}, {-2, -2}, {-2, -1}, {-2, 0}, {-2, 1}, {-2, 2}, {-2, 3}, {0, -1}, {1, -1}, {0, -2}, {1, -2}}

func main() {
	root := flag.String("root", defaultAssetRoot(), "asset root")
	gafDir := flag.String("dir", "anims", "VFS directory the GAF files live in (anims for features, textures for model textures)")
	gafList := flag.String("gaf", "trees", "comma-separated GAF names; the first holds the query entries, all supply examples")
	seq := flag.String("seq", "leaf1", "entry name, comma-separated names, or 'all' for every entry of the first GAF")
	frameIndex := flag.Int("frame", 0, "frame of the entry, -1 for every frame (later frames are seeded from the previous frame's matches)")
	out := flag.String("out", "/tmp/featupscale", "output directory")
	iterations := flag.Int("iterations", 8, "PatchMatch passes")
	tone := flag.Int("tone", 64, "tone weight: squared distance between a block's opaque RGB sum and its parent colour times the opaque count, beyond the dead zone")
	deadzone := flag.Int("deadzone", 0, "tone dead zone per channel")
	seam := flag.Int("seam", 8, "seam weight between adjacent output blocks (opaque pairs only)")
	coherence := flag.Int("coherence", 32, "coherence weight: the candidate's authored surround against the synthesized surround, opacity mismatches included")
	coverage := flag.Int("coverage", 200000, "coverage weight: squared difference between a block's opaque count and four times the query's 3x3 opaque density, so silhouettes neither erode nor grow")
	tie := flag.Int("tie", 2, "minimum opaque pixels for a reduced block to count as opaque (1..4)")
	exclude := flag.String("exclude", "burn,boom,fire,smoke,rec", "comma-separated substrings of entry names left out of the example set (fire, explosion and reclaim frames carry colours the idle art never has)")
	closeness := flag.Int("closeness", 3*24*24, "colour closure: an example block is admitted only when each of its colours is within this squared RGB distance of a colour the query sprite uses, so rare highlights from other entries cannot leak in; -1 disables")
	mismatch := flag.Int("mismatch", 3*64*64, "coherence charge for an opacity mismatch between the candidate's authored surround and the synthesized surround, in squared-RGB units")
	relax := flag.Bool("relax", true, "also accept blocks that reduce to a palette entry one blend step from the parent when they average closer to the parent's colour, as the terrain upscaler does")
	pngDir := flag.String("png", "", "read queries and examples from palette-indexed PNGs in this directory instead of GAFs (a second octave over earlier output)")
	edgeClamp := flag.Bool("clamp", false, "treat pixels beyond a sprite's edge as copies of the edge pixel instead of transparent (opaque textures)")
	writeEPX := flag.Bool("epx", false, "also write a Scale2x result for comparison")
	flag.Parse()
	started := time.Now()
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(*root); err != nil {
		fatalf("mount: %v", err)
	}
	tables, err := retailpalette.Load(fs)
	if err != nil {
		fatalf("palette: %v", err)
	}
	var pal [256][3]int32
	palette := make(color.Palette, 256)
	for i := range 256 {
		r, g, b, _ := tables.RGBA(byte(i))
		pal[i] = [3]int32{int32(r), int32(g), int32(b)}
		palette[i] = color.NRGBA{r, g, b, 255}
	}
	palette[colorKey] = color.NRGBA{0, 0, 0, 0}
	alp := tables.Alpha[:]
	excluded := strings.Split(*exclude, ",")
	wanted := map[string]bool{}
	for _, name := range strings.Split(*seq, ",") {
		wanted[strings.ToLower(strings.TrimSpace(name))] = true
	}
	var queries []*sprite
	var db []*sprite
	if *pngDir != "" {
		entries, err := os.ReadDir(*pngDir)
		if err != nil {
			fatalf("%v", err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".png") || strings.HasSuffix(e.Name(), "-1x.png") || strings.HasSuffix(e.Name(), "-epx.png") {
				continue
			}
			s, err := readIndexedSprite(filepath.Join(*pngDir, e.Name()))
			if err != nil {
				fatalf("%s: %v", e.Name(), err)
			}
			s.name = strings.TrimSuffix(e.Name(), ".png")
			db = append(db, s)
			if wanted["all"] || wanted[strings.ToLower(s.name)] {
				queries = append(queries, s)
			}
		}
	}
	for gi, name := range strings.Split(*gafList, ",") {
		if *pngDir != "" {
			break
		}
		g, err := formats.LoadGAFFile(fs, *gafDir+"/"+strings.ToLower(strings.TrimSpace(name))+".gaf")
		if err != nil {
			fatalf("%s: %v", name, err)
		}
		for ei := range g.Entries {
			e := &g.Entries[ei]
			lower := strings.ToLower(e.Name)
			isQuery := gi == 0 && (wanted["all"] || wanted[lower])
			skip := false
			for _, part := range excluded {
				if part = strings.TrimSpace(part); part != "" && strings.Contains(lower, part) {
					skip = true
				}
			}
			for fi, ref := range e.Frames {
				if ref.Frame == nil || ref.Frame.Width == 0 || ref.Frame.Height == 0 {
					continue
				}
				s := fromFrame(ref.Frame)
				s.name, s.frame = e.Name, fi
				if !skip {
					db = append(db, s)
				}
				if isQuery && (*frameIndex < 0 || fi == *frameIndex) {
					queries = append(queries, s)
				}
			}
		}
	}
	if len(queries) == 0 {
		fatalf("no entry matches -seq %q in the first GAF", *seq)
	}
	clampEdges = *edgeClamp
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatalf("%v", err)
	}
	pairDistance := make([]int32, 256*256)
	for a := range 256 {
		for b := range 256 {
			var t int32
			for c := range 3 {
				d := pal[a][c] - pal[b][c]
				t += d * d
			}
			pairDistance[a*256+b] = t
		}
	}
	planes, examples := buildExamples(db, *tie, alp, &pal)
	basis := fitBasis(planes, examples, pal)
	contributions := makeContributions(basis, pal)
	for pi := range planes {
		p := &planes[pi]
		for y := range p.h {
			for x := range p.w {
				examples[p.begin+y*p.w+x].feat = windowFeature(func(dy, dx int) int16 {
					yy, xx := y+dy, x+dx
					if clampEdges {
						yy, xx = min(max(yy, 0), p.h-1), min(max(xx, 0), p.w-1)
					}
					if yy < 0 || yy >= p.h || xx < 0 || xx >= p.w {
						return keyClear
					}
					return p.key[yy*p.w+xx]
				}, contributions)
			}
		}
	}
	index := indexExamples(examples, pairDistance, alp, &pal, *relax)
	fmt.Printf("examples=%d from %d frames, %s\n", len(examples), len(db), time.Since(started).Round(time.Millisecond))
	opts := options{iterations: *iterations, tone: int32(*tone), deadzone: int32(*deadzone), seam: int32(*seam),
		coherence: int32(*coherence), coverage: int32(*coverage), mismatch: int32(*mismatch), closeness: int32(*closeness)}
	var previous *sprite
	var previousMatches []int32
	for _, query := range queries {
		var seed []int32
		if previous != nil && previous.name == query.name && previous.w == query.w && previous.h == query.h {
			seed = previousMatches
		}
		began := time.Now()
		result, matches, stats := upscale(query, seed, db, planes, examples, index, contributions, pairDistance, &pal, opts)
		base := filepath.Join(*out, fmt.Sprintf("%s-%d", strings.ToLower(query.name), query.frame))
		writeSprite(base+"-2x.png", result, palette)
		writeSprite(base+"-1x.png", query, palette)
		if *writeEPX {
			writeSprite(base+"-epx.png", epx(query), palette)
		}
		fmt.Printf("%s frame %d: %dx%d luma 1x=%.1f 2x=%.1f opaque 1x=%.3f 2x=%.3f unmatched=%d mean-cost=%.0f %s\n",
			query.name, query.frame, query.w, query.h, stats.luma1, stats.luma2, stats.opaque1, stats.opaque2,
			stats.unmatched, stats.meanCost, time.Since(began).Round(time.Millisecond))
		previous, previousMatches = query, matches
	}
}

// buildExamples reduces every database sprite at the four 2x2 phases. A
// block is opaque when at least tie of its pixels are; opaque blocks reduce
// their opaque pixels through the palette blend [03 §3.7].
func buildExamples(db []*sprite, tie int, alp []byte, pal *[256][3]int32) ([]plane, []example) {
	var planes []plane
	var examples []example
	for si, s := range db {
		for phase := range 4 {
			oy, ox := phase/2, phase%2
			pw, ph := (s.w-ox)/2, (s.h-oy)/2
			if pw <= 0 || ph <= 0 {
				continue
			}
			p := plane{sprite: si, phase: phase, w: pw, h: ph, key: make([]int16, pw*ph), begin: len(examples)}
			for y := range ph {
				for x := range pw {
					sy, sx := oy+2*y, ox+2*x
					var ex example
					ex.plane, ex.x, ex.y = int32(len(planes)), int32(x), int32(y)
					idx := [4]int{sy*s.w + sx, sy*s.w + sx + 1, (sy+1)*s.w + sx, (sy+1)*s.w + sx + 1}
					var opaque []byte
					for k, i := range idx {
						ex.block[k], ex.alpha[k] = s.pix[i], s.alpha[i]
						if s.alpha[i] {
							opaque = append(opaque, s.pix[i])
							for c := range 3 {
								ex.sum[c] += int16(pal[s.pix[i]][c])
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

type upscaleStats struct {
	luma1, luma2, opaque1, opaque2, meanCost float64
	unmatched                                int
}

// upscale synthesizes the 2x sprite for one query frame. seed, when given,
// holds the previous frame's matches at the same pixels and is offered as a
// candidate at every visit, which keeps animated entries from flickering.
func upscale(query *sprite, seed []int32, db []*sprite, planes []plane, examples []example, ix exampleIndex,
	contributions []float32, pairDistance []int32, pal *[256][3]int32, opts options) (*sprite, []int32, upscaleStats) {
	qw, qh := query.w, query.h
	n := qw * qh
	qkey := make([]int16, n)
	for i := range qkey {
		if query.alpha[i] {
			qkey[i] = int16(query.pix[i])
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
				if clampEdges {
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
					if yy >= 0 && yy < qh && xx >= 0 && xx < qw && query.alpha[yy*qw+xx] {
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
		if query.alpha[i] {
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
		for i, a := range query.alpha {
			if a {
				used[query.pix[i]] = true
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
	rng := uint64(0x9e3779b97f4a7c15) ^ uint64(query.frame)*0x9e3779b97f4a7c15
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
		c := featureCost(&qfeat[pixel], &ex.feat)
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
			s := db[p.sprite]
			sy, sx := p.phase/2+2*int(ex.y), p.phase%2+2*int(ex.x)
			var t int32
			for _, off := range coherenceOffsets {
				dy, dx := off[0], off[1]
				if !forward {
					dy, dx = 1-dy, 1-dx
				}
				i := (oy+dy)*ow + ox + dx
				if synthA[i] < 0 {
					continue
				}
				ay, ax := sy+dy, sx+dx
				aOpaque := ay >= 0 && ay < s.h && ax >= 0 && ax < s.w && s.alpha[ay*s.w+ax]
				switch {
				case aOpaque && synthA[i] == 1:
					t += pairDistance[int(s.pix[ay*s.w+ax])*256+int(synth[i])]
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
	result := &sprite{name: query.name, frame: query.frame, w: 2 * qw, h: 2 * qh, pix: make([]byte, 4*n), alpha: make([]bool, 4*n)}
	var stats upscaleStats
	for pixel, m := range matches {
		y, x := pixel/qw, pixel%qw
		for k, o := range [4][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}} {
			i := (2*y+o[0])*result.w + 2*x + o[1]
			if m < 0 {
				result.pix[i], result.alpha[i] = query.pix[pixel], query.alpha[pixel]
				continue
			}
			result.pix[i], result.alpha[i] = examples[m].block[k], examples[m].alpha[k]
		}
		if m < 0 {
			stats.unmatched++
		} else {
			stats.meanCost += float64(costs[pixel]) / float64(n)
		}
	}
	stats.luma1, stats.opaque1 = measure(query, pal)
	stats.luma2, stats.opaque2 = measure(result, pal)
	return result, matches, stats
}

// measure returns the mean opaque luma and the opaque share of a sprite.
func measure(s *sprite, pal *[256][3]int32) (float64, float64) {
	var t float64
	n := 0
	for i, a := range s.alpha {
		if a {
			c := pal[s.pix[i]]
			t += float64(c[0])*0.2126 + float64(c[1])*0.7152 + float64(c[2])*0.0722
			n++
		}
	}
	return t / float64(max(n, 1)), float64(n) / float64(max(len(s.alpha), 1))
}

// writeSprite writes a palette-indexed PNG with the retail palette; the
// colour-key index is transparent, matching raw GAF frames.
func writeSprite(path string, s *sprite, palette color.Palette) {
	img := image.NewPaletted(image.Rect(0, 0, s.w, s.h), palette)
	keyed := 0
	for i := range s.pix {
		if !s.alpha[i] {
			img.Pix[i] = colorKey
			continue
		}
		if s.pix[i] == colorKey {
			keyed++
		}
		img.Pix[i] = s.pix[i]
	}
	if keyed > 0 {
		fmt.Fprintf(os.Stderr, "featupscale: %s: %d opaque pixels use the colour-key index and become transparent\n", path, keyed)
	}
	f, err := os.Create(path)
	if err != nil {
		fatalf("%v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fatalf("%v", err)
	}
}

var _ = sort.Ints

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

func fromFrame(f *formats.GAFFrame) *sprite {
	s := &sprite{w: int(f.Width), h: int(f.Height), pix: make([]byte, int(f.Width)*int(f.Height)), alpha: make([]bool, int(f.Width)*int(f.Height))}
	copy(s.pix, f.Pixels)
	for i := range s.alpha {
		s.alpha[i] = !f.Transparent[i]
	}
	return s
}

// readIndexedSprite loads a palette-indexed PNG as a sprite; the colour
// key index is transparent.
func readIndexedSprite(path string) (*sprite, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	p, ok := img.(*image.Paletted)
	if !ok {
		return nil, fmt.Errorf("not palette-indexed")
	}
	w, h := p.Rect.Dx(), p.Rect.Dy()
	s := &sprite{w: w, h: h, pix: make([]byte, w*h), alpha: make([]bool, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := p.Pix[y*p.Stride+x]
			s.pix[y*w+x] = v
			s.alpha[y*w+x] = v != colorKey
		}
	}
	return s, nil
}

// epx is the Scale2x rule on indexed pixels with transparency as its own value.
func epx(s *sprite) *sprite {
	at := func(y, x int) int {
		if y < 0 || y >= s.h || x < 0 || x >= s.w || !s.alpha[y*s.w+x] {
			return keyClear
		}
		return int(s.pix[y*s.w+x])
	}
	r := &sprite{w: 2 * s.w, h: 2 * s.h, pix: make([]byte, 4*s.w*s.h), alpha: make([]bool, 4*s.w*s.h)}
	put := func(y, x, v int) {
		if v != keyClear {
			r.pix[y*r.w+x], r.alpha[y*r.w+x] = byte(v), true
		}
	}
	for y := range s.h {
		for x := range s.w {
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

// clampEdges makes out-of-bounds neighbourhood lookups repeat the edge
// pixel: model textures are opaque and have no silhouette to respect.
var clampEdges bool

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
	cov := make([]float64, patchValues*patchValues)
	var values [patchValues]float64
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
			for l := range patchValues {
				for r := 0; r <= l; r++ {
					cov[l*patchValues+r] += values[l] * values[r]
				}
			}
		}
	}
	for l := range patchValues {
		for r := 0; r < l; r++ {
			cov[r*patchValues+l] = cov[l*patchValues+r]
		}
	}
	vectors := make([]float64, dims*patchValues)
	for i := range vectors {
		vectors[i] = float64(int64(splitmix64(uint64(i)+0xb6c8e9cf570932bd)>>11)&0xfffff)/524288 - 1
	}
	orthonormalize(vectors)
	nextV := make([]float64, len(vectors))
	for range 32 {
		for d := range dims {
			in := vectors[d*patchValues : (d+1)*patchValues]
			outV := nextV[d*patchValues : (d+1)*patchValues]
			for row := range patchValues {
				s := 0.0
				for col, v := range in {
					s += cov[row*patchValues+col] * v
				}
				outV[row] = s
			}
		}
		orthonormalize(nextV)
		vectors, nextV = nextV, vectors
	}
	r := make([]float32, len(vectors))
	for i, v := range vectors {
		r[i] = float32(v)
	}
	return r
}

func orthonormalize(v []float64) {
	for cur := range dims {
		vec := v[cur*patchValues : (cur+1)*patchValues]
		for prior := 0; prior < cur; prior++ {
			o := v[prior*patchValues : (prior+1)*patchValues]
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
			vec[cur%patchValues] = 1
			continue
		}
		for i := range vec {
			vec[i] /= n
		}
	}
}

func makeContributions(basis []float32, pal [256][3]int32) []float32 {
	r := make([]float32, span*span*keys*dims)
	for pos := range span * span {
		for k := range keys {
			v := keyValues(k, &pal)
			for d := range dims {
				var s float32
				for c := range chans {
					s += float32(v[c]) * basis[d*patchValues+pos*chans+c]
				}
				r[(pos*keys+k)*dims+d] = s
			}
		}
	}
	return r
}

func featureCost(a, b *[dims]int16) int32 {
	var t int32
	for i := range dims {
		d := int32(a[i]) - int32(b[i])
		t += d * d
	}
	return t
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	z := x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func defaultAssetRoot() string {
	if v := os.Getenv("NANOLATHE_TA_ROOT"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return home + "/TotalAnnihilation"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "featupscale: "+format+"\n", args...)
	os.Exit(1)
}
