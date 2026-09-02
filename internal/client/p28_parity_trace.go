package client

// RendererTraceSink is an opt-in presentation diagnostic callback. The
// renderer calls it only after a model subject has finished its indexed
// composition, so ActualWinner and FinalPalette describe the completed
// subject pixel rather than the first candidate that happened to pass the
// height test [03 §2.4.1][03 §5.2].
//
// The callback is deliberately local to client. The parity bundle can adapt
// these value records without coupling the production renderer to a test
// recorder package.
type RendererTraceSink func(RendererCandidate)

// RendererTraceFilter is applied to completed value records, after all
// candidates for the subject have been resolved. This keeps selection generic
// (unit/piece/texture predicates belong to the fixture) without changing
// raster traversal for excluded records.
type RendererTraceFilter func(RendererCandidate) bool

// RendererWriter identifies a writer that is actually exposed by the client
// composition path. Unknown is used when a candidate had no visible final
// writer; no provenance is inferred for paths not instrumented here.
type RendererWriter uint8

const (
	RendererWriterUnknown RendererWriter = iota
	RendererWriterModel
	RendererWriterNanoframeOutline
)

// RendererTraceScope says which composition boundary the final fields cover.
// The client emits subject-composition evidence only; later world, fog,
// selection, and HUD passes are outside this trace and are not attributed.
type RendererTraceScope uint8

const (
	RendererScopeSubjectComposition RendererTraceScope = iota + 1
)

// RendererWinnerReason is a typed explanation of the candidate/final-writer
// relationship. It is intentionally separate from the writer identity.
type RendererWinnerReason uint8

const (
	RendererReasonUnknown RendererWinnerReason = iota
	RendererReasonModelSubjectWinner
	RendererReasonLaterAdmittedCandidate
	RendererReasonEqualHeightLaterCandidate
	RendererReasonOutlineOverwrite
	RendererReasonOutlineOnly
	RendererReasonNanoframeErase
	RendererReasonHeightRejected
	// RendererReasonOutsideTexture is a sample whose interpolated texel
	// coordinates fell outside the texture's own pixels. It is NOT a
	// transparency rejection: no span writer tests the texel against a
	// transparent or colour-key index [R-REN-03A §5].
	RendererReasonOutsideTexture
	RendererReasonDisplacedByLaterCandidate
	RendererReasonDisplacedByEqualHeightCandidate
)

// RendererUnavailableReason identifies why final subject provenance is not
// available. The outside-composition values document the scope boundary and
// prevent a subject trace from claiming full-frame attribution.
type RendererUnavailableReason uint8

const (
	RendererUnavailableNone RendererUnavailableReason = iota
	RendererUnavailableSubjectBackground
	RendererUnavailableNanoframeErase
	RendererUnavailableLaterWorld
	RendererUnavailableFog
	RendererUnavailableSelection
	RendererUnavailableHUD
	// RendererUnavailableTraceOverflow means that the diagnostic capture cap
	// was reached for this pixel. The indexed raster result remains authoritative,
	// but the incomplete trace must not claim a final writer.
	RendererUnavailableTraceOverflow
)

// RendererOutsideScope records later writers that this subject trace does not
// inspect. It is a mask because any of these passes may run after the model;
// the trace must not select one without evidence.
type RendererOutsideScope uint8

const (
	RendererOutsideLaterWorld RendererOutsideScope = 1 << iota
	RendererOutsideProjectile
	RendererOutsideEffect
	RendererOutsideFog
	RendererOutsideSelection
	RendererOutsideInterface
	RendererOutsideCursor
	RendererOutsideAll = RendererOutsideLaterWorld | RendererOutsideProjectile | RendererOutsideEffect | RendererOutsideFog | RendererOutsideSelection | RendererOutsideInterface | RendererOutsideCursor
	// RendererOutsideHUD retains the old name for callers that classify the
	// interface pass as HUD; it is the same documented writer bit.
	RendererOutsideHUD = RendererOutsideInterface
)

// RendererValueState makes unavailable source provenance explicit instead of
// using an invented frame, texel, or texture name.
type RendererValueState uint8

const (
	RendererValueUnavailable RendererValueState = iota
	RendererValueAvailable
)

// RendererTextureSample describes the resolved source for one model
// candidate. Frame and texel fields are valid only when their corresponding
// state is RendererValueAvailable. A transparent texel remains source
// evidence while Admitted is false.
type RendererTextureSample struct {
	Name        string
	Resolved    RendererValueState
	Frame       int
	FrameState  RendererValueState
	TexelX      int
	TexelY      int
	Texel       uint8
	TexelState  RendererValueState
	Transparent bool
}

// RendererCandidate is one indexed model candidate at one screen pixel.
// Unit is the selected committed-frame identity supplied to drawModel; it is
// never read from a live simulation object [03 §2.4][03 §5.2][I6].
type RendererCandidate struct {
	Tick              uint32
	Scope             RendererTraceScope
	OutsideScope      RendererOutsideScope
	Unit              uint64
	Piece             int
	Primitive         int
	CandidateOrder    uint32
	X, Y              int32
	IncomingHeight    uint8
	StoredHeight      uint8
	Admitted          bool
	ActualWinner      bool
	CandidateIndex    uint8
	FinalPalette      uint8
	FinalPaletteState RendererValueState
	FinalUnavailable  RendererUnavailableReason
	ShadeRow          int16
	Writer            RendererWriter
	FinalWriter       RendererWriter
	Reason            RendererWinnerReason
	RejectReason      string
	Texture           RendererTextureSample
}

// rendererTraceEventCap bounds diagnostic retention. It is an observability
// resource limit, not a rendering or retail-behavior constant: reaching it
// never changes the target planes or indexed output.
const rendererTraceEventCap = 4096

// RendererTraceStats reports bounded diagnostic capture. Dropped candidates
// are reported explicitly so an incomplete trace cannot be mistaken for a
// complete winner trace.
type RendererTraceStats struct {
	Capacity   int
	Captured   int
	Dropped    uint64
	Overflowed bool
}

// rendererTrace stores candidate events while one subject is rasterized.
// Allocation is performed only when a sink is installed; nil-sink rendering
// keeps the existing target planes and loops unchanged [I6]. The event cap is
// diagnostic-only, and incomplete pixels are never given invented provenance.
type rendererTrace struct {
	events      []RendererCandidate
	predecessor []int
	winner      []int
	incomplete  []bool
	dropped     uint64
	overflowed  bool
	unit        uint64
	tick        uint32
	width       int
	height      int
}

func rendererID(ids []uint64) uint64 {
	if len(ids) != 0 {
		return ids[0]
	}
	return 0
}

func (t *modelTarget) traceCandidate(v RendererCandidate) int {
	if t == nil || t.trace == nil {
		return -1
	}
	return t.trace.add(v)
}

func (t *modelTarget) traceAdmitted(pixel, event int) {
	if t == nil || t.trace == nil {
		return
	}
	t.trace.admitted(pixel, event)
}

func (t *modelTarget) traceRejected(event int, reason RendererWinnerReason, detail string) {
	if t == nil || t.trace == nil || event < 0 || event >= len(t.trace.events) {
		return
	}
	t.trace.events[event].Reason = reason
	t.trace.events[event].RejectReason = detail
}

func rendererTexture(t *faceIdentity, texelX, texelY int, texel uint8, state RendererValueState, transparent bool) RendererTextureSample {
	if t == nil {
		return RendererTextureSample{}
	}
	s := RendererTextureSample{Name: t.texture, Resolved: RendererValueUnavailable, FrameState: RendererValueUnavailable, TexelState: RendererValueUnavailable, Transparent: transparent}
	if t.frame != nil {
		s.Resolved = RendererValueAvailable
		s.Frame = t.frameIndex
		s.FrameState = t.frameState
	}
	if state == RendererValueAvailable {
		s.TexelX, s.TexelY, s.Texel = texelX, texelY, texel
		s.TexelState = RendererValueAvailable
	}
	return s
}

func rendererCandidate(t *faceIdentity, tick uint32, id uint64, px, py int32, incoming, stored, color uint8, shade int, sample RendererTextureSample) RendererCandidate {
	v := RendererCandidate{
		Tick: tick, Scope: RendererScopeSubjectComposition, OutsideScope: RendererOutsideAll,
		Unit: id, Piece: t.piece, Primitive: t.primitive, CandidateOrder: t.candidate,
		X: px, Y: py, IncomingHeight: incoming, StoredHeight: stored,
		CandidateIndex: color, ShadeRow: int16(shade), Writer: RendererWriterModel,
		Texture: sample,
	}
	return v
}

func newRendererTrace(pixels int) *rendererTrace {
	if pixels < 0 {
		pixels = 0
	}
	r := &rendererTrace{
		events:      make([]RendererCandidate, 0, rendererTraceEventCap),
		predecessor: make([]int, 0, rendererTraceEventCap),
		winner:      make([]int, pixels),
		incomplete:  make([]bool, pixels),
	}
	r.reset()
	return r
}

// reset starts a fresh subject capture while retaining bounded backing
// storage. The renderer creates one trace per subject today; the explicit
// lifecycle method also makes reuse safe for fixture and test callers.
func (r *rendererTrace) reset() {
	if r == nil {
		return
	}
	r.events = r.events[:0]
	r.predecessor = r.predecessor[:0]
	for i := range r.winner {
		r.winner[i] = -1
	}
	for i := range r.incomplete {
		r.incomplete[i] = false
	}
	r.dropped = 0
	r.overflowed = false
	r.unit = 0
	r.tick = 0
	r.width = 0
	r.height = 0
}

func (r *rendererTrace) stats() RendererTraceStats {
	if r == nil {
		return RendererTraceStats{}
	}
	return RendererTraceStats{Capacity: rendererTraceEventCap, Captured: len(r.events), Dropped: r.dropped, Overflowed: r.overflowed}
}

func (r *rendererTrace) pixelIndex(x, y int32) int {
	if r == nil || r.width <= 0 || x < 0 || y < 0 || x >= int32(r.width) || y >= int32(r.height) {
		return -1
	}
	i := int(y)*r.width + int(x)
	if i < 0 || i >= len(r.incomplete) {
		return -1
	}
	return i
}

func (r *rendererTrace) recordDrop(x, y int32) {
	if r == nil {
		return
	}
	r.dropped++
	r.overflowed = true
	if pixel := r.pixelIndex(x, y); pixel >= 0 {
		r.incomplete[pixel] = true
	}
}

func (r *rendererTrace) add(v RendererCandidate) int {
	if r == nil {
		return -1
	}
	if len(r.events) >= rendererTraceEventCap {
		r.recordDrop(v.X, v.Y)
		return -1
	}
	i := len(r.events)
	r.events = append(r.events, v)
	r.predecessor = append(r.predecessor, -1)
	return i
}

func (r *rendererTrace) admitted(pixel, event int) {
	if r == nil || pixel < 0 || pixel >= len(r.winner) {
		return
	}
	if event < 0 {
		if r.overflowed {
			r.incomplete[pixel] = true
		}
		return
	}
	if event >= len(r.events) {
		return
	}
	previous := r.winner[pixel]
	if previous >= 0 && previous < len(r.events) {
		r.predecessor[event] = previous
		classifyDisplacement(&r.events[previous], &r.events[event])
	}
	r.winner[pixel] = event
	r.events[event].Admitted = true
}

func classifyDisplacement(loser, replacer *RendererCandidate) {
	if loser == nil || replacer == nil || loser.Reason != RendererReasonUnknown {
		return
	}
	if replacer.IncomingHeight == loser.IncomingHeight {
		loser.Reason = RendererReasonDisplacedByEqualHeightCandidate
	} else {
		loser.Reason = RendererReasonDisplacedByLaterCandidate
	}
}

// composite records a nanoframe outline write without pretending it was a
// depth-tested model face. The outline pass writes into the composition image
// after the body, under the same key admission [R-COMP-01 §3]; the trace keeps
// it as its own writer so an outline pixel is never mistaken for a face.
func (r *rendererTrace) composite(x, y int32, color uint8, piece, primitive int) {
	if r == nil {
		return
	}
	if r.width > 0 && r.height > 0 && (x < 0 || x >= int32(r.width) || y < 0 || y >= int32(r.height)) {
		return
	}
	event := r.add(RendererCandidate{
		Tick: r.tick, Scope: RendererScopeSubjectComposition, OutsideScope: RendererOutsideAll, Unit: r.unit,
		Piece: piece, Primitive: primitive, CandidateOrder: uint32(primitive),
		X: x, Y: y, Admitted: true, ActualWinner: true, CandidateIndex: color,
		FinalPalette: color, FinalPaletteState: RendererValueAvailable,
		Writer: RendererWriterNanoframeOutline, FinalWriter: RendererWriterNanoframeOutline,
		Reason: RendererReasonOutlineOnly,
	})
	if event < 0 {
		return
	}
	for i := 0; i < event; i++ {
		e := &r.events[i]
		if e.X == x && e.Y == y {
			e.ActualWinner = false
			e.FinalWriter = RendererWriterNanoframeOutline
			e.Reason = RendererReasonOutlineOverwrite
			e.FinalPalette = color
			e.FinalPaletteState = RendererValueAvailable
		}
	}
}

func (r *rendererTrace) resolve(t *modelTarget, indexed []uint8, width, height int) {
	if r == nil || t == nil {
		return
	}
	for pixel, event := range r.winner {
		if pixel < len(r.incomplete) && r.incomplete[pixel] {
			r.invalidatePixel(pixel)
			continue
		}
		if event < 0 || event >= len(r.events) {
			continue
		}
		e := &r.events[event]
		if e.FinalWriter == RendererWriterNanoframeOutline {
			// The outline is a later compositor, but body candidates that
			// preceded its overwrite still have a proven displacement relation.
			r.markDisplaced(event)
			continue
		}
		if t.covered[pixel] {
			e.ActualWinner = true
			e.FinalWriter = RendererWriterModel
			e.FinalUnavailable = RendererUnavailableNone
			e.Reason = r.modelReason(event)
			r.markDisplaced(event)
			if pixel >= 0 && pixel < len(indexed) && width > 0 && height > 0 {
				e.FinalPalette = indexed[pixel]
				e.FinalPaletteState = RendererValueAvailable
			}
		} else {
			e.ActualWinner = false
			e.FinalWriter = RendererWriterUnknown
			e.FinalPaletteState = RendererValueUnavailable
			e.FinalUnavailable = RendererUnavailableNanoframeErase
			if e.Reason != RendererReasonNanoframeErase {
				e.FinalUnavailable = RendererUnavailableSubjectBackground
			}
		}
	}
	for i := range r.events {
		e := &r.events[i]
		if pixel := r.pixelIndex(e.X, e.Y); pixel >= 0 && r.incomplete[pixel] {
			continue
		}
		if e.FinalWriter == RendererWriterUnknown && e.X >= 0 && e.X < int32(width) && e.Y >= 0 && e.Y < int32(height) {
			pixel := int(e.Y)*width + int(e.X)
			if pixel >= 0 && pixel < len(r.winner) && r.winner[pixel] >= 0 && t.covered[pixel] {
				e.FinalWriter = RendererWriterModel
				e.FinalUnavailable = RendererUnavailableNone
			} else if e.FinalUnavailable == RendererUnavailableNone {
				e.FinalUnavailable = RendererUnavailableSubjectBackground
			}
		}
		if e.X >= 0 && e.X < int32(width) && e.Y >= 0 && e.Y < int32(height) {
			if e.FinalPaletteState != RendererValueUnavailable || e.FinalUnavailable == RendererUnavailableNone {
				e.FinalPalette = indexed[int(e.Y)*width+int(e.X)]
				e.FinalPaletteState = RendererValueAvailable
			}
		}
	}
}

func (r *rendererTrace) invalidatePixel(pixel int) {
	if r == nil || pixel < 0 || pixel >= len(r.incomplete) {
		return
	}
	width := r.width
	if width <= 0 {
		return
	}
	x, y := int32(pixel%width), int32(pixel/width)
	for i := range r.events {
		e := &r.events[i]
		if e.X != x || e.Y != y {
			continue
		}
		e.ActualWinner = false
		e.FinalWriter = RendererWriterUnknown
		e.FinalPaletteState = RendererValueUnavailable
		e.FinalUnavailable = RendererUnavailableTraceOverflow
	}
}

func (r *rendererTrace) modelReason(event int) RendererWinnerReason {
	if r == nil || event < 0 || event >= len(r.events) {
		return RendererReasonUnknown
	}
	previous := -1
	if event < len(r.predecessor) {
		previous = r.predecessor[event]
	}
	if previous < 0 {
		return RendererReasonModelSubjectWinner
	}
	if r.events[previous].IncomingHeight == r.events[event].IncomingHeight {
		return RendererReasonEqualHeightLaterCandidate
	}
	return RendererReasonLaterAdmittedCandidate
}

// markDisplaced classifies each admitted loser against the next admitted
// candidate that replaced it at this pixel. A final-winner-only comparison
// loses the intermediate boundary in a chain such as 5 -> 7 -> 7: the first
// candidate was displaced by a strict increase, while the second was
// displaced by an equal-height later candidate.
func (r *rendererTrace) markDisplaced(winner int) {
	if r == nil || winner < 0 || winner >= len(r.events) {
		return
	}
	for winner >= 0 && winner < len(r.predecessor) {
		previous := r.predecessor[winner]
		if previous < 0 || previous >= len(r.events) {
			return
		}
		classifyDisplacement(&r.events[previous], &r.events[winner])
		winner = previous
	}
}

func (r *rendererTrace) emit(sink RendererTraceSink, filter RendererTraceFilter) {
	if r == nil || sink == nil {
		return
	}
	for i := range r.events {
		if filter == nil || filter(r.events[i]) {
			sink(r.events[i])
		}
	}
}

// SetRendererTraceSink enables renderer candidate/final-winner evidence. A
// nil sink restores the zero-overhead production path.
func (c *Client) SetRendererTraceSink(sink RendererTraceSink) {
	if c != nil {
		c.rendererTraceSink = sink
	}
}

// SetRendererTraceFilter narrows emitted evidence without affecting indexed
// composition or the winner calculation. Nil removes the filter.
func (c *Client) SetRendererTraceFilter(filter RendererTraceFilter) {
	if c != nil {
		c.rendererTraceFilter = filter
	}
}

// SetParityRendererSink is a descriptive alias for fixture callers.
func (c *Client) SetParityRendererSink(sink RendererTraceSink) {
	c.SetRendererTraceSink(sink)
}
