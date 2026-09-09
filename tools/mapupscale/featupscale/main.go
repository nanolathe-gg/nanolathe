// Command featupscale is the sprite half of the 2x upscaler. It applies the
// terrain upscaler's premise to sprite (GAF) map features: every authored 2x2
// block of every frame in a GAF family, reduced through the retail palette
// blend, is an example of how a pixel of that colour looks one octave up.
// Transparency is carried as a fifth feature channel so silhouettes get
// authored edge detail instead of stair steps. Research tool for remastering;
// output is derived retail art and is never committed. See README.md.
//
// The synthesizer itself lives in internal/upscale, which is what the engine
// calls at load time; this command is the wrapper that picks the query
// entries, builds the example set from the named GAFs (or from a directory of
// palette-indexed PNGs) and writes the per-frame PNGs.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nanolathe/nanolathe/formats"
	retailpalette "github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/upscale"
	"github.com/nanolathe/nanolathe/vfs"
)

// colorKey is the transparent index of raw GAF frames [fmt gaf]; output PNGs
// use it for transparent pixels so they stay palette-indexed.
const colorKey = 9

func main() {
	defaults := upscale.DefaultSpriteParams()
	root := flag.String("root", defaultAssetRoot(), "asset root")
	gafDir := flag.String("dir", "anims", "VFS directory the GAF files live in (anims for features, textures for model textures)")
	gafList := flag.String("gaf", "trees", "comma-separated GAF names; the first holds the query entries, all supply examples")
	seq := flag.String("seq", "leaf1", "entry name, comma-separated names, or 'all' for every entry of the first GAF")
	frameIndex := flag.Int("frame", 0, "frame of the entry, -1 for every frame (later frames are seeded from the previous frame's matches)")
	out := flag.String("out", "/tmp/featupscale", "output directory")
	iterations := flag.Int("iterations", defaults.Iterations, "PatchMatch passes")
	tone := flag.Int("tone", defaults.Tone, "tone weight: squared distance between a block's opaque RGB sum and its parent colour times the opaque count, beyond the dead zone")
	deadzone := flag.Int("deadzone", defaults.Deadzone, "tone dead zone per channel")
	seam := flag.Int("seam", defaults.Seam, "seam weight between adjacent output blocks (opaque pairs only)")
	coherence := flag.Int("coherence", defaults.Coherence, "coherence weight: the candidate's authored surround against the synthesized surround, opacity mismatches included")
	coverage := flag.Int("coverage", defaults.Coverage, "coverage weight: squared difference between a block's opaque count and four times the query's 3x3 opaque density, so silhouettes neither erode nor grow")
	tie := flag.Int("tie", defaults.Tie, "minimum opaque pixels for a reduced block to count as opaque (1..4)")
	exclude := flag.String("exclude", "burn,boom,fire,smoke,rec", "comma-separated substrings of entry names left out of the example set (fire, explosion and reclaim frames carry colours the idle art never has)")
	closeness := flag.Int("closeness", defaults.Closeness, "colour closure: an example block is admitted only when each of its colours is within this squared RGB distance of a colour the query sprite uses, so rare highlights from other entries cannot leak in; -1 disables")
	mismatch := flag.Int("mismatch", defaults.Mismatch, "coherence charge for an opacity mismatch between the candidate's authored surround and the synthesized surround, in squared-RGB units")
	relax := flag.Bool("relax", defaults.Relax, "also accept blocks that reduce to a palette entry one blend step from the parent when they average closer to the parent's colour, as the terrain upscaler does")
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
	var pal [256][3]uint8
	palette := make(color.Palette, 256)
	for i := range 256 {
		r, g, b, _ := tables.RGBA(byte(i))
		pal[i] = [3]uint8{r, g, b}
		palette[i] = color.NRGBA{r, g, b, 255}
	}
	palette[colorKey] = color.NRGBA{0, 0, 0, 0}
	alp := tables.Alpha[:]
	excluded := strings.Split(*exclude, ",")
	wanted := map[string]bool{}
	for _, name := range strings.Split(*seq, ",") {
		wanted[strings.ToLower(strings.TrimSpace(name))] = true
	}
	var queries []*upscale.Sprite
	var db []*upscale.Sprite
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
			s.Name = strings.TrimSuffix(e.Name(), ".png")
			db = append(db, s)
			if wanted["all"] || wanted[strings.ToLower(s.Name)] {
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
				s := upscale.FromGAFFrame(ref.Frame)
				s.Name, s.Frame = e.Name, fi
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
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatalf("%v", err)
	}
	params := upscale.SpriteParams{
		Iterations: *iterations, Tone: *tone, Deadzone: *deadzone, Seam: *seam, Coherence: *coherence,
		Coverage: *coverage, Mismatch: *mismatch, Closeness: *closeness, Tie: *tie,
		Relax: *relax, ClampEdges: *edgeClamp,
	}
	set := upscale.BuildSpriteExamples(db, pal, alp, params)
	fmt.Printf("examples=%d from %d frames, %s\n", set.Count(), set.Frames(), time.Since(started).Round(time.Millisecond))
	var previous *upscale.Sprite
	var previousMatches []int32
	for _, query := range queries {
		var seed []int32
		if previous != nil && previous.Name == query.Name && previous.W == query.W && previous.H == query.H {
			seed = previousMatches
		}
		began := time.Now()
		result, matches, stats := set.Upscale(query, seed)
		base := filepath.Join(*out, fmt.Sprintf("%s-%d", strings.ToLower(query.Name), query.Frame))
		writeSprite(base+"-2x.png", result, palette)
		writeSprite(base+"-1x.png", query, palette)
		if *writeEPX {
			writeSprite(base+"-epx.png", upscale.EPX(query), palette)
		}
		fmt.Printf("%s frame %d: %dx%d luma 1x=%.1f 2x=%.1f opaque 1x=%.3f 2x=%.3f unmatched=%d mean-cost=%.0f %s\n",
			query.Name, query.Frame, query.W, query.H, stats.Luma1x, stats.Luma2x, stats.Opaque1x, stats.Opaque2x,
			stats.Unmatched, stats.MeanCost, time.Since(began).Round(time.Millisecond))
		previous, previousMatches = query, matches
	}
}

// writeSprite writes a palette-indexed PNG with the retail palette; the
// colour-key index is transparent, matching raw GAF frames.
func writeSprite(path string, s *upscale.Sprite, palette color.Palette) {
	img := image.NewPaletted(image.Rect(0, 0, s.W, s.H), palette)
	keyed := 0
	for i := range s.Pix {
		if !s.Alpha[i] {
			img.Pix[i] = colorKey
			continue
		}
		if s.Pix[i] == colorKey {
			keyed++
		}
		img.Pix[i] = s.Pix[i]
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

// readIndexedSprite loads a palette-indexed PNG as a sprite; the colour key
// index is transparent.
func readIndexedSprite(path string) (*upscale.Sprite, error) {
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
	s := &upscale.Sprite{W: w, H: h, Pix: make([]byte, w*h), Alpha: make([]bool, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := p.Pix[y*p.Stride+x]
			s.Pix[y*w+x] = v
			s.Alpha[y*w+x] = v != colorKey
		}
	}
	return s, nil
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
