package render

import (
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TexturePlayer is one cursor embedded in a loaded model primitive. Its owner
// chooses registration and phase-7 lifetime; runtime model instances only read
// its selected frame [03 R-CRD-005 §1][03 §4.4].
//
// Durations are authored whole simulation ticks.  A zero duration is retained
// as authored data; the cursor advances at the next tick rather than inventing
// a replacement duration for malformed content.
//
// Phase 7 advances the cursor while presentation may read it from another
// goroutine (docs/DESIGN_GPU_RENDERER.md §13.13), so the two fields a reader
// sees — the position and whether it is still playing — are atomic; the rest
// is touched by the stepping side alone.
type TexturePlayer struct {
	frames      []content.AssetID
	durations   []uint32
	loop        bool
	index       atomic.Int32
	remaining   uint32
	initialized bool
	active      atomic.Bool
}

// NewTexturePlayer copies one authored sequence into one embedded cursor.
func NewTexturePlayer(sequence content.AssetSequence) *TexturePlayer {
	p := &TexturePlayer{
		frames:    append([]content.AssetID(nil), sequence.Frames...),
		durations: append([]uint32(nil), sequence.Durations...),
		loop:      sequence.Loop,
	}
	if len(p.frames) != 0 {
		p.remaining = p.duration(0)
		p.initialized = true
		p.active.Store(true)
	}
	return p
}
func (p *TexturePlayer) duration(index int) uint32 {
	if p == nil || index < 0 || index >= len(p.frames) {
		return 0
	}
	if index >= len(p.durations) {
		// Missing duration is not a valid authored sequence. Keep the frame
		// visible indefinitely instead of manufacturing timing.
		return ^uint32(0)
	}
	return p.durations[index]
}

// Frame returns the current immutable asset identity. A missing sequence is a
// normal unresolved-art result.
func (p *TexturePlayer) Frame() (content.AssetID, bool) {
	index, ok := p.FrameIndex()
	if !ok {
		return "", false
	}
	return p.frames[index], true
}

// FrameIndex returns the current sequence position without advancing playback.
// Resolved-frame adapters can index their parallel immutable storage directly
// instead of reconstructing asset identities [03 R-CRD-005 §1].
func (p *TexturePlayer) FrameIndex() (int, bool) {
	if p == nil || !p.active.Load() {
		return 0, false
	}
	index := int(p.index.Load())
	if index < 0 || index >= len(p.frames) {
		return 0, false
	}
	return index, true
}

// Step advances one simulation tick. It is intentionally separate from
// presentation frame rendering: callers invoke it only when Clock.NewSimTick
// is true, never once per draw pass [03 §4.4].
func (p *TexturePlayer) Step() {
	if p == nil || !p.active.Load() || len(p.frames) <= 1 || !p.initialized {
		return
	}
	if p.remaining > 1 {
		p.remaining--
		return
	}
	index := p.index.Load() + 1
	if int(index) >= len(p.frames) {
		if !p.loop {
			p.index.Store(index)
			p.active.Store(false)
			return
		}
		index = 0
	}
	p.index.Store(index)
	p.remaining = p.duration(int(index))
}

// Advance advances exactly once when the caller consumed a new simulation
// tick. Passing false is a no-op, allowing every model instance to observe the
// same committed-tick boundary without consuming it independently.
func (p *TexturePlayer) Advance(newSimTick bool) {
	if newSimTick {
		p.Step()
	}
}

// Reset returns the cursor to its authored first frame and duration.
func (p *TexturePlayer) Reset() {
	if p == nil {
		return
	}
	p.index.Store(0)
	p.initialized = len(p.frames) != 0
	p.active.Store(p.initialized)
	if p.initialized {
		p.remaining = p.duration(0)
	}
}
