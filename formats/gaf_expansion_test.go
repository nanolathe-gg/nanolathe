package formats

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// Each parent references the next frame twice. The stored graph is linear,
// while a consumer visiting every child occurrence does exponential work.
func gafSharedExpansion(frames int, roots []int, width uint16) []byte {
	const entry = 16
	frameBase := entry + 40 + len(roots)*8
	table := frameBase + frames*24
	pixels := table + (frames-1)*8
	data := make([]byte, pixels+int(width))
	put := func(offset, value int) { binary.LittleEndian.PutUint32(data[offset:], uint32(value)) }
	put(4, 1)
	put(12, entry)
	binary.LittleEndian.PutUint16(data[entry:], uint16(len(roots)))
	copy(data[entry+8:], "shared")
	for i, root := range roots {
		put(entry+40+i*8, frameBase+root*24)
	}
	for i := 0; i < frames; i++ {
		header := frameBase + i*24
		binary.LittleEndian.PutUint16(data[header:], width)
		binary.LittleEndian.PutUint16(data[header+2:], 1)
		if i+1 < frames {
			data[header+10] = 2
			put(header+16, table+i*8)
			put(table+i*8, header+24)
			put(table+i*8+4, header+24)
		} else {
			put(header+16, pixels)
		}
	}
	return data
}

func checkGAFExpansion(t *testing.T, data []byte, limits GAFLimits, want string) {
	t.Helper()
	_, metadataErr := LoadGAFMetadataWithLimits(data, limits)
	_, fullErr := LoadGAFWithLimits(data, limits)
	for _, err := range []error{metadataErr, fullErr} {
		if want == "" {
			if err != nil {
				t.Fatalf("accepted graph: %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}

func TestGAFExpansionLimitsCountCachedChildOccurrences(t *testing.T) {
	data := gafSharedExpansion(3, []int{0}, 4) // Seven visits, 28 expanded pixels.
	limits := DefaultGAFLimits()
	limits.MaxExpandedFrames = 6
	checkGAFExpansion(t, data, limits, "expanded frames")
	limits.MaxExpandedFrames = 7
	limits.MaxExpandedPixels = 27
	checkGAFExpansion(t, data, limits, "expanded pixels")
	limits.MaxExpandedPixels = 28
	checkGAFExpansion(t, data, limits, "")
}

func TestGAFExpansionLimitsAggregateDistinctRoots(t *testing.T) {
	// Index small children first, so the larger roots are assembled from cache.
	data := gafSharedExpansion(4, []int{2, 1, 0}, 1) // 3 + 7 + 15 visits.
	limits := DefaultGAFLimits()
	limits.MaxExpandedFrames = 24
	checkGAFExpansion(t, data, limits, "aggregate expanded frames")
	limits.MaxExpandedFrames = 25
	limits.MaxExpandedPixels = 24
	checkGAFExpansion(t, data, limits, "aggregate expanded pixels")
	limits.MaxExpandedPixels = 25
	checkGAFExpansion(t, data, limits, "")
	// Reusing a top-level frame does not create a second cached variant.
	limits.MaxExpandedFrames = 15
	limits.MaxExpandedPixels = 15
	checkGAFExpansion(t, gafSharedExpansion(4, []int{0, 0}, 1), limits, "")
}

func TestGAFExpansionLimitsRejectTinyExponentialGraph(t *testing.T) {
	checkGAFExpansion(t, gafSharedExpansion(32, []int{0}, 1), DefaultGAFLimits(), "expanded frames")
}

func TestGAFExpansionAccountingCannotWrap(t *testing.T) {
	limits := DefaultGAFLimits()
	limits.MaxExpandedFrames = math.MaxUint64
	limits.MaxExpandedPixels = math.MaxUint64
	// The depth remains allowed, but expanding it would overflow uint64.
	checkGAFExpansion(t, gafSharedExpansion(65, []int{0}, 1), limits, "expanded frames")
	checkGAFExpansion(t, gafSharedExpansion(64, []int{0}, 2), limits, "expanded pixels")
}

func TestGAFExpansionOmittedLimitsStillBoundInput(t *testing.T) {
	limits := DefaultGAFLimits()
	limits.MaxExpandedFrames = 0
	limits.MaxExpandedPixels = 0
	checkGAFExpansion(t, gafSharedExpansion(32, []int{0}, 1), limits, "expanded frames")
}
