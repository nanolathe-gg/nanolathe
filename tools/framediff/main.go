// Command framediff compares two PNG captures pixel for pixel and reports how
// they differ. It is the parity gate of docs/DESIGN_GPU_RENDERER.md §6: a
// classic capture against its recorded baseline (plan Phase 1), or a modern
// capture against the classic one (Phases 2 and 3).
//
//	framediff [-max N] [-out diff.png] [-block 8] a.png b.png
//
// Output is the count and share of differing pixels, then the difference
// clusters — connected groups of differing blocks — largest first, each with
// its bounding box in pixels, so a reader can tell "one sprite moved" from
// "the fog pass is wrong" without opening an image. With -out, a diff image is
// written: the first capture dimmed, differing pixels in magenta.
//
// Exit status: 0 when the count is at most -max (default 0), 1 when it is
// larger, 2 when the images cannot be compared at all (unreadable, or
// different sizes). Standard library only.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type cluster struct {
	count          int // differing pixels
	x0, y0, x1, y1 int // inclusive pixel bounds
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("framediff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	maxDiff := fs.Int("max", 0, "largest number of differing pixels that still passes")
	out := fs.String("out", "", "write a diff PNG here (first image dimmed, differences in magenta)")
	block := fs.Int("block", 8, "cluster grid size in pixels")
	top := fs.Int("top", 12, "how many clusters to list")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: framediff [-max N] [-out diff.png] a.png b.png")
		return 2
	}
	if *block < 1 {
		*block = 1
	}
	a, err := load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "framediff:", err)
		return 2
	}
	b, err := load(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "framediff:", err)
		return 2
	}
	if a.Rect != b.Rect {
		fmt.Fprintf(stderr, "framediff: size mismatch: %s is %dx%d, %s is %dx%d\n",
			fs.Arg(0), a.Rect.Dx(), a.Rect.Dy(), fs.Arg(1), b.Rect.Dx(), b.Rect.Dy())
		return 2
	}

	w, h := a.Rect.Dx(), a.Rect.Dy()
	differing := make([]bool, w*h)
	count := 0
	for y := 0; y < h; y++ {
		ra := a.Pix[y*a.Stride : y*a.Stride+w*4]
		rb := b.Pix[y*b.Stride : y*b.Stride+w*4]
		for x := 0; x < w; x++ {
			i := x * 4
			// Alpha is compared too: an executor that writes a transparent
			// pixel where the other wrote opaque black is a difference.
			if ra[i] != rb[i] || ra[i+1] != rb[i+1] || ra[i+2] != rb[i+2] || ra[i+3] != rb[i+3] {
				differing[y*w+x] = true
				count++
			}
		}
	}

	fmt.Fprintf(stdout, "%dx%d: %d differing pixels (%.4f%%)\n", w, h, count, 100*float64(count)/float64(w*h))
	if count > 0 {
		clusters := clusterize(differing, w, h, *block)
		sort.Slice(clusters, func(i, j int) bool { return clusters[i].count > clusters[j].count })
		fmt.Fprintf(stdout, "%d clusters (block %d):\n", len(clusters), *block)
		for i, c := range clusters {
			if i >= *top {
				fmt.Fprintf(stdout, "  ... %d more\n", len(clusters)-i)
				break
			}
			fmt.Fprintf(stdout, "  %6d px  at (%d,%d)-(%d,%d)  %dx%d\n", c.count, c.x0, c.y0, c.x1, c.y1, c.x1-c.x0+1, c.y1-c.y0+1)
		}
	}

	if *out != "" {
		if err := writeDiff(*out, a, differing, w, h); err != nil {
			fmt.Fprintln(stderr, "framediff:", err)
			return 2
		}
	}
	if count > *maxDiff {
		return 1
	}
	return 0
}

// clusterize groups differing pixels into connected components on a coarse
// block grid: two differing pixels are in one cluster when the blocks that
// hold them touch (eight-connected). The grid keeps a full-screen difference
// from turning into thousands of one-pixel clusters while still separating
// two sprites that moved independently.
func clusterize(differing []bool, w, h, block int) []cluster {
	bw := (w + block - 1) / block
	bh := (h + block - 1) / block
	hit := make([]bool, bw*bh)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if differing[y*w+x] {
				hit[(y/block)*bw+x/block] = true
			}
		}
	}
	label := make([]int, bw*bh)
	var clusters []cluster
	stack := make([]int, 0, 64)
	for start := range hit {
		if !hit[start] || label[start] != 0 {
			continue
		}
		id := len(clusters) + 1
		c := cluster{x0: w, y0: h, x1: -1, y1: -1}
		label[start] = id
		stack = append(stack[:0], start)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			bx, by := cur%bw, cur/bw
			// Fold the block's differing pixels into the cluster.
			for y := by * block; y < (by+1)*block && y < h; y++ {
				for x := bx * block; x < (bx+1)*block && x < w; x++ {
					if !differing[y*w+x] {
						continue
					}
					c.count++
					if x < c.x0 {
						c.x0 = x
					}
					if x > c.x1 {
						c.x1 = x
					}
					if y < c.y0 {
						c.y0 = y
					}
					if y > c.y1 {
						c.y1 = y
					}
				}
			}
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := bx+dx, by+dy
					if nx < 0 || ny < 0 || nx >= bw || ny >= bh {
						continue
					}
					n := ny*bw + nx
					if hit[n] && label[n] == 0 {
						label[n] = id
						stack = append(stack, n)
					}
				}
			}
		}
		clusters = append(clusters, c)
	}
	return clusters
}

func writeDiff(path string, a *image.RGBA, differing []bool, w, h int) error {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*a.Stride + x*4
			if differing[y*w+x] {
				out.SetRGBA(x, y, color.RGBA{255, 0, 255, 255})
				continue
			}
			out.SetRGBA(x, y, color.RGBA{a.Pix[i] / 3, a.Pix[i+1] / 3, a.Pix[i+2] / 3, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, out)
}

// load decodes a PNG into a straight RGBA image so both captures are compared
// in one representation whatever colour model the encoder chose.
func load(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, nil
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			rgba.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return rgba, nil
}
