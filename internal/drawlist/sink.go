package drawlist

// Sink is the executor interface for a recorded List: one method per command
// family (docs/DESIGN_GPU_RENDERER.md §2.1). List.Replay calls these methods in
// exact record order — the same order the family commands were recorded in
// across families (C-G3, I1). The classic executor implements Sink as the
// existing software blitters; the modern executor implements it as Ebitengine
// draws.
//
// Order semantics: an implementation must treat the call sequence as the paint
// order. It may merge consecutive same-family calls into one device draw only
// when no merged command's pixels depend on another merged command's result —
// opaque keyed sprites merge freely, destination-reading families merge only
// while their rectangles stay pairwise disjoint (C-G3, docs §2.3). It must not
// reorder across families or cull beyond what the recorded walk already did.
type Sink interface {
	// Terrain replays one terrain blit.
	Terrain(Terrain)
	// Sprite replays one GAF-frame blit.
	Sprite(Sprite)
	// Glyphs replays one FNT text run.
	Glyphs(Glyphs)
	// Fill replays one indexed rectangle.
	Fill(Fill)
	// Line replays one indexed line.
	Line(Line)
	// Points replays one batch of single-pixel writes.
	Points(Points)
	// Model replays one composed model subject.
	Model(Model)
	// Fog replays one clipped fog op list.
	Fog(Fog)
	// Surface replays one indexed byte surface blit.
	Surface(Surface)
	// Cursor replays the software cursor blit.
	Cursor(Cursor)
	// Expand performs the single index-to-RGBA expansion pass (C-G8).
	Expand()
}
