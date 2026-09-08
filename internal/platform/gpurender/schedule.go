package gpurender

import "github.com/hajimehoshi/ebiten/v2"

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
//     the same pixel. The pixel set is kept as one pixel→phase table for the
//     whole segment, so a later point at a written pixel is placed one phase
//     after the point that wrote it. Any other command sharing a cell with a
//     point is still pushed past it, because a rectangle test cannot enumerate
//     the point set.
// The lit point relaxation is the one relaxation of the rectangle test:
//
// # Barriers
//
// A barrier — the clear, a composed attached-unit group's shared staging image,
// a model subject the slot atlas could not fit, the expansion — ends a SEGMENT:
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
	// schedOpaque is the class of every command that reads no destination
	// pixel; schedDest is the class of the table lookups on the destination.
	schedOpaque  = 0
	schedDest    = 1
	schedClasses = 2
)

// schedGridShift is the 32-pixel screen grid of §11.2, as a shift.
const schedGridShift = 5

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

// schedReadNone marks a run that binds no read surface.
const schedReadNone = int8(-1)

// schedDestReadSlot is the image slot the destination shader samples the phase's
// read surface from (sceneDestShaderSource's snapAt). It is filled at
// submission, not at compilation, because which of the two alternating surfaces
// a phase reads is only known once the phases are ordered (§11.5).
const schedDestReadSlot = int8(2)

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
// index scratch, the four source images it binds, and the shader it draws with
// (nil for the class's own shader). imgs entries left nil are filled with a
// placeholder at submission, so a shader never samples an unbound slot; readSlot
// names the one slot the phase's read surface is bound into instead.
type schedRun struct {
	imgs       [4]*ebiten.Image
	shader     *ebiten.Shader
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
}

func (b *schedBatch) reset() {
	b.runs = b.runs[:0]
	b.verts = b.verts[:0]
	b.idx = b.idx[:0]
}

func (b *schedBatch) empty() bool { return len(b.idx) == 0 }

// schedPhase is one opaque batch and one destination-reading batch. x0..y1 is
// the union rectangle of the destination batch, the only region the next pass
// has to copy forward (§11.5).
type schedPhase struct {
	batch          [schedClasses]schedBatch
	x0, y0, x1, y1 int32
	hasDest        bool
}

func (p *schedPhase) reset() {
	p.batch[schedOpaque].reset()
	p.batch[schedDest].reset()
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

	// pointPixels maps a lit point's pixel to the phase of the last point that
	// wrote it, so the next point at that pixel is placed one phase later. It is
	// cleared, never reallocated, between segments.
	pointPixels map[uint64]int32

	// cur is where the command being compiled appends: the phase it was placed
	// in and its class. begin sets it; quad reads it.
	curPhase int32
	curClass int
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
	s.resetSegment()
}

// resetSegment drops the compiled phases and starts a fresh segment: a new grid
// serial, so no tag from before the barrier can be read after it.
func (s *scheduler) resetSegment() {
	for i := 0; i < s.nphase && i < len(s.phases); i++ {
		s.phases[i].reset()
	}
	s.nphase = 0
	s.owners = s.owners[:0]
	s.curPhase, s.curClass = -1, schedOpaque
	clear(s.pointPixels)
	s.nextSerial()
}

// nextSerial advances the segment serial, clearing the grid on the (practically
// unreachable) wrap so a stale tag can never alias the new serial.
func (s *scheduler) nextSerial() {
	if s.serial == ^uint32(0) {
		for i := range s.tags {
			s.tags[i] = 0
		}
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
			s.phases[s.nphase].reset()
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
// the largest contribution of every earlier command that can reach it. isPoint
// suppresses the cell-level lit point term, which the caller resolves exactly
// against the point pixel table.
func (s *scheduler) place(x0, y0, x1, y1 int, isPoint bool) int32 {
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
			if !isPoint {
				if p := s.cellPoint[at]; p >= 0 && p+1 > phase {
					phase = p + 1
				}
			}
		}
	}
	return phase
}

func pointKey(x, y int) uint64 { return uint64(uint32(x))<<32 | uint64(uint32(y)) }

// placePoint is place for one pixel of a lit point batch: an earlier point
// conflicts only when it wrote this very pixel, while a rectangle command is
// tested exactly, as a one-pixel rectangle.
func (s *scheduler) placePoint(x, y int) int32 {
	phase := s.place(x, y, x+1, y+1, true)
	if prev, ok := s.pointPixels[pointKey(x, y)]; ok && prev+1 > phase {
		phase = prev + 1
	}
	return phase
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

// tagPoint records one lit point pixel: its cell keeps the largest point phase
// it has seen, and the pixel table keeps the exact phase that wrote it.
func (s *scheduler) tagPoint(x, y int, phase int32) {
	cx, cy, _, _, ok := s.cellRange(x, y, x+1, y+1)
	if ok {
		at := cy*s.cols + cx
		s.touchCell(at)
		if phase > s.cellPoint[at] {
			s.cellPoint[at] = phase
		}
	}
	if s.pointPixels == nil {
		s.pointPixels = make(map[uint64]int32)
	}
	key := pointKey(x, y)
	if prev, held := s.pointPixels[key]; !held || phase > prev {
		s.pointPixels[key] = phase
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
func (b *schedBatch) selectRun(imgs [4]*ebiten.Image, shader *ebiten.Shader, readSlot int8) {
	if n := len(b.runs); n > 0 {
		run := &b.runs[n-1]
		if run.shader == shader && run.readSlot == readSlot && bindable(&run.imgs, &imgs) {
			bind(&run.imgs, &imgs)
			return
		}
	}
	b.openRun(imgs, shader, readSlot)
}

func (b *schedBatch) openRun(imgs [4]*ebiten.Image, shader *ebiten.Shader, readSlot int8) {
	b.runs = append(b.runs, schedRun{
		imgs:     imgs,
		shader:   shader,
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
	return s.beginShader(class, x0, y0, x1, y1, imgs, nil)
}

// beginShader is beginFloor for a command that draws with its own shader rather
// than the class's — today only the fog composite. The run binds the phase's read
// surface in slot 0 for the fog pass and in the destination shader's snapshot
// slot for every other destination-reading command.
func (s *scheduler) beginShader(class int, x0, y0, x1, y1 int, imgs [4]*ebiten.Image, shader *ebiten.Shader) bool {
	if x0 >= x1 || y0 >= y1 {
		return false
	}
	dest := class == schedDest
	phase := s.place(x0, y0, x1, y1, false)
	s.tag(x0, y0, x1, y1, phase, dest)
	p := s.phaseAt(phase)
	readSlot := schedReadNone
	if dest {
		s.growDest(p, x0, y0, x1, y1)
		readSlot = schedDestReadSlot
		if shader != nil {
			// The fog pass samples the pre-fog destination from its own first
			// slot (fogPassShaderSource), not from the destination shader's
			// snapshot slot.
			readSlot = 0
		}
	}
	p.batch[class].selectRun(imgs, shader, readSlot)
	s.curPhase, s.curClass = phase, class
	return true
}

// beginPoint places one lit point of a destination-reading batch. Its own pixel
// is what decides it, not its batch's bounding rectangle.
func (s *scheduler) beginPoint(x, y int, imgs [4]*ebiten.Image) {
	phase := s.placePoint(x, y)
	s.tagPoint(x, y, phase)
	p := s.phaseAt(phase)
	s.growDest(p, x, y, x+1, y+1)
	p.batch[schedDest].selectRun(imgs, nil, schedDestReadSlot)
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
		imgs, shader, readSlot := run.imgs, run.shader, run.readSlot
		b.openRun(imgs, shader, readSlot)
		run = &b.runs[len(b.runs)-1]
	}
	base := uint32(run.vLen)
	b.verts = append(b.verts,
		ebiten.Vertex{DstX: dx0, DstY: dy0, SrcX: sx0, SrcY: sy0,
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]},
		ebiten.Vertex{DstX: dx1, DstY: dy0, SrcX: sx1, SrcY: sy0,
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]},
		ebiten.Vertex{DstX: dx0, DstY: dy1, SrcX: sx0, SrcY: sy1,
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]},
		ebiten.Vertex{DstX: dx1, DstY: dy1, SrcX: sx1, SrcY: sy1,
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3]},
	)
	b.idx = append(b.idx, base, base+1, base+2, base+1, base+2, base+3)
	run.vLen += quadVertices
	run.iLen += 6
}

// submitSchedule draws every compiled phase of the current segment and clears it.
// It runs at every barrier — a composed attached-unit group, an overflowing model
// subject, the clear and the expansion — and at the end of Execute.
//
// A phase is submitted as
//
//  1. one draw per run of its opaque batch, into the composed surface;
//  2. one copy of the composed surface into the read surface, over the union
//     rectangle of the phase's destination-reading batch;
//  3. one draw per run of its destination-reading batch, into the composed
//     surface, reading the copy.
//
// The copy is what makes a destination-reading batch read the state its phase
// begins with rather than the pixels it is writing, and it only has to cover the
// batch's own rectangles: nothing in the batch samples the destination outside
// its own quad.
//
// TODO(H1): docs/DESIGN_GPU_RENDERER.md §11.5 asks for one pass per phase, with
// two full-size surfaces alternating so that the copy and its destination switch
// disappear. Implemented as written, that scheme composes a battle frame whose
// model shadow commits differ from this executor's by about 500 pixels of
// 2,073,600, and the difference survives every variation of the copy rectangle,
// the copy blend, run merging and the phase assignment; the same placement over
// the snapshot submission below is byte-identical. What remains unexplained is
// why the alternation itself changes those pixels, so the snapshot submission
// stands until it is. Critical-path placement alone still cuts the passes per
// frame by more than half, because it more than halves the phases.
func (r *Renderer) submitSchedule() {
	s := &r.sched
	if r.surfaces[0] == nil || s.nphase == 0 {
		s.resetSegment()
		return
	}
	r.modelStats.Phases += s.nphase
	compose, read := r.surfaces[0], r.surfaces[1]
	for k := 0; k < s.nphase; k++ {
		p := &s.phases[k]
		r.drawBatch(compose, p, schedOpaque, nil)
		if p.hasDest {
			r.copyIndexed(read, compose, int(p.x0), int(p.y0), int(p.x1), int(p.y1))
			r.drawBatch(compose, p, schedDest, read)
		}
	}
	s.resetSegment()
}

// drawBatch submits one phase's batch for a class into dst. read is the phase's
// read surface, bound into each run's declared read slot; it is nil for an opaque
// batch, which reads no destination. Unbound image slots are filled with the
// table atlas (or, before a palette is installed, a 1×1 placeholder) so no shader
// samples a nil slot; a command never reads a slot it did not request.
func (r *Renderer) drawBatch(dst *ebiten.Image, p *schedPhase, class int, read *ebiten.Image) {
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
		for j := 0; j < 4; j++ {
			img := run.imgs[j]
			if int8(j) == run.readSlot {
				img = read
			}
			if img == nil {
				img = fill
			}
			r.sceneOpts.Images[j] = img
		}
		// One blend serves every family: each fragment returns either an opaque
		// index or a fully transparent "skip", and source-over with alpha 1
		// stores the index unchanged while alpha 0 leaves the destination byte
		// in place — the byte writers' overwrite and key-skip exactly (C-G4).
		// The fog pass always returns alpha 1, so source-over reproduces the
		// copy blend its own draw used.
		r.sceneOpts.Blend = ebiten.BlendSourceOver
		r.beginPass(dst)
		dst.DrawTrianglesShader32(
			b.verts[run.vOff:run.vOff+run.vLen],
			b.idx[run.iOff:run.iOff+run.iLen],
			shader, &r.sceneOpts)
		r.frameDraws++
	}
}

// copyIndexed copies the clipped rectangle of src into the same rectangle of dst.
// It is a shader copy rather than an image draw so it allocates no sub-image per
// pass: the scene shader's plain copy op reads the source index and writes it
// back unchanged, which reproduces the stored byte exactly (C-G4).
func (r *Renderer) copyIndexed(dst, src *ebiten.Image, x0, y0, x1, y1 int) {
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
			Custom3: sceneOpCopy}
	}
	fill := r.placeholderImage()
	r.sceneOpts.Images[0] = src
	r.sceneOpts.Images[1], r.sceneOpts.Images[2], r.sceneOpts.Images[3] = fill, fill, fill
	r.sceneOpts.Blend = ebiten.BlendSourceOver
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
