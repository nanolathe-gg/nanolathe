package formats

import (
	"crypto/sha256"
	"fmt"
	"unsafe"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// GAFSource owns a validated encoded bank for on-demand presentation. Metadata
// and returned frames are immutable. Unlike LoadGAF, retaining a source does
// not retain decoded rasters [fmt gaf "Nanolathe on-demand source"].
type GAFSource struct {
	data   []byte
	meta   *GAFMetadata
	digest [32]byte
}

// LoadGAFSourceFile reads at most maxBytes and takes ownership of that private
// buffer. Eager GAF loaders retain their existing safety defaults.
func LoadGAFSourceFile(fs vfs.FSOps, name string, maxBytes int64, limits GAFLimits) (*GAFSource, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("gaf: invalid source byte limit")
	}
	data, err := readVFSWithLimit(fs, name, maxBytes)
	if err != nil {
		return nil, err
	}
	return loadGAFSource(data, limits)
}

func loadGAFSource(data []byte, limits GAFLimits) (*GAFSource, error) {
	meta, err := LoadGAFMetadataWithLimits(data, limits)
	if err != nil {
		return nil, err
	}
	return &GAFSource{data: data, meta: meta, digest: sha256.Sum256(data)}, nil
}

// Metadata returns a borrowed immutable index. It contains no decoded pixels.
func (s *GAFSource) Metadata() *GAFMetadata {
	if s == nil {
		return nil
	}
	return s.meta
}

// EncodedBytes is the retained source buffer size.
func (s *GAFSource) EncodedBytes() uint64 {
	if s == nil {
		return 0
	}
	return uint64(len(s.data))
}

// Digest identifies the validated bytes across source-cache eviction/reload.
func (s *GAFSource) Digest() [32]byte {
	if s == nil {
		return [32]byte{}
	}
	return s.digest
}

// Frame materializes one entry frame and its reachable child graph. maxPixels
// bounds both unique geometry and expanded traversal before raster allocation.
// This also bounds a consumer that recursively copies aliased children. Whole-bank graph,
// expansion and RLE checks have already run, including frames never requested.
// Aliased children share their immutable decoded frame within this result;
// no decoded frame is retained by the source. The caller owns cache policy.
func (s *GAFSource) Frame(name string, index int, maxPixels uint64) (*GAFFrame, error) {
	if s == nil || s.meta == nil || maxPixels == 0 {
		return nil, fmt.Errorf("gaf: invalid frame request")
	}
	entry, ok := s.meta.Find(name)
	if !ok || index < 0 || index >= len(entry.Frames) {
		return nil, fmt.Errorf("gaf: missing entry frame %q %d", name, index)
	}
	root := entry.Frames[index].Frame
	if root.expandedPixels > maxPixels {
		return nil, fmt.Errorf("gaf: selected frame expanded pixels exceed limit")
	}
	seen := make(map[*GAFMetadataFrame]bool)
	var pixels uint64
	var visit func(*GAFMetadataFrame) error
	visit = func(f *GAFMetadataFrame) error {
		if seen[f] {
			return nil
		}
		seen[f] = true
		n := uint64(f.Width) * uint64(f.Height)
		if n > maxPixels-pixels {
			return fmt.Errorf("gaf: selected frame pixels exceed limit")
		}
		pixels += n
		for _, child := range f.Subframes {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	frames := make(map[*GAFMetadataFrame]*GAFFrame, len(seen))
	frame, err := materializeGAFFrame(s.data, root, frames)
	if err != nil {
		return nil, err
	}
	for _, f := range frames {
		f.Transient = true
		if f.directRaster != nil {
			f.directRaster.Transient = true
		}
	}
	return frame, nil
}

// GAFFrameBytes counts frame headers, child slices and owned raster storage
// once per shared node. Plain planes
// alias Pixels/Transparent in decoded and doubled frames; a direct RLE view
// owns one extra byte plane. This is cache accounting, not authored metadata.
func GAFFrameBytes(root *GAFFrame) uint64 {
	seen := make(map[*GAFFrame]bool)
	var walk func(*GAFFrame) uint64
	walk = func(f *GAFFrame) uint64 {
		if f == nil || seen[f] {
			return 0
		}
		seen[f] = true
		n := uint64(unsafe.Sizeof(*f)) + uint64(cap(f.Subframes))*uint64(unsafe.Sizeof(f)) + uint64(cap(f.Pixels)+cap(f.Transparent))
		if !sameByteSlice(f.PlainPixels, f.Pixels) {
			n += uint64(cap(f.PlainPixels) + cap(f.PlainTransparent))
		}
		if f.directRaster != nil {
			n += uint64(unsafe.Sizeof(*f.directRaster)) + uint64(cap(f.directRaster.Pixels))
		}
		for _, child := range f.Subframes {
			n += walk(child)
		}
		return n
	}
	return walk(root)
}
