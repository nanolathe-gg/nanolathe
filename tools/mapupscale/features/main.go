// Command features extracts the sprite (GAF) map features a retail map
// places, as indexed PNGs with statistics, so a 2x feature upscale can be
// researched against real examples. Research tool; output is derived retail
// art and is never committed.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	retailpalette "github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/vfs"
)

func main() {
	root := flag.String("root", defaultAssetRoot(), "Total Annihilation asset root")
	mapName := flag.String("map", "Great Divide", "map name, with or without maps/ and .tnt")
	out := flag.String("out", "/tmp/features", "output directory")
	census := flag.Bool("census", false, "print a size histogram over every sprite feature definition")
	flag.Parse()
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(*root); err != nil {
		fatalf("mount %s: %v", *root, err)
	}
	defs, err := content.CompileFeatures(fs)
	if err != nil {
		fatalf("features: %v", err)
	}
	tables, err := retailpalette.Load(fs)
	if err != nil {
		fatalf("palette: %v", err)
	}
	pal := make(color.Palette, 256)
	for i := range 256 {
		r, g, b, _ := tables.RGBA(byte(i))
		pal[i] = color.RGBA{r, g, b, 255}
	}
	gafs := map[string]*formats.GAF{}
	loadGAF := func(filename string) *formats.GAF {
		key := strings.ToLower(filename)
		if g, ok := gafs[key]; ok {
			return g
		}
		logical := key
		if !strings.HasSuffix(logical, ".gaf") {
			logical += ".gaf"
		}
		if !strings.Contains(logical, "/") {
			logical = "anims/" + logical
		}
		g, err := formats.LoadGAFFile(fs, logical)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", logical, err)
			g = nil
		}
		gafs[key] = g
		return g
	}
	if *census {
		runCensus(defs, loadGAF)
		return
	}
	logical, err := findMap(fs, *mapName)
	if err != nil {
		fatalf("%v", err)
	}
	tnt, err := formats.LoadTNTFile(fs, logical)
	if err != nil {
		fatalf("load %s: %v", logical, err)
	}
	counts := map[int]int{}
	for _, a := range tnt.Attributes {
		if a.Feature < 0xFFFB {
			counts[int(a.Feature)]++
		}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("map=%s feature-table=%d\n", logical, len(tnt.FeatureTable))
	for index, rec := range tnt.FeatureTable {
		name := strings.ToLower(rec.Name)
		def := defs[name]
		if def == nil {
			fmt.Printf("%-16s placements=%-5d MISSING definition\n", rec.Name, counts[index])
			continue
		}
		if def.Filename == "" {
			fmt.Printf("%-16s placements=%-5d object=%s (3DO)\n", rec.Name, counts[index], def.Object)
			continue
		}
		g := loadGAF(def.Filename)
		if g == nil {
			continue
		}
		for _, seq := range []struct{ label, name string }{{"idle", def.SeqName}, {"shadow", def.SeqNameShad}, {"burn", def.SeqNameBurn}, {"reclaim", def.SeqNameReclamate}, {"die", def.SeqNameDie}} {
			if seq.name == "" {
				continue
			}
			entry, ok := g.Find(seq.name)
			if !ok || len(entry.Frames) == 0 || entry.Frames[0].Frame == nil {
				fmt.Printf("%-16s %-7s seq=%s NOT FOUND in %s\n", rec.Name, seq.label, seq.name, def.Filename)
				continue
			}
			fr := entry.Frames[0].Frame
			st := stats(fr)
			fmt.Printf("%-16s placements=%-5d %-7s %s/%s %dx%d off=(%d,%d) frames=%d anim=%d trans=%d foot=%dx%d colors=%d opaque=%.2f uniform2x2=%.2f runs/px=%.2f\n",
				rec.Name, counts[index], seq.label, def.Filename, seq.name, fr.Width, fr.Height, fr.XOffset, fr.YOffset,
				len(entry.Frames), def.Animating, def.AnimTrans, def.FootprintX, def.FootprintZ, st.colors, st.opaque, st.uniform, st.runs)
			if seq.label == "idle" || seq.label == "shadow" {
				for fi, ref := range entry.Frames {
					if ref.Frame == nil || (fi > 0 && fi != len(entry.Frames)/2) {
						continue
					}
					file := filepath.Join(*out, fmt.Sprintf("%s-%s-%d.png", name, seq.label, fi))
					if err := writeFrame(file, ref.Frame, pal); err != nil {
						fatalf("%v", err)
					}
				}
			}
		}
	}
}

type frameStats struct {
	colors  int
	opaque  float64
	uniform float64
	runs    float64
}

// stats summarises how "pixel-art" a frame is: distinct colours, opaque
// coverage, share of opaque 2x2 blocks that are one flat colour, and mean
// horizontal runs per pixel (lower = flatter).
func stats(f *formats.GAFFrame) frameStats {
	w, h := int(f.Width), int(f.Height)
	var seen [256]bool
	opaque, uniform, blocks, runs := 0, 0, 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if f.Transparent[i] {
				continue
			}
			opaque++
			seen[f.Pixels[i]] = true
			if x == 0 || f.Transparent[i-1] || f.Pixels[i-1] != f.Pixels[i] {
				runs++
			}
			if x+1 < w && y+1 < h && !f.Transparent[i+1] && !f.Transparent[i+w] && !f.Transparent[i+w+1] {
				blocks++
				if f.Pixels[i] == f.Pixels[i+1] && f.Pixels[i] == f.Pixels[i+w] && f.Pixels[i] == f.Pixels[i+w+1] {
					uniform++
				}
			}
		}
	}
	var st frameStats
	for _, s := range seen {
		if s {
			st.colors++
		}
	}
	if w*h > 0 {
		st.opaque = float64(opaque) / float64(w*h)
	}
	if blocks > 0 {
		st.uniform = float64(uniform) / float64(blocks)
	}
	if opaque > 0 {
		st.runs = float64(runs) / float64(opaque)
	}
	return st
}

func writeFrame(path string, f *formats.GAFFrame, pal color.Palette) error {
	w, h := int(f.Width), int(f.Height)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		if f.Transparent[i] {
			continue
		}
		c := pal[f.Pixels[i]].(color.RGBA)
		img.Set(i%w, i/w, color.NRGBA{c.R, c.G, c.B, 255})
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

func runCensus(defs map[string]*content.FeatureDef, loadGAF func(string) *formats.GAF) {
	type row struct {
		name  string
		w, h  int
		anim  int32
		file  string
		trans int32
	}
	var rows []row
	hist := map[string]int{}
	for name, def := range defs {
		if def.Filename == "" || def.SeqName == "" {
			continue
		}
		g := loadGAF(def.Filename)
		if g == nil {
			continue
		}
		entry, ok := g.Find(def.SeqName)
		if !ok || len(entry.Frames) == 0 || entry.Frames[0].Frame == nil {
			continue
		}
		fr := entry.Frames[0].Frame
		rows = append(rows, row{name, int(fr.Width), int(fr.Height), def.Animating, def.Filename, def.AnimTrans})
		bucket := fmt.Sprintf("%dx%d", (int(fr.Width)+15)/16*16, (int(fr.Height)+15)/16*16)
		hist[bucket]++
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].w*rows[i].h < rows[j].w*rows[j].h })
	fmt.Printf("sprite features=%d (of %d definitions)\n", len(rows), len(defs))
	keys := make([]string, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  <=%-8s %d\n", k, hist[k])
	}
	anim, trans := 0, 0
	for _, r := range rows {
		if r.anim != 0 {
			anim++
		}
		if r.trans != 0 {
			trans++
		}
	}
	fmt.Printf("animating=%d animtrans=%d largest=%s %dx%d smallest=%s %dx%d\n", anim, trans,
		rows[len(rows)-1].name, rows[len(rows)-1].w, rows[len(rows)-1].h, rows[0].name, rows[0].w, rows[0].h)
}

func defaultAssetRoot() string {
	if configured := os.Getenv("OPENTA_TA_ROOT"); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "TotalAnnihilation"
	}
	return filepath.Join(home, "TotalAnnihilation")
}

func findMap(fs *vfs.FS, requested string) (string, error) {
	wanted := strings.ToLower(strings.TrimSpace(requested))
	wanted = strings.TrimSuffix(strings.TrimPrefix(wanted, "maps/"), ".tnt")
	for _, entry := range fs.Entries() {
		logical := strings.ToLower(filepath.ToSlash(entry.Path))
		if entry.IsDir || !strings.HasPrefix(logical, "maps/") || !strings.HasSuffix(logical, ".tnt") {
			continue
		}
		if strings.TrimSuffix(strings.TrimPrefix(logical, "maps/"), ".tnt") == wanted {
			return entry.Path, nil
		}
	}
	return "", fmt.Errorf("map %q not found", requested)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "features: "+format+"\n", args...)
	os.Exit(1)
}
