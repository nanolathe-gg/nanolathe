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
//
// The comparison itself lives in internal/framediff; this file is the CLI over
// it, and cmd/nanolathe --shot-renderer both calls the same package in-process.
package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/nanolathe/nanolathe/internal/framediff"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
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

	res, err := framediff.Compare(a, b, *block)
	if errors.Is(err, framediff.ErrSizeMismatch) {
		fmt.Fprintf(stderr, "framediff: size mismatch: %s is %dx%d, %s is %dx%d\n",
			fs.Arg(0), a.Bounds().Dx(), a.Bounds().Dy(), fs.Arg(1), b.Bounds().Dx(), b.Bounds().Dy())
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "framediff:", err)
		return 2
	}

	fmt.Fprintf(stdout, "%dx%d: %d differing pixels (%.4f%%)\n", res.Width, res.Height, res.Count, 100*float64(res.Count)/float64(res.Total))
	if res.Count > 0 {
		fmt.Fprintf(stdout, "%d clusters (block %d):\n", len(res.Clusters), max(*block, 1))
		for i, c := range res.Clusters {
			if i >= *top {
				fmt.Fprintf(stdout, "  ... %d more\n", len(res.Clusters)-i)
				break
			}
			fmt.Fprintf(stdout, "  %6d px  at (%d,%d)-(%d,%d)  %dx%d\n", c.Count, c.X0, c.Y0, c.X1, c.Y1, c.X1-c.X0+1, c.Y1-c.Y0+1)
		}
	}

	if *out != "" {
		diff := framediff.DiffImage(a, res.Differing, res.Width, res.Height)
		if err := writePNG(*out, diff); err != nil {
			fmt.Fprintln(stderr, "framediff:", err)
			return 2
		}
	}
	if res.Count > *maxDiff {
		return 1
	}
	return 0
}

// load decodes a PNG file.
func load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return img, nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
