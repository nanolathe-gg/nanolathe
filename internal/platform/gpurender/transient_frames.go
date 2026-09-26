package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
)

// Host cache limits, DESIGN_GPU_RENDERER "On-demand effect uploads". Current
// Execute sources are pinned until every scheduler/glow/reflection submission
// has consumed them. An exceptional simultaneous working set can exceed the
// cache; its excess is retired at the end of Execute, never stored per bank.
const transientFrameBytes = 256 << 20
const transientFrameCount = 256

// Reserve a disjoint positive page namespace; -1 remains the absent-page
// sentinel used by reflection runs.
const transientPageBase = 1 << 30

type transientFrame struct {
	frame *formats.GAFFrame
	image *ebiten.Image
	bytes uint64
	used  uint64
}
type transientFrames struct {
	slots []transientFrame
	index map[*formats.GAFFrame]int
	bytes uint64
	clock uint64
}

func (a *transientFrames) evict(index int, release func(*ebiten.Image)) {
	s := &a.slots[index]
	delete(a.index, s.frame)
	a.bytes -= s.bytes
	if s.image != nil {
		release(s.image)
	}
	*s = transientFrame{}
}
func (a *transientFrames) oldest(keepCurrent bool) int {
	oldest := -1
	for i := range a.slots {
		s := &a.slots[i]
		if s.frame == nil || (keepCurrent && s.used == a.clock) {
			continue
		}
		if oldest < 0 || s.used < a.slots[oldest].used {
			oldest = i
		}
	}
	return oldest
}
func (a *transientFrames) trim(limit uint64, reserve uint64, keepCurrent bool, release func(*ebiten.Image)) {
	countLimit := transientFrameCount
	if reserve != 0 {
		countLimit--
	}
	for a.bytes+reserve > limit || len(a.index) > countLimit {
		i := a.oldest(keepCurrent)
		if i < 0 {
			break
		}
		a.evict(i, release)
	}
}
func (a *transientFrames) slot(f *formats.GAFFrame, bytes uint64, release func(*ebiten.Image)) int {
	if i, ok := a.index[f]; ok {
		a.slots[i].used = a.clock
		return i
	}
	a.trim(transientFrameBytes, bytes, true, release)
	if a.index == nil {
		a.index = make(map[*formats.GAFFrame]int)
	}
	i := len(a.slots)
	for j := range a.slots {
		if a.slots[j].frame == nil {
			i = j
			break
		}
	}
	if i == len(a.slots) {
		a.slots = append(a.slots, transientFrame{})
	}
	a.slots[i] = transientFrame{frame: f, bytes: bytes, used: a.clock}
	a.index[f] = i
	a.bytes += bytes
	return i
}

func (r *Renderer) transientFrameFor(f *formats.GAFFrame) sceneEntry {
	w, h := int(f.Width), int(f.Height)
	if w <= 0 || h <= 0 {
		return sceneEntry{}
	}
	pw, ph := w+2*sceneAtlasPad, h+2*sceneAtlasPad
	a := &r.scene.transient
	i := a.slot(f, uint64(pw)*uint64(ph)*4, func(img *ebiten.Image) { img.Deallocate() })
	s := &a.slots[i]
	if s.image == nil {
		// One local padded upload plane. It does not grow either persistent
		// scene scratch buffer to the dimensions of a giant effect frame.
		buf := make([]byte, pw*ph*4)
		for y := 0; y < ph; y++ {
			sy := clampInt(y-sceneAtlasPad, 0, h-1)
			for x := 0; x < pw; x++ {
				sx := clampInt(x-sceneAtlasPad, 0, w-1)
				src, dst := sy*w+sx, (y*pw+x)*4
				if src < len(f.Pixels) {
					buf[dst] = f.Pixels[src]
					if src >= len(f.Transparent) || !f.Transparent[src] {
						buf[dst+1] = 255
					}
				}
				buf[dst+3] = 255
			}
		}
		s.image = ebiten.NewImage(pw, ph)
		s.image.WritePixels(buf)
	}
	// Reserved pages name transient slots; persistent shelf-packed pages keep
	// their existing identity and lifetime. All consumers use pageImage.
	return sceneEntry{page: transientPageBase + int32(i), x: sceneAtlasPad, y: sceneAtlasPad, w: int32(w), h: int32(h), ok: true}
}
