package drawlist

import "github.com/nanolathe-gg/nanolathe/internal/camera"

// The world-space boundary and the strategic marker layer
// (docs/DESIGN_GPU_RENDERER.md §16.3, §16.11).
//
// A recorded list is a flat sequence of screen-space commands, and until §16
// nothing in it said which commands were the world and which were the chrome
// painted over it. The modern executor's free zoom needs that distinction: it
// scales the recorded world by the live factor over the record step and leaves
// the interface alone. WorldSpace is that marker.
//
// It is recorded through an OPTIONAL sink interface, exactly as the trail
// family is, so every existing Sink implementation — the classic executor and
// the fixtures — keeps compiling and keeps drawing what it drew. An executor
// that cannot scale simply never sees the marker, which is right: the classic
// executor has no free zoom, so its factor always equals its step and the
// transform it would apply is the identity anyway.

// WorldSpace opens or closes the world-space region of one frame's recording
// (docs/DESIGN_GPU_RENDERER.md §16.3).
//
// Zoom and Step are the live factor and the record step the region was recorded
// under. The executor scales every command between the open and the close by
// Zoom/Step about the surface origin — the point the camera origin is drawn at,
// which is the framebuffer's own top-left, because the recorder projects the
// world from there and the chrome is painted over it afterwards. At
// Zoom == ZoomOf(Step) the scale is one and the region reaches pixels
// untouched, which is what keeps the §6 parity gate exact at 1x and 2x.
type WorldSpace struct {
	// Begin opens the region; a false Begin closes it.
	Begin bool
	// Zoom is the live presentation factor (camera.Zoom, 1/1024 units).
	Zoom camera.Zoom
	// Step is the record step the world commands were projected at.
	Step camera.ViewScale
	// Viewport is the battle viewport in framebuffer pixels — the rectangle the
	// chrome leaves for the world [03 §4.1]. It is carried for an executor that
	// wants to bound the region; the transform itself is about the surface
	// origin, not about this rectangle.
	Viewport Rect
	// RecordW, RecordH are the record-space extent the world was clipped to:
	// the framebuffer measured in record pixels, which is larger than the
	// framebuffer whenever the live factor is below the step (§16.3).
	RecordW, RecordH int32
}

// Identity reports whether the region needs no transform at all, which is the
// case at every rest step and therefore in the whole classic executor.
func (w WorldSpace) Identity() bool {
	return w.Zoom <= 0 || w.Zoom == camera.ZoomOf(w.Step)
}

// MarkerAtlas holds immutable generated icon coverage (GPU design §18).
// Each pixel has four independent disjoint coverage weights: red = team ink,
// green = white role ink, blue = selected halo, alpha = dark backing.
// Their sum is at most 255. This is data, not a premultiplied color image.
// Lists and their clones retain the atlas for their whole lifetime.
type MarkerAtlas struct {
	Width, Height int
	Pixels        []byte
}

// Marker records one strategic-view unit marker: a fixed-size filled square at
// a screen position, in the team palette entry the minimap's own dot uses
// (docs/DESIGN_GPU_RENDERER.md §16.11).
//
// Markers replace models below the strategic threshold. They are recorded
// OUTSIDE the world region, already positioned through the live factor, because
// their size is a fixed number of SCREEN pixels: scaling them with the world
// would defeat the point of drawing them at all.
type Marker struct {
	// IconAtlas and IconRect select immutable generated mask art. A nil atlas
	// preserves the generic square contact. Size remains the screen extent.
	IconAtlas *MarkerAtlas
	IconRect  Rect

	// X, Y is the marker's centre in framebuffer pixels.
	X, Y int32
	// Size is the square's side in framebuffer pixels.
	Size int32
	// Index is the physical palette byte the square is filled with [C-G2].
	Index uint8
	// Outline is the physical palette byte of the one-pixel selection outline;
	// it is drawn only when Selected is set.
	Outline  uint8
	Selected bool
	// Alpha is the layer's fade, 0..255. The layer fades in as the factor falls
	// from the marker threshold to the model cut, so markers do not appear
	// abruptly under the units they replace (§16.11).
	Alpha uint8
	// Clip is the battle viewport the markers are confined to; markers outside
	// it are not recorded at all, so an executor may treat this as advisory.
	Clip    Rect
	HasClip bool
}

// Markers records one batch of strategic markers in record order. The batch is
// a self-owned sub-slice of the client's reusable marker arena, immutable for
// the frame, exactly as a Points batch is.
type Markers struct {
	Marks []Marker
}

// WorldSink is the optional interface an executor implements to observe the
// world-space boundary. The classic executor does not: its factor always equals
// its step (§16.1).
type WorldSink interface {
	World(WorldSpace)
}

// MarkerSink is the optional interface an executor implements to draw the
// strategic marker layer. The classic executor does not: the strategic view is
// a modern-only feature, and classic never reaches a factor that produces one.
type MarkerSink interface {
	Markers(Markers)
}

// RecordWorld appends one world-space boundary marker in record order.
func (l *List) RecordWorld(c WorldSpace) {
	l.order = append(l.order, tag{familyWorld, len(l.world)})
	l.world = append(l.world, c)
}

// RecordMarkers appends one strategic marker batch in record order.
func (l *List) RecordMarkers(c Markers) {
	l.order = append(l.order, tag{familyMarkers, len(l.markers)})
	l.markers = append(l.markers, c)
}

// WorldSpaceAt returns the recorded world-space boundary markers in record
// order. It is a diagnostic and test accessor; executors see them through
// Replay.
func (l *List) WorldSpaces() []WorldSpace { return l.world }

// MarkerBatches returns the recorded marker batches in record order, for the
// same diagnostic and test use.
func (l *List) MarkerBatches() []Markers { return l.markers }
