package gpurender

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
)

// The phase scheduler (docs/DESIGN_GPU_RENDERER.md §11.2 "The scheduler" and
// §11.5 "Render passes, not draws"). The Sink methods no longer draw: each
// compiles its command's clipped screen rectangle, its class and its vertices
// into a phase, and Execute submits the phases in order once Replay returns.
// Order is preserved exactly where pixels depend on each other and relaxed
// everywhere else (C-G3).
//
// # Critical-path placement
//
// A command is placed in the EARLIEST phase its overlaps permit, not in the
// latest open one. With record order the only order, the phase of a command C
// is the maximum, over every earlier-recorded command E whose clipped rectangle
// overlaps C's, of
//
//   - E's phase plus one when E reads the destination, and
//   - E's phase when it does not,
//
// or zero when C overlaps nothing. That is exactly the dependency the older
// sequential rules expressed:
//
//   - a destination read must follow, by a whole phase, every earlier
//     destination read it covers, because the later one reads the earlier one's
//     result and a phase reads one state;
//   - a destination read must be preceded, in the same or an earlier phase, by
//     every earlier opaque write it covers, because a phase draws its opaque
//     batch before its destination batch;
//   - an opaque write must follow, by a whole phase, every earlier destination
//     read it covers, because it has to overwrite that result;
//   - an opaque write may share a phase with earlier opaque writes, because one
//     batch rasterizes in vertex order, which is record order.
//
// Each phase therefore keeps its own vertex and index storage and a command
// appends to the storage of the phase it was placed in, so a batch is still
// record-ordered inside itself. All the storage is pooled and reused, so a
// steady-state frame allocates nothing here (§11.2 "Allocation policy").
//
// # The cell grid
//
// Placement asks a coarse 32-pixel screen grid which earlier commands can reach
// a rectangle. A cell remembers, for the whole segment, up to schedCellOwners
// owners with each owner's rectangle, phase and class; the rectangles decide, so
// merely sharing a cell does not constrain anything. A cell that runs out of
// slots saturates and answers with the largest contribution it has ever seen,
// which is conservative — it can cost a phase that was not needed, never place a
// command too early.
//
// An opaque owner whose phase is zero is not recorded at all: its contribution
// is its phase, and zero is already the floor of every placement, so it can
// never raise an answer. That keeps the frame's terrain, background fills and
// first-phase sprites off the grid entirely.
//
// Two relaxations of the rectangle test are exact, not heuristic:
//
//   - A lit point tags the cell of its own pixel rather than the cells of its
//     batch's bounding rectangle, and two points conflict only when they write
//     the same pixel. The pixel set is kept as one screen-sized pixel→phase
//     table stamped with the segment serial, so a later point at a written pixel
//     is placed one phase after the point that wrote it. Any other command
//     sharing a cell with a point is still pushed past it, because a rectangle
//     test cannot enumerate the point set.
//
// That is the one relaxation of the rectangle test.
//
// # Barriers
//
// A barrier — the clear, a composed attached-unit group's shared staging image,
// a model subject the slot atlas could not fit, the Expand marker — ends a SEGMENT:
// everything compiled so far is submitted, and every later command is placed
// after it. Fog is no longer a barrier: it is an ordinary destination-reading
// command whose rectangle is the visible fog region, so the rectangle rules
// place it after everything it reads and before everything that overwrites it.
//
// Within a run, vertex order is record order, and the device rasterizes one
// draw's primitives in order, so overlapping opaque writes in the same batch
// still resolve to the later command (C-G3). A batch splits into consecutive
// runs — never reordered — when the next command needs a source image or a
// shader the run cannot bind, or when the run reaches its vertex limit.

const (
	// schedOpaque is the class of every command that overwrites its pixels;
	// schedDest is the class of the families whose result depends on what is
	// already there — the ALP composites, the row scales and the fog composite.
	// Since §13.3 those are device blends rather than destination reads, but the
	// order they impose is the same, so the placement rules are unchanged.
	schedOpaque  = 0
	schedDest    = 1
	schedClasses = 2
)

// schedGridShift is the 32-pixel screen grid of §11.2, as a shift; schedCellMask
// picks a pixel's offset inside its cell and schedPointCellShift is how far a
// cell's page in the lit point phase table is from the previous cell's.
const (
	schedGridShift      = 5
	schedCellMask       = 1<<schedGridShift - 1
	schedPointCellShift = 2 * schedGridShift
)

// schedCellOwners is how many commands one 32-pixel cell names exactly. A cell
// that runs out of slots forgets its OLDEST owner and folds that owner's
// contribution into a floor every later placement over the cell takes, so a
// crowded cell stays exact about the commands a new one is most likely to
// overlap and only ever over-constrains, never under-constrains. Forgetting the
// oldest rather than refusing the newest matters: the floor then follows the
// early, low-numbered phases instead of ratcheting up with every command that
// lands on the cell.
const schedCellOwners = 4

// schedRunVertexLimit bounds one device draw's vertex slice. Runs submit through
// DrawTrianglesShader32 with uint32 indices, so the old 65,536-vertex ceiling —
// which existed only because a uint16 index would silently wrap onto earlier
// geometry — no longer applies; this limit exists only so one draw's vertex
// buffer stays a bounded size, and no frame of the battle benchmark approaches
// it. Splitting never reorders geometry (C-G3).
const schedRunVertexLimit = 1 << 20

// schedReadNone marks a run that binds no read surface. Since §13.3 that is
// every run but the fog composite's: the ALP and row families are device blends
// over the composite and read nothing.
const schedReadNone = int8(-1)

// schedOwner is one placed command's clipped screen rectangle together with the
// phase it landed in and whether it read the destination. contribution is the
// smallest phase a later overlapping command may take.
type schedOwner struct {
	x0, y0, x1, y1 int32
	phase          int32
	dest           bool
}

func (o *schedOwner) overlaps(x0, y0, x1, y1 int) bool {
	return int32(x0) < o.x1 && int32(x1) > o.x0 && int32(y0) < o.y1 && int32(y1) > o.y0
}

func (o *schedOwner) contribution() int32 {
	if o.dest {
		return o.phase + 1
	}
	return o.phase
}

// schedRun is one device draw: a contiguous slice of its batch's vertex and
// index scratch, the four source images it binds, the shader it draws with (nil
// for the class's own shader) and the blend it draws under. imgs entries left nil
// are filled with a placeholder at submission, so a shader never samples an
// unbound slot; readSlot names the one slot the read copy is bound into instead.
//
// The blend is part of the run key (§13.3): the ALP families and the row
// families composite differently, so two commands with different blends never
// share a device draw even when their images and shader agree.
type schedRun struct {
	imgs       [4]*ebiten.Image
	shader     *ebiten.Shader
	blend      ebiten.Blend
	readSlot   int8
	vOff, vLen int32
	iOff, iLen int32
}

// schedBatch is one class's work inside one phase: its runs in record order and
// the vertex and index storage they slice. Indices are relative to their own
// run's first vertex.
type schedBatch struct {
	runs  []schedRun
	verts []ebiten.Vertex
	idx   []uint32
	// vHint and iHint are what this batch held when it was last reset. A batch's
	// load is similar from one frame to the next even though the storage itself is
	// pooled, so asking the pool for that size at the first growth skips the
	// doublings — and their copies — that walking up from the smallest class would
	// cost (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
	vHint, iHint int32
}

// reset returns the batch's storage to the scheduler's pool, keeping what it
// held as the next segment's sizing hint. The runs slice stays with the batch: a
// batch holds a couple of runs, so it converges at once.
func (b *schedBatch) reset(s *scheduler) {
	b.runs = b.runs[:0]
	b.vHint, b.iHint = int32(len(b.verts)), int32(len(b.idx))
	s.putVerts(b.verts)
	s.putIdx(b.idx)
	b.verts, b.idx = nil, nil
}

func (b *schedBatch) empty() bool { return len(b.idx) == 0 }

// Pooled batch storage. Per-phase storage is what keeps a phase's commands one
// device run, but which phase carries a frame's heavy batch moves from frame to
// frame — an explosion's lit point batches land wherever their overlaps put
// them — so storage owned by a phase index never converged: a batch that had
// only ever held a few hundred vertices reallocated a multi-megabyte buffer as
// soon as the load moved onto it, and that regrowth was the whole of the
// executor's own steady-state allocation
// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy", §11.2 "Allocation
// policy"). Buffers are therefore sized in powers of two and returned to the
// scheduler when a segment ends, so the next batch that needs that size takes
// the buffer instead of allocating one.
const (
	// schedStoreMinLog is the smallest pooled buffer: 256 elements, which is what
	// the many small batches of a frame need and never exceed.
	schedStoreMinLog = 8
	// schedStoreLogs bounds the size classes. The largest is far above
	// schedRunVertexLimit, so every request has a class.
	schedStoreLogs = 25
)

// storeClass is the size class whose capacity holds n elements.
func storeClass(n int) int {
	c := schedStoreMinLog
	for c < schedStoreLogs-1 && 1<<c < n {
		c++
	}
	return c
}

func (s *scheduler) takeVerts(n int) []ebiten.Vertex {
	c := storeClass(n)
	if p := s.vertPool[c]; len(p) > 0 {
		v := p[len(p)-1]
		s.vertPool[c] = p[:len(p)-1]
		return v[:0]
	}
	return make([]ebiten.Vertex, 0, 1<<c)
}

func (s *scheduler) putVerts(v []ebiten.Vertex) {
	if cap(v) == 0 {
		return
	}
	c := storeClass(cap(v))
	s.vertPool[c] = append(s.vertPool[c], v)
}

func (s *scheduler) takeIdx(n int) []uint32 {
	c := storeClass(n)
	if p := s.idxPool[c]; len(p) > 0 {
		v := p[len(p)-1]
		s.idxPool[c] = p[:len(p)-1]
		return v[:0]
	}
	return make([]uint32, 0, 1<<c)
}

func (s *scheduler) putIdx(v []uint32) {
	if cap(v) == 0 {
		return
	}
	c := storeClass(cap(v))
	s.idxPool[c] = append(s.idxPool[c], v)
}

// growVerts moves a batch's vertices into a buffer that holds n of them and
// returns the old buffer to the pool.
func (s *scheduler) growVerts(v []ebiten.Vertex, n int) []ebiten.Vertex {
	next := s.takeVerts(n)[:len(v)]
	copy(next, v)
	s.putVerts(v)
	return next
}

func (s *scheduler) growIdx(v []uint32, n int) []uint32 {
	next := s.takeIdx(n)[:len(v)]
	copy(next, v)
	s.putIdx(v)
	return next
}

// schedPhase is one opaque batch and one destination-reading batch. x0..y1 is
// the union rectangle of the destination batch, the only region the next pass
// has to copy forward (§11.5).
type schedPhase struct {
	batch          [schedClasses]schedBatch
	x0, y0, x1, y1 int32
	hasDest        bool
}

func (p *schedPhase) reset(s *scheduler) {
	p.batch[schedOpaque].reset(s)
	p.batch[schedDest].reset(s)
	p.x0, p.y0, p.x1, p.y1 = 0, 0, 0, 0
	p.hasDest = false
}

// scheduler owns the compiled segment. Every slice is reused across frames and
// across segments, so a steady-state frame allocates nothing here
// (§11.2 "Allocation policy").
type scheduler struct {
	// phases is a pool: it keeps the high-water number of phases ever compiled
	// and nphase says how many of them this segment uses. Truncating it would
	// throw away each phase's retained vertex and index storage.
	phases []schedPhase
	nphase int

	// vertPool and idxPool hold the batch storage of every phase reset so far,
	// by size class.
	vertPool [schedStoreLogs][][]ebiten.Vertex
	idxPool  [schedStoreLogs][][]uint32

	// owners holds this segment's placed commands. A cell names them by index.
	owners []schedOwner

	// tags carries the segment serial that last touched each 32-pixel cell.
	// Serials only ever increase, so a stale tag from an earlier segment or an
	// earlier frame never matches. cells names up to schedCellOwners owners per
	// cell and cellHead the slot the next owner replaces, so the names in a full
	// cell are its most recent ones. cellFloor is the largest contribution among
	// the owners the cell has forgotten, and cellPoint the largest phase of a lit
	// point on it, or -1 for none.
	tags       []uint32
	cells      []int32
	cellCount  []uint8
	cellHead   []uint8
	cellFloor  []int32
	cellPoint  []int32
	cols, rows int
	serial     uint32

	// pointPhase records, per screen pixel, the phase of the last lit point that
	// wrote it, so the next point at that pixel is placed one phase later. The
	// high 32 bits are the segment serial and the low 32 the phase, so a pixel
	// written before the current segment reads as unwritten and nothing has to be
	// cleared at a barrier. It replaces the pixel→phase map the earlier executor
	// kept: a 1080p battle frame places tens of thousands of lit points — a flash
	// disc contributes one per covered pixel [03 R-FX-01 §4] — and the hash lookup
	// and insert were most of what placing one cost
	// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
	//
	// It is indexed cell-major — the cell's index, then the pixel's offset inside
	// the 32-pixel cell — so the whole of a cell's page is one contiguous 8 KB
	// block. A point batch walks a few cells, and scanline order inside one would
	// otherwise stride across the frame.
	pointPhase []uint64

	// ptCell caches the placement inputs of the 32-pixel cell the lit point batch
	// is currently walking. No lit point records an owner — tagging one only
	// touches its own pixel and its cell's point phase — so a cell's owner list
	// and floor cannot change while a batch is being placed, and about nine in ten
	// of a battle frame's points land in the cell the previous point did. The
	// cache is dropped whenever a rectangle command tags the grid and at every
	// segment boundary, which is the only way those inputs change.
	ptCellAt int
	ptFloor  int32
	ptOwnerN int
	ptOwners [schedCellOwners]schedOwner

	// ptRunPhase is the phase whose destination run the lit point batch is
	// already appending to, or -1. Selecting a run copies a command's four image
	// bindings and its blend and compares them against the batch's open run, and
	// every point of a batch carries the same ones. Any other command compiled
	// into the scheduler drops the cache, since it may open a run of its own.
	ptRunPhase int32

	// cur is where the command being compiled appends: the phase it was placed
	// in and its class. begin sets it; quad reads it.
	curPhase int32
	curClass int

	// world is the free-zoom transform of docs/DESIGN_GPU_RENDERER.md §16.3:
	// while the recording is inside its world region, every rectangle placed and
	// every vertex appended is scaled by the live factor over the record step,
	// about the surface origin. worldOn is false everywhere else — the chrome,
	// the strategic markers, and every frame of the classic executor and of a
	// modern one sitting on a rest step — and then nothing below costs more than
	// one predictable branch.
	//
	// The transform is a pure scale about (0,0) because the recorder projects
	// the world from the FRAMEBUFFER's own top-left: the camera origin is drawn
	// there and the chrome painted over it, so record x = step·(worldX − camX)
	// and screen x = factor·(worldX − camX) differ by exactly this factor. That
	// is also why it needs no translation term to keep the world under the
	// viewport corner fixed.
	worldOn    bool
	worldScale float32
}

// setWorld arms or disarms the world transform. num/den are the live factor and
// the record step's factor in the same units; num == den disarms, so a rest
// step compiles byte-identically to the build before §16.
func (s *scheduler) setWorld(num, den int32) {
	if num <= 0 || den <= 0 || num == den {
		s.worldOn, s.worldScale = false, 1
		return
	}
	s.worldOn, s.worldScale = true, float32(num)/float32(den)
}

// clearWorld closes the world region.
func (s *scheduler) clearWorld() { s.worldOn, s.worldScale = false, 1 }

// TODO(question): sources are sampled NEAREST at every factor, because the
// scene shader reads palette indices and an index cannot be interpolated
// (DESIGN_GPU_RENDERER §16.3 "Sampling"). Filtering after the palette resolve —
// four texels, each through PAL, blended in colour — would soften the aliasing
// of thin features at four times the lookups; whether that is worth the cost
// needs a measurement and a human look.

// txf maps one record coordinate to its screen coordinate.
func (s *scheduler) txf(v float32) float32 {
	if !s.worldOn {
		return v
	}
	return v * s.worldScale
}

// txRect maps a record-space integer rectangle to the screen pixels it covers:
// the origin floors and the far edge ceils, so the placed rectangle is a
// superset of the rasterized one and the overlap tests stay conservative.
func (s *scheduler) txRect(x0, y0, x1, y1 int) (int, int, int, int) {
	if !s.worldOn {
		return x0, y0, x1, y1
	}
	k := float64(s.worldScale)
	return int(math.Floor(float64(x0) * k)), int(math.Floor(float64(y0) * k)),
		int(math.Ceil(float64(x1) * k)), int(math.Ceil(float64(y1) * k))
}

// txPoint maps one record-space pixel to the screen pixel it lands in.
func (s *scheduler) txPoint(x, y int) (int, int) {
	if !s.worldOn {
		return x, y
	}
	k := float64(s.worldScale)
	return int(math.Floor(float64(x) * k)), int(math.Floor(float64(y) * k))
}

// resetFrame drops the compiled segment and re-sizes the cell grid. The backing
// arrays are retained.
func (s *scheduler) resetFrame(w, h int) {
	cols := (w + (1 << schedGridShift) - 1) >> schedGridShift
	rows := (h + (1 << schedGridShift) - 1) >> schedGridShift
	if cols < 0 {
		cols = 0
	}
	if rows < 0 {
		rows = 0
	}
	if cols != s.cols || rows != s.rows || len(s.tags) < cols*rows {
		s.cols, s.rows = cols, rows
		s.tags = make([]uint32, cols*rows)
		s.cells = make([]int32, cols*rows*schedCellOwners)
		s.cellCount = make([]uint8, cols*rows)
		s.cellHead = make([]uint8, cols*rows)
		s.cellFloor = make([]int32, cols*rows)
		s.cellPoint = make([]int32, cols*rows)
		s.serial = 0
	}
	if len(s.pointPhase) != cols*rows<<schedPointCellShift {
		s.pointPhase = make([]uint64, cols*rows<<schedPointCellShift)
		s.serial = 0
	}
	s.clearWorld()
	s.resetSegment()
}

// resetSegment drops the compiled phases and starts a fresh segment: a new grid
// serial, so no tag from before the barrier can be read after it.
func (s *scheduler) resetSegment() {
	for i := 0; i < s.nphase && i < len(s.phases); i++ {
		s.phases[i].reset(s)
	}
	s.nphase = 0
	s.owners = s.owners[:0]
	s.curPhase, s.curClass = -1, schedOpaque
	s.ptCellAt, s.ptRunPhase = -1, -1
	s.nextSerial()
}

// nextSerial advances the segment serial, clearing the grid and the lit point
// table on the (practically unreachable) wrap so a stale tag or point phase can
// never alias the new serial.
func (s *scheduler) nextSerial() {
	if s.serial == ^uint32(0) {
		for i := range s.tags {
			s.tags[i] = 0
		}
		clear(s.pointPhase)
		s.serial = 0
	}
	s.serial++
}

// phaseAt returns phase k of the current segment, opening the phases up to it.
// The pool keeps every phase's storage, so re-opening one costs nothing.
func (s *scheduler) phaseAt(k int32) *schedPhase {
	for s.nphase <= int(k) {
		if s.nphase == len(s.phases) {
			s.phases = append(s.phases, schedPhase{})
		} else {
			s.phases[s.nphase].reset(s)
		}
		s.nphase++
	}
	return &s.phases[k]
}

// cellRange converts a screen rectangle to inclusive cell bounds, clamped to the
// grid. It returns false for a rectangle with no cell.
func (s *scheduler) cellRange(x0, y0, x1, y1 int) (cx0, cy0, cx1, cy1 int, ok bool) {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > s.cols<<schedGridShift {
		x1 = s.cols << schedGridShift
	}
	if y1 > s.rows<<schedGridShift {
		y1 = s.rows << schedGridShift
	}
	if x0 >= x1 || y0 >= y1 || s.cols == 0 || s.rows == 0 {
		return 0, 0, 0, 0, false
	}
	return x0 >> schedGridShift, y0 >> schedGridShift, (x1 - 1) >> schedGridShift, (y1 - 1) >> schedGridShift, true
}

// place returns the earliest phase a command covering the rectangle may take:
// the largest contribution of every earlier command that can reach it. A lit
// point does not come through here — placePoint resolves its own pixel exactly
// against the point phase table instead of taking the cell's point term.
func (s *scheduler) place(x0, y0, x1, y1 int) int32 {
	cx0, cy0, cx1, cy1, ok := s.cellRange(x0, y0, x1, y1)
	if !ok {
		return 0
	}
	phase := int32(0)
	for cy := cy0; cy <= cy1; cy++ {
		row := cy * s.cols
		for cx := cx0; cx <= cx1; cx++ {
			at := row + cx
			if s.tags[at] != s.serial {
				continue
			}
			if f := s.cellFloor[at]; f > phase {
				phase = f
			}
			for k, n := 0, int(s.cellCount[at]); k < n; k++ {
				own := &s.owners[s.cells[at*schedCellOwners+k]]
				if !own.overlaps(x0, y0, x1, y1) {
					continue
				}
				if c := own.contribution(); c > phase {
					phase = c
				}
			}
			// A point owner covers single pixels a rectangle test cannot
			// enumerate, so anything but another point is pushed past it.
			if p := s.cellPoint[at]; p >= 0 && p+1 > phase {
				phase = p + 1
			}
		}
	}
	return phase
}

// placePoint is place for one pixel of a lit point batch: an earlier point
// conflicts only when it wrote this very pixel, while a rectangle command is
// tested exactly, as a one-pixel rectangle. It also records the point, which
// tagPoint did separately before: both halves read the same cell, and a battle
// frame runs them tens of thousands of times, so they share one cell lookup
// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
//
// The answer is exactly what place plus the point pixel table gave: the cell's
// floor, the largest contribution of an owner whose rectangle contains the
// pixel, and one phase past any earlier point of the segment at that pixel.
func (s *scheduler) placePoint(x, y int) int32 {
	cx, cy, _, _, ok := s.cellRange(x, y, x+1, y+1)
	if !ok {
		return 0
	}
	at := cy*s.cols + cx
	if at != s.ptCellAt {
		s.loadPointCell(at)
	}
	phase := s.ptFloor
	for k := 0; k < s.ptOwnerN; k++ {
		own := &s.ptOwners[k]
		if !own.overlaps(x, y, x+1, y+1) {
			continue
		}
		if c := own.contribution(); c > phase {
			phase = c
		}
	}
	pixel := at<<schedPointCellShift | (y&schedCellMask)<<schedGridShift | x&schedCellMask
	if v := s.pointPhase[pixel]; uint32(v>>32) == s.serial {
		if prev := int32(uint32(v)); prev+1 > phase {
			phase = prev + 1
		}
	}
	// The point's own record: its cell keeps the largest point phase it has seen,
	// for the rectangle commands that follow it, and its pixel the exact phase.
	if phase > s.cellPoint[at] {
		s.cellPoint[at] = phase
	}
	s.pointPhase[pixel] = uint64(s.serial)<<32 | uint64(uint32(phase))
	return phase
}

// loadPointCell brings one cell into the point batch's cache, resetting it first
// when its tag is stale exactly as an untouched cell would read.
func (s *scheduler) loadPointCell(at int) {
	s.touchCell(at)
	s.ptCellAt = at
	s.ptFloor = s.cellFloor[at]
	s.ptOwnerN = int(s.cellCount[at])
	for k := 0; k < s.ptOwnerN; k++ {
		s.ptOwners[k] = s.owners[s.cells[at*schedCellOwners+k]]
	}
}

// tag records a placed command over the rectangle and marks every cell it covers
// as owned by it. An opaque command placed in phase zero is not recorded: its
// contribution is zero, which is already every placement's floor.
func (s *scheduler) tag(x0, y0, x1, y1 int, phase int32, dest bool) {
	if !dest && phase == 0 {
		return
	}
	s.owners = append(s.owners, schedOwner{int32(x0), int32(y0), int32(x1), int32(y1), phase, dest})
	owner := int32(len(s.owners) - 1)
	// A new owner is the one thing that changes the inputs the lit point batch
	// caches, so the cached cell is dropped here.
	s.ptCellAt = -1
	contribution := s.owners[owner].contribution()
	cx0, cy0, cx1, cy1, ok := s.cellRange(x0, y0, x1, y1)
	if !ok {
		return
	}
	for cy := cy0; cy <= cy1; cy++ {
		row := cy * s.cols
		for cx := cx0; cx <= cx1; cx++ {
			s.addOwner(row+cx, owner, contribution)
		}
	}
}

// touchCell brings a cell into this segment, resetting it when its tag is stale.
func (s *scheduler) touchCell(at int) {
	if s.tags[at] == s.serial {
		return
	}
	s.tags[at] = s.serial
	s.cellCount[at] = 0
	s.cellHead[at] = 0
	s.cellFloor[at] = 0
	s.cellPoint[at] = -1
}

// addOwner records owner on one cell. A cell names up to schedCellOwners owners
// exactly; the next one replaces the oldest of them, whose contribution becomes a
// floor for every later placement over the cell.
func (s *scheduler) addOwner(at int, owner, contribution int32) {
	s.touchCell(at)
	n := s.cellCount[at]
	if int(n) < schedCellOwners {
		s.cells[at*schedCellOwners+int(n)] = owner
		s.cellCount[at] = n + 1
		return
	}
	head := int(s.cellHead[at])
	if c := s.owners[s.cells[at*schedCellOwners+head]].contribution(); c > s.cellFloor[at] {
		s.cellFloor[at] = c
	}
	s.cells[at*schedCellOwners+head] = owner
	s.cellHead[at] = uint8((head + 1) % schedCellOwners)
}

// bindable reports whether run can also serve a command needing req, i.e. every
// requested slot is either unbound or already bound to that same image.
func bindable(run *[4]*ebiten.Image, req *[4]*ebiten.Image) bool {
	for i := 0; i < 4; i++ {
		if req[i] != nil && run[i] != nil && run[i] != req[i] {
			return false
		}
	}
	return true
}

// bind fills a run's unbound slots from req. The caller has checked bindable.
func bind(run *[4]*ebiten.Image, req *[4]*ebiten.Image) {
	for i := 0; i < 4; i++ {
		if req[i] != nil {
			run[i] = req[i]
		}
	}
}

// selectRun keeps the batch's last run when it can serve this command's shader
// and sources, and opens the next run when it cannot. A batch is only ever
// appended to, so its last run is its open one.
func (b *schedBatch) selectRun(imgs [4]*ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, readSlot int8) {
	if n := len(b.runs); n > 0 {
		run := &b.runs[n-1]
		if run.shader == shader && run.blend == blend && run.readSlot == readSlot && bindable(&run.imgs, &imgs) {
			bind(&run.imgs, &imgs)
			return
		}
	}
	b.openRun(imgs, shader, blend, readSlot)
}

func (b *schedBatch) openRun(imgs [4]*ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, readSlot int8) {
	b.runs = append(b.runs, schedRun{
		imgs:     imgs,
		shader:   shader,
		blend:    blend,
		readSlot: readSlot,
		vOff:     int32(len(b.verts)),
		iOff:     int32(len(b.idx)),
	})
}

// begin places one command: it computes the command's phase, records it on the
// grid, extends the phase's destination rectangle for a destination-reading
// command, and selects the run its quads will land in. x0..y1 is the command's
// clipped screen rectangle. It returns false for an empty rectangle, which the
// caller treats as a command that covers nothing.
func (s *scheduler) begin(class int, x0, y0, x1, y1 int, imgs [4]*ebiten.Image) bool {
	blend := blendComposite
	if class == schedDest {
		blend = blendHalfSource
	}
	return s.beginBlended(class, x0, y0, x1, y1, imgs, nil, blend, schedReadNone)
}

// beginBlended is begin for a command that needs its own shader, blend or read
// copy: the row families' scale blend, and the fog composite, which is the one
// command still reading the pixels it rewrites and so binds the read copy in its
// own first slot (fogPassShaderSource).
func (s *scheduler) beginBlended(class int, x0, y0, x1, y1 int, imgs [4]*ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, readSlot int8) bool {
	if x0 >= x1 || y0 >= y1 {
		return false
	}
	// The rectangle reaches the grid in SCREEN pixels, so the overlap tests that
	// decide a command's phase compare what actually lands on the composite
	// (§16.3).
	x0, y0, x1, y1 = s.txRect(x0, y0, x1, y1)
	if x0 >= x1 || y0 >= y1 {
		return false
	}
	dest := class == schedDest
	phase := s.place(x0, y0, x1, y1)
	s.tag(x0, y0, x1, y1, phase, dest)
	p := s.phaseAt(phase)
	if dest {
		s.growDest(p, x0, y0, x1, y1)
	}
	p.batch[class].selectRun(imgs, shader, blend, readSlot)
	s.ptRunPhase = -1
	s.curPhase, s.curClass = phase, class
	return true
}

// beginPoint places one lit point of a destination-compositing batch. Its own
// pixel is what decides it, not its batch's bounding rectangle.
func (s *scheduler) beginPoint(x, y int, imgs [4]*ebiten.Image) {
	x, y = s.txPoint(x, y)
	phase := s.placePoint(x, y)
	p := s.phaseAt(phase)
	s.growDest(p, x, y, x+1, y+1)
	if phase != s.ptRunPhase {
		p.batch[schedDest].selectRun(imgs, nil, blendScaleDestination, schedReadNone)
		s.ptRunPhase = phase
	}
	s.curPhase, s.curClass = phase, schedDest
}

// growDest extends the phase's destination rectangle, the union of its
// destination batch and the only region the following pass has to copy forward.
func (s *scheduler) growDest(p *schedPhase, x0, y0, x1, y1 int) {
	if !p.hasDest {
		p.x0, p.y0, p.x1, p.y1 = int32(x0), int32(y0), int32(x1), int32(y1)
		p.hasDest = true
		return
	}
	p.x0, p.y0 = int32(minInt(int(p.x0), x0)), int32(minInt(int(p.y0), y0))
	p.x1, p.y1 = int32(maxInt(int(p.x1), x1)), int32(maxInt(int(p.y1), y1))
}

// quad appends one axis-aligned quad to the open run of the phase the last begin
// placed, splitting the run when it reaches its vertex limit. Splitting never
// reorders geometry: the new run is drawn immediately after this one (C-G3).
func (s *scheduler) quad(class int, dx0, dy0, dx1, dy1, sx0, sy0, sx1, sy1 float32, col, custom [4]float32) {
	if s.curPhase < 0 || class != s.curClass {
		return
	}
	b := &s.phases[s.curPhase].batch[class]
	if len(b.runs) == 0 {
		return
	}
	run := &b.runs[len(b.runs)-1]
	if int(run.vLen)+quadVertices > schedRunVertexLimit {
		imgs, shader, blend, readSlot := run.imgs, run.shader, run.blend, run.readSlot
		b.openRun(imgs, shader, blend, readSlot)
		run = &b.runs[len(b.runs)-1]
	}
	if s.worldOn {
		dx0, dy0, dx1, dy1 = s.txf(dx0), s.txf(dy0), s.txf(dx1), s.txf(dy1)
		// A world quad that shrinks below one screen pixel keeps one: the
		// one-pixel primitives the world is full of — the selection quad's
		// lines, a dotted path's dots, a lit point's row span — would otherwise
		// fall between two pixel centres and vanish at an arbitrary subset of
		// factors (§16.3). Nothing wider is touched, so the terrain's tiles and
		// every sprite still tile the plane exactly.
		if dx1-dx0 < 1 {
			dx1 = dx0 + 1
		}
		if dy1-dy0 < 1 {
			dy1 = dy0 + 1
		}
	}
	base := uint32(run.vLen)
	// The four vertices and the six indices are written into the batch's storage
	// rather than appended: an append of four struct literals builds them on the
	// stack and copies them in, and a battle frame compiles tens of thousands of
	// these quads (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
	nv := len(b.verts)
	if nv+quadVertices > cap(b.verts) {
		b.verts = s.growVerts(b.verts, maxInt(nv+quadVertices, int(b.vHint)))
	}
	b.verts = b.verts[:nv+quadVertices]
	v := b.verts[nv : nv+quadVertices : nv+quadVertices]
	v[0] = ebiten.Vertex{DstX: dx0, DstY: dy0, SrcX: sx0, SrcY: sy0,
		ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
		Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]}
	v[1] = ebiten.Vertex{DstX: dx1, DstY: dy0, SrcX: sx1, SrcY: sy0,
		ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
		Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]}
	v[2] = ebiten.Vertex{DstX: dx0, DstY: dy1, SrcX: sx0, SrcY: sy1,
		ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
		Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]}
	v[3] = ebiten.Vertex{DstX: dx1, DstY: dy1, SrcX: sx1, SrcY: sy1,
		ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
		Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]}
	ni := len(b.idx)
	if ni+6 > cap(b.idx) {
		b.idx = s.growIdx(b.idx, maxInt(ni+6, int(b.iHint)))
	}
	b.idx = b.idx[:ni+6]
	i := b.idx[ni : ni+6 : ni+6]
	i[0], i[1], i[2] = base, base+1, base+2
	i[3], i[4], i[5] = base+1, base+2, base+3
	run.vLen += quadVertices
	run.iLen += 6
}

// quadCorners appends one quad with explicit corner positions and per-vertex
// custom lanes to the open run, in the same vertex order as quad; the trail
// marks are rotated quads (§15). Source coordinates are zero: the run's
// shader op reads no texture.
func (s *scheduler) quadCorners(class int, xs, ys [4]float32, col [4]float32, custom [4][4]float32) {
	if s.curPhase < 0 || class != s.curClass {
		return
	}
	b := &s.phases[s.curPhase].batch[class]
	if len(b.runs) == 0 {
		return
	}
	run := &b.runs[len(b.runs)-1]
	if int(run.vLen)+quadVertices > schedRunVertexLimit {
		imgs, shader, blend, readSlot := run.imgs, run.shader, run.blend, run.readSlot
		b.openRun(imgs, shader, blend, readSlot)
		run = &b.runs[len(b.runs)-1]
	}
	base := uint32(run.vLen)
	nv := len(b.verts)
	if nv+quadVertices > cap(b.verts) {
		b.verts = s.growVerts(b.verts, maxInt(nv+quadVertices, int(b.vHint)))
	}
	b.verts = b.verts[:nv+quadVertices]
	v := b.verts[nv : nv+quadVertices : nv+quadVertices]
	for i := 0; i < quadVertices; i++ {
		v[i] = ebiten.Vertex{DstX: s.txf(xs[i]), DstY: s.txf(ys[i]),
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[i][0], Custom1: custom[i][1], Custom2: custom[i][2], Custom3: custom[i][3]}
	}
	ni := len(b.idx)
	if ni+6 > cap(b.idx) {
		b.idx = s.growIdx(b.idx, maxInt(ni+6, int(b.iHint)))
	}
	b.idx = b.idx[:ni+6]
	i := b.idx[ni : ni+6 : ni+6]
	i[0], i[1], i[2] = base, base+1, base+2
	i[3], i[4], i[5] = base+1, base+2, base+3
	run.vLen += quadVertices
	run.iLen += 6
}

// submitSchedule draws every compiled phase of the current segment and clears it.
// It runs at every barrier — a composed attached-unit group, an overflowing model
// subject, the clear and the Expand marker — and at the end of Execute.
//
// A phase is submitted as one draw per run of its opaque batch followed by one
// draw per run of its destination batch, all into the composite, with no copy
// between them: since §13.3 the destination families are device blends, so the
// hardware reads the composite for them and the per-phase snapshot is gone. A
// whole segment is therefore ONE render pass — the destination never changes —
// and the phases survive only as the submission ORDER that the byte writers'
// dependencies require (§13.3 "The scheduler keeps its placement and loses its
// snapshots").
//
// The one exception is a run that declares a read slot: the fog composite still
// reads the pixels it rewrites, so drawBatch copies the phase's destination
// rectangle into the read surface just before that run draws.
func (r *Renderer) submitSchedule() {
	s := &r.sched
	if r.surfaces[0] == nil || s.nphase == 0 {
		s.resetSegment()
		return
	}
	r.modelStats.Phases += s.nphase
	compose := r.surfaces[0]
	for k := 0; k < s.nphase; k++ {
		p := &s.phases[k]
		r.drawBatch(compose, p, schedOpaque)
		if p.hasDest {
			r.drawBatch(compose, p, schedDest)
		}
	}
	s.resetSegment()
}

// drawBatch submits one phase's batch for a class into dst. Unbound image slots
// are filled with the table atlas (or, before a palette is installed, a 1×1
// placeholder) so no shader samples a nil slot; a command never reads a slot it
// did not request. A run that declares a read slot takes the phase's read copy
// first and binds it there.
func (r *Renderer) drawBatch(dst *ebiten.Image, p *schedPhase, class int) {
	b := &p.batch[class]
	if dst == nil || b.empty() {
		return
	}
	def := r.scene2D
	if class == schedDest {
		def = r.sceneDest
	}
	fill := r.placeholderImage()
	for i := range b.runs {
		run := &b.runs[i]
		if run.iLen == 0 {
			continue
		}
		shader := run.shader
		if shader == nil {
			shader = def
		}
		if shader == nil {
			continue
		}
		if run.readSlot != schedReadNone {
			// The one read copy of the frame. The phase's destination rectangles
			// are pairwise disjoint, so nothing this phase has already drawn lies
			// under this run, and the copy is the state the run must read.
			r.copyComposite(r.surfaces[1], dst, int(p.x0), int(p.y0), int(p.x1), int(p.y1))
		}
		for j := 0; j < 4; j++ {
			img := run.imgs[j]
			if int8(j) == run.readSlot {
				img = r.surfaces[1]
			}
			if img == nil {
				img = fill
			}
			r.sceneOpts.Images[j] = img
		}
		r.sceneOpts.Blend = run.blend
		r.beginPass(dst)
		dst.DrawTrianglesShader32(
			b.verts[run.vOff:run.vOff+run.vLen],
			b.idx[run.iOff:run.iOff+run.iLen],
			shader, &r.sceneOpts)
		r.frameDraws++
	}
}

// copyComposite copies the clipped rectangle of the composite into the same
// rectangle of dst. It is a shader copy rather than an image draw so it allocates
// no sub-image per pass: the scene shader's colour copy op reads the composite's
// own RGB and writes it back unchanged (§13.3).
func (r *Renderer) copyComposite(dst, src *ebiten.Image, x0, y0, x1, y1 int) {
	if dst == nil || src == nil || r.scene2D == nil {
		return
	}
	x0, y0 = maxInt(x0, 0), maxInt(y0, 0)
	x1, y1 = minInt(x1, r.w), minInt(y1, r.h)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	fx0, fy0 := float32(x0), float32(y0)
	fx1, fy1 := float32(x1), float32(y1)
	corners := [4][2]float32{{fx0, fy0}, {fx1, fy0}, {fx0, fy1}, {fx1, fy1}}
	for i, c := range corners {
		r.copyVerts[i] = ebiten.Vertex{DstX: c[0], DstY: c[1], SrcX: c[0], SrcY: c[1],
			Custom3: sceneOpCopyColor}
	}
	fill := r.placeholderImage()
	r.sceneOpts.Images[0] = src
	r.sceneOpts.Images[1], r.sceneOpts.Images[2], r.sceneOpts.Images[3] = fill, fill, fill
	r.sceneOpts.Blend = blendComposite
	r.beginPass(dst)
	dst.DrawTrianglesShader32(r.copyVerts[:], r.copyIdx[:], r.scene2D, &r.sceneOpts)
	r.frameDraws++
}

// beginPass counts one device destination switch. Ebitengine's backends open a
// render pass whenever the destination image of a device call differs from the
// previous call's, and that switch is the executor's unit of cost
// (docs/DESIGN_GPU_RENDERER.md §11.5).
func (r *Renderer) beginPass(dst *ebiten.Image) {
	if dst == nil || dst == r.lastDest {
		return
	}
	r.lastDest = dst
	r.modelStats.Passes++
}

// placeholderImage is the 1×1 stand-in bound into unrequested image slots when
// no palette is installed. It is created once.
func (r *Renderer) placeholderImage() *ebiten.Image {
	if r.tables.atlas != nil {
		return r.tables.atlas
	}
	if r.placeholder == nil {
		r.placeholder = ebiten.NewImage(1, 1)
	}
	return r.placeholder
}
