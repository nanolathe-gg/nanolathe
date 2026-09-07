// Package framediff compares two frame captures pixel for pixel and reports how
// they differ. It is the comparison core of the parity gate in
// docs/DESIGN_GPU_RENDERER.md §6: a classic capture against its recorded
// baseline (plan Phase 1), or a modern (GPU) capture against the classic one
// (Phases 2 and 3). The command tools/framediff is a thin CLI over this package;
// cmd/nanolathe --shot-renderer both calls it in-process to diff the two
// captures it produces.
//
// The package is pure standard library — no Ebitengine, no device — so it stays
// on the device-free side of the architecture boundary and can be tested without
// a window (C-G10).
package framediff

import (
	"errors"
	"image"
	"image/color"
	"sort"
)

// ErrSizeMismatch is returned by Compare when the two images do not share the
// same bounds. The two captures cannot be compared at all in that case; the
// caller reports it (tools/framediff exits 2).
var ErrSizeMismatch = errors.New("framediff: image size mismatch")

// Cluster is one connected group of differing blocks with the pixel bounds of
// the differences inside it. The bounds are inclusive.
type Cluster struct {
	Count          int // differing pixels in this cluster
	X0, Y0, X1, Y1 int // inclusive pixel bounds of the differences
}

// Result is the outcome of one comparison: the differing-pixel count against the
// total, the per-pixel difference mask (row-major, Width*Height entries), and the
// difference clusters (connected coarse-block components), largest first.
type Result struct {
	Width, Height int
	Count         int       // differing pixels
	Total         int       // Width*Height
	Differing     []bool    // per-pixel mask, row-major; len == Total
	Clusters      []Cluster // sorted by Count, largest first
}

// Compare compares a against b and reports the differences, grouping them into
// clusters on a coarse grid of the given block size (a block below 1 is treated
// as 1). It returns ErrSizeMismatch, with a zero Result, when the two images do
// not share the same bounds.
//
// Alpha is compared too: an executor that writes a transparent pixel where the
// other wrote opaque black is a difference. The comparison is exact per channel;
// there is no tolerance here (the parity gate of §6 asserts zero for Phase 2).
func Compare(a, b image.Image, block int) (Result, error) {
	ra := toRGBA(a)
	rb := toRGBA(b)
	if ra.Rect.Dx() != rb.Rect.Dx() || ra.Rect.Dy() != rb.Rect.Dy() {
		return Result{}, ErrSizeMismatch
	}
	if block < 1 {
		block = 1
	}

	w, h := ra.Rect.Dx(), ra.Rect.Dy()
	differing := make([]bool, w*h)
	count := 0
	for y := 0; y < h; y++ {
		rowA := ra.Pix[y*ra.Stride : y*ra.Stride+w*4]
		rowB := rb.Pix[y*rb.Stride : y*rb.Stride+w*4]
		for x := 0; x < w; x++ {
			i := x * 4
			if rowA[i] != rowB[i] || rowA[i+1] != rowB[i+1] || rowA[i+2] != rowB[i+2] || rowA[i+3] != rowB[i+3] {
				differing[y*w+x] = true
				count++
			}
		}
	}

	res := Result{
		Width:     w,
		Height:    h,
		Count:     count,
		Total:     w * h,
		Differing: differing,
	}
	if count > 0 {
		res.Clusters = clusterize(differing, w, h, block)
		sort.Slice(res.Clusters, func(i, j int) bool { return res.Clusters[i].Count > res.Clusters[j].Count })
	}
	return res, nil
}

// clusterize groups differing pixels into connected components on a coarse block
// grid: two differing pixels are in one cluster when the blocks that hold them
// touch (eight-connected). The grid keeps a full-screen difference from turning
// into thousands of one-pixel clusters while still separating two sprites that
// moved independently.
func clusterize(differing []bool, w, h, block int) []Cluster {
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
	var clusters []Cluster
	stack := make([]int, 0, 64)
	for start := range hit {
		if !hit[start] || label[start] != 0 {
			continue
		}
		id := len(clusters) + 1
		c := Cluster{X0: w, Y0: h, X1: -1, Y1: -1}
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
					c.Count++
					if x < c.X0 {
						c.X0 = x
					}
					if x > c.X1 {
						c.X1 = x
					}
					if y < c.Y0 {
						c.Y0 = y
					}
					if y > c.Y1 {
						c.Y1 = y
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

// DiffImage renders the difference as a picture: the first capture dimmed to a
// third of its brightness with every differing pixel painted magenta. differing
// is the per-pixel mask (row-major, w*h entries) from a Result. The returned
// image is a fresh opaque RGBA of size w×h.
func DiffImage(a image.Image, differing []bool, w, h int) *image.RGBA {
	ra := toRGBA(a)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < ra.Rect.Dx() && y < ra.Rect.Dy() && differing[y*w+x] {
				out.SetRGBA(x, y, color.RGBA{255, 0, 255, 255})
				continue
			}
			i := y*ra.Stride + x*4
			if i+3 < len(ra.Pix) {
				out.SetRGBA(x, y, color.RGBA{ra.Pix[i] / 3, ra.Pix[i+1] / 3, ra.Pix[i+2] / 3, 255})
			}
		}
	}
	return out
}

// toRGBA returns img as a straight *image.RGBA so both captures are compared in
// one representation whatever colour model the decoder chose. An image that is
// already *image.RGBA is returned as-is.
func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			rgba.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return rgba
}
