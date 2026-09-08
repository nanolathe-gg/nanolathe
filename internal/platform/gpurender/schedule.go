package gpurender

import "github.com/hajimehoshi/ebiten/v2"

// The phase scheduler (docs/DESIGN_GPU_RENDERER.md §11.2 "The scheduler"). The
// Sink methods no longer draw: each compiles its command's clipped screen
// rectangle, its class and its vertices into a phase, and Execute submits the
// phases in order once Replay returns. Order is preserved exactly where pixels
// depend on each other and relaxed everywhere else (C-G3).
//
// A phase is submitted as
//
//  1. one draw per run of its opaque batch,
//  2. one copy of the offscreen into the phase snapshot, over the union
//     rectangle of the phase's destination-reading batch,
//  3. one draw per run of its destination-reading batch.
//
// Commands are appended to phases in record order under the 32-pixel grid rules
// of §11.2:
//
//   - an opaque command joins the current phase unless its rectangle overlaps a
//     destination-reading command of the current phase; then it opens the next
//     phase, because it must overwrite that result;
//   - a destination-reading command joins the current phase unless its rectangle
//     overlaps another destination-reading command of the current phase; then it
//     opens the next phase, because the later one reads the earlier one's result;
//   - cells are tagged with the phase serial, so both tests cost one grid lookup
//     per covered cell and allocate nothing after warm-up.
//
// Both tests are therefore the same test: does this rectangle overlap a
// destination-reading command of the current phase. Only destination-reading
// commands tag, because two opaque writes in one batch resolve in vertex order
// on the device, which is record order.
//
// A cell tag names the destination-reading commands that covered it, and the
// test then compares their rectangles with this one, so a rectangle that merely
// shares a 32-pixel cell with an earlier destination write no longer splits the
// phase. A cell remembers schedCellOwners of them and then saturates, answering
// every later test the way the plain cell test did.
//
// Two relaxations of the rectangle test are exact, not heuristic:
//
//   - A lit point batch tags the cell of each visible point rather than the
//     cells of its bounding rectangle, and two points conflict only when they
//     write the same pixel; the phase keeps the pixel set that decides it. Any
//     other command sharing a cell with a point still opens the next phase.
//   - A model body commit may join the phase of its own shadow commit. The
//     shadow pass writes — and reads — only pixels its own body plane leaves
//     uncovered, and the body commit writes only pixels its plane covers, so the
//     two pixel sets are disjoint and their order cannot matter
//     [03 R-REN-03D §4–§5]. The exemption names that one command and no other.
//
// Within a run, vertex order is record order, and the device rasterizes one
// draw's primitives in order, so overlapping opaque writes in the same batch
// still resolve to the later command (C-G3). A batch splits into consecutive
// runs — never reordered — when the next command needs a source image the run
// cannot bind, or when the run reaches the uint16 index domain.

const (
	// schedOpaque is the class of every command that reads no destination
	// pixel; schedDest is the class of the table lookups on the destination.
	schedOpaque  = 0
	schedDest    = 1
	schedClasses = 2
)

// schedGridShift is the 32-pixel screen grid of §11.2, as a shift.
const schedGridShift = 5

// schedNoOwner is the "no destination-reading command" owner tag, used both for
// an untagged cell's owner and for "this command exempts nothing".
const schedNoOwner = int32(-1)

// schedCellOwners is how many distinct destination-reading commands one 32-pixel
// cell remembers before it gives up and answers every later test
// conservatively. Four covers the overlapping-smoke case the phase count is
// dominated by; a fifth command on one cell costs a phase it might not have
// needed, never a wrong one.
const schedCellOwners = 4

// schedCellSaturated is the owner count of a cell that ran out of slots.
const schedCellSaturated = 255

// schedDestRect is one destination-reading command's clipped screen rectangle,
// retained for the phase so a later command that lands on one of its cells is
// tested against the rectangle instead of the cell. points marks the phase's lit
// point owner, whose covered pixels are the phase's point set rather than a
// filled rectangle.
type schedDestRect struct {
	x0, y0, x1, y1 int32
	points         bool
}

func (d *schedDestRect) overlaps(x0, y0, x1, y1 int) bool {
	return int32(x0) < d.x1 && int32(x1) > d.x0 && int32(y0) < d.y1 && int32(y1) > d.y0
}

// schedRun is one device draw: a contiguous slice of a class's vertex and index
// scratch, plus the four source images it binds. imgs entries left nil are
// filled with a placeholder at submission, so a shader never samples an unbound
// slot.
type schedRun struct {
	imgs       [4]*ebiten.Image
	vOff, vLen int32
	iOff, iLen int32
}

// schedPhase is one opaque batch, one snapshot and one destination-reading
// batch. x0..y1 is the union rectangle of the destination batch, which is all
// the snapshot copy has to cover.
type schedPhase struct {
	opFirst, opCount int32
	dsFirst, dsCount int32
	x0, y0, x1, y1   int32
	hasDest          bool
}

// scheduler owns the compiled frame. Every slice is reused across frames, so a
// steady-state frame allocates nothing here (§11.2 "Allocation policy").
type scheduler struct {
	phases []schedPhase
	runs   [schedClasses][]schedRun
	verts  [schedClasses][]ebiten.Vertex
	idx    [schedClasses][]uint16

	// tags carries the phase serial that last covered each 32-pixel cell with a
	// destination-reading command. Serials only ever increase, so a stale tag
	// from an earlier phase or an earlier frame never matches. owners names the
	// command that tagged the cell, an index into dests.
	tags       []uint32
	owners     []int32
	ownerCount []uint8
	dests      []schedDestRect
	cols, rows int
	serial     uint32

	// pointsOwner is the current phase's lit point owner, or schedNoOwner before
	// the phase has a point. pointPixels is the set of pixels that owner has
	// written in this phase, which is what decides whether the next point has to
	// open a new phase.
	pointsOwner int32
	pointPixels map[uint64]struct{}

	// cur is the open run per class within the current phase, or -1.
	cur [schedClasses]int32
}

// resetFrame drops the previous frame's compiled phases and re-sizes the cell
// grid. The backing arrays are retained.
func (s *scheduler) resetFrame(w, h int) {
	s.phases = s.phases[:0]
	for c := 0; c < schedClasses; c++ {
		s.runs[c] = s.runs[c][:0]
		s.verts[c] = s.verts[c][:0]
		s.idx[c] = s.idx[c][:0]
		s.cur[c] = -1
	}
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
		s.owners = make([]int32, cols*rows*schedCellOwners)
		s.ownerCount = make([]uint8, cols*rows)
		s.serial = 0
	}
	s.dests = s.dests[:0]
	s.nextSerial()
}

// nextSerial advances the phase serial, clearing the grid on the (practically
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

// newPhase opens the next phase. Both open runs close with it, because a run
// never spans phases.
func (s *scheduler) newPhase() {
	s.phases = append(s.phases, schedPhase{
		opFirst: int32(len(s.runs[schedOpaque])),
		dsFirst: int32(len(s.runs[schedDest])),
	})
	s.cur[schedOpaque], s.cur[schedDest] = -1, -1
	s.pointsOwner = schedNoOwner
	clear(s.pointPixels)
	s.nextSerial()
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

// touches reports whether the rectangle overlaps a destination-reading command
// of the current phase. Each covered cell names the commands that tagged it, and
// their own rectangles decide the answer, so sharing a 32-pixel cell is not
// enough. exempt names one owner whose written pixels are provably disjoint from
// this rectangle's, or schedNoOwner for none.
func (s *scheduler) touches(x0, y0, x1, y1 int, exempt int32) bool {
	cx0, cy0, cx1, cy1, ok := s.cellRange(x0, y0, x1, y1)
	if !ok {
		return false
	}
	for cy := cy0; cy <= cy1; cy++ {
		row := cy * s.cols
		for cx := cx0; cx <= cx1; cx++ {
			at := row + cx
			if s.tags[at] != s.serial {
				continue
			}
			n := s.ownerCount[at]
			if n == schedCellSaturated {
				return true
			}
			for k := 0; k < int(n); k++ {
				o := s.owners[at*schedCellOwners+k]
				if o == exempt {
					continue
				}
				d := &s.dests[o]
				// A point owner covers single pixels this test cannot
				// enumerate, so sharing one of its cells is a conflict for
				// anything but another point (touchesPoint).
				if d.points || d.overlaps(x0, y0, x1, y1) {
					return true
				}
			}
		}
	}
	return false
}

// touchesPoint is touches for one pixel of a lit point batch: another point of
// this phase conflicts only when it wrote this very pixel, while a rectangle
// command is tested exactly, as a one-pixel rectangle.
func (s *scheduler) touchesPoint(x, y int) bool {
	cx0, cy0, _, _, ok := s.cellRange(x, y, x+1, y+1)
	if !ok {
		return false
	}
	at := cy0*s.cols + cx0
	if s.tags[at] != s.serial {
		return false
	}
	n := s.ownerCount[at]
	if n == schedCellSaturated {
		return true
	}
	for k := 0; k < int(n); k++ {
		d := &s.dests[s.owners[at*schedCellOwners+k]]
		if d.points {
			if _, repeated := s.pointPixels[pointKey(x, y)]; repeated {
				return true
			}
			continue
		}
		if d.overlaps(x, y, x+1, y+1) {
			return true
		}
	}
	return false
}

func pointKey(x, y int) uint64 { return uint64(uint32(x))<<32 | uint64(uint32(y)) }

// tag records a new destination-reading command over the rectangle and marks
// every cell it covers as owned by it.
func (s *scheduler) tag(x0, y0, x1, y1 int) int32 {
	s.dests = append(s.dests, schedDestRect{int32(x0), int32(y0), int32(x1), int32(y1), false})
	owner := int32(len(s.dests) - 1)
	s.tagCells(x0, y0, x1, y1, owner)
	return owner
}

// tagPoint records one lit point pixel under the phase's shared point owner,
// whose rectangle grows to the batch's bounding box so the snapshot copy covers
// it.
func (s *scheduler) tagPoint(x, y int) {
	if s.pointsOwner == schedNoOwner {
		s.dests = append(s.dests, schedDestRect{int32(x), int32(y), int32(x + 1), int32(y + 1), true})
		s.pointsOwner = int32(len(s.dests) - 1)
	} else {
		d := &s.dests[s.pointsOwner]
		d.x0, d.y0 = int32(minInt(int(d.x0), x)), int32(minInt(int(d.y0), y))
		d.x1, d.y1 = int32(maxInt(int(d.x1), x+1)), int32(maxInt(int(d.y1), y+1))
	}
	s.tagCells(x, y, x+1, y+1, s.pointsOwner)
	if s.pointPixels == nil {
		s.pointPixels = make(map[uint64]struct{})
	}
	s.pointPixels[pointKey(x, y)] = struct{}{}
}

// tagCells records owner on every cell of the rectangle. A cell keeps up to
// schedCellOwners distinct owners and then saturates, answering every later test
// conservatively.
func (s *scheduler) tagCells(x0, y0, x1, y1 int, owner int32) {
	cx0, cy0, cx1, cy1, ok := s.cellRange(x0, y0, x1, y1)
	if !ok {
		return
	}
	for cy := cy0; cy <= cy1; cy++ {
		row := cy * s.cols
		for cx := cx0; cx <= cx1; cx++ {
			at := row + cx
			if s.tags[at] != s.serial {
				s.tags[at] = s.serial
				s.ownerCount[at] = 1
				s.owners[at*schedCellOwners] = owner
				continue
			}
			n := s.ownerCount[at]
			if n == schedCellSaturated {
				continue
			}
			held := false
			for k := 0; k < int(n); k++ {
				if s.owners[at*schedCellOwners+k] == owner {
					held = true
					break
				}
			}
			if held {
				continue
			}
			if int(n) == schedCellOwners {
				s.ownerCount[at] = schedCellSaturated
				continue
			}
			s.owners[at*schedCellOwners+int(n)] = owner
			s.ownerCount[at] = n + 1
		}
	}
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

// openRun starts a new run of the class with the given bindings. Both open runs
// close with a phase, because a run never spans phases.
func (s *scheduler) openRun(class int, imgs [4]*ebiten.Image) {
	s.runs[class] = append(s.runs[class], schedRun{
		imgs: imgs,
		vOff: int32(len(s.verts[class])),
		iOff: int32(len(s.idx[class])),
	})
	s.cur[class] = int32(len(s.runs[class]) - 1)
	p := &s.phases[len(s.phases)-1]
	if class == schedOpaque {
		p.opCount++
	} else {
		p.dsCount++
	}
}

// begin places one command: it decides whether the command opens the next phase,
// tags the grid for a destination-reading command, extends the phase's snapshot
// rectangle, and selects the run its quads will land in. x0..y1 is the command's
// clipped screen rectangle. It returns false for an empty rectangle, which the
// caller treats as a command that covers nothing.
func (s *scheduler) begin(class int, x0, y0, x1, y1 int, imgs [4]*ebiten.Image) bool {
	return s.beginExempt(class, x0, y0, x1, y1, imgs, schedNoOwner) >= 0
}

// beginExempt is begin with one destination-reading command of the current phase
// whose pixels this command provably cannot overwrite or be read by. It returns
// the placed command's owner index for a destination-reading command, 0 for an
// opaque one, and -1 for an empty rectangle.
func (s *scheduler) beginExempt(class int, x0, y0, x1, y1 int, imgs [4]*ebiten.Image, exempt int32) int32 {
	if x0 >= x1 || y0 >= y1 {
		return -1
	}
	if len(s.phases) == 0 {
		s.newPhase()
	}
	if s.touches(x0, y0, x1, y1, exempt) {
		s.newPhase()
	}
	owner := int32(0)
	if class == schedDest {
		owner = s.tag(x0, y0, x1, y1)
		s.growSnapshot(x0, y0, x1, y1)
	}
	s.selectRun(class, imgs)
	return owner
}

// beginPoint places one lit point of a destination-reading batch. Its cell is
// tagged, not its batch's bounding rectangle, and it opens the next phase only
// when this phase already wrote this very pixel or covered it with a rectangle
// command.
func (s *scheduler) beginPoint(x, y int, imgs [4]*ebiten.Image) {
	if len(s.phases) == 0 {
		s.newPhase()
	}
	if s.touchesPoint(x, y) {
		s.newPhase()
	}
	s.tagPoint(x, y)
	s.growSnapshot(x, y, x+1, y+1)
	s.selectRun(schedDest, imgs)
}

// growSnapshot extends the phase's snapshot rectangle, which is the union of its
// destination-reading batch and all the snapshot copy has to cover.
func (s *scheduler) growSnapshot(x0, y0, x1, y1 int) {
	p := &s.phases[len(s.phases)-1]
	if !p.hasDest {
		p.x0, p.y0, p.x1, p.y1 = int32(x0), int32(y0), int32(x1), int32(y1)
		p.hasDest = true
		return
	}
	p.x0, p.y0 = int32(minInt(int(p.x0), x0)), int32(minInt(int(p.y0), y0))
	p.x1, p.y1 = int32(maxInt(int(p.x1), x1)), int32(maxInt(int(p.y1), y1))
}

// selectRun keeps the class's open run when it can bind this command's sources,
// and opens the next run when it cannot.
func (s *scheduler) selectRun(class int, imgs [4]*ebiten.Image) {
	cur := s.cur[class]
	if cur < 0 || !bindable(&s.runs[class][cur].imgs, &imgs) {
		s.openRun(class, imgs)
		return
	}
	bind(&s.runs[class][cur].imgs, &imgs)
}

// quad appends one axis-aligned quad to the open run of the class, splitting the
// run when the uint16 index domain is reached. Splitting never reorders
// geometry: the new run is drawn immediately after this one (C-G3).
func (s *scheduler) quad(class int, dx0, dy0, dx1, dy1, sx0, sy0, sx1, sy1 float32, col, custom [4]float32) {
	cur := s.cur[class]
	if cur < 0 {
		return
	}
	run := &s.runs[class][cur]
	if int(run.vLen)+quadVertices > quadBatchVertexLimit {
		imgs := run.imgs
		s.openRun(class, imgs)
		cur = s.cur[class]
		run = &s.runs[class][cur]
	}
	base := uint16(run.vLen)
	s.verts[class] = append(s.verts[class],
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
	s.idx[class] = append(s.idx[class], base, base+1, base+2, base+1, base+2, base+3)
	run.vLen += quadVertices
	run.iLen += 6
}

// submitSchedule draws every compiled phase in order and clears the compiled
// frame (§11.2 "The scheduler"). It is called at every barrier — a fog pass, a
// carrier/child group, an overflowing model subject, the clear and the
// expansion — and at the end of Execute.
func (r *Renderer) submitSchedule() {
	s := &r.sched
	if r.offscreen == nil {
		s.resetPending()
		return
	}
	for i := range s.phases {
		p := &s.phases[i]
		r.drawRuns(schedOpaque, p.opFirst, p.opCount, r.scene2D)
		if p.hasDest {
			r.snapshotRect(int(p.x0), int(p.y0), int(p.x1), int(p.y1))
			r.drawRuns(schedDest, p.dsFirst, p.dsCount, r.sceneDest)
		}
	}
	s.resetPending()
}

// resetPending drops the compiled phases without touching the grid serial, so
// the next phase after a barrier cannot alias a tag from before it.
func (s *scheduler) resetPending() {
	s.phases = s.phases[:0]
	for c := 0; c < schedClasses; c++ {
		s.runs[c] = s.runs[c][:0]
		s.verts[c] = s.verts[c][:0]
		s.idx[c] = s.idx[c][:0]
		s.cur[c] = -1
	}
	s.dests = s.dests[:0]
	s.pointsOwner = schedNoOwner
	clear(s.pointPixels)
	s.nextSerial()
}

// drawRuns submits one phase's batch for a class. Unbound image slots are filled
// with the table atlas (or, before a palette is installed, a 1×1 placeholder) so
// no shader samples a nil slot; an op never reads a slot it did not request.
func (r *Renderer) drawRuns(class int, first, count int32, shader *ebiten.Shader) {
	if shader == nil || count == 0 {
		return
	}
	s := &r.sched
	for i := first; i < first+count; i++ {
		run := &s.runs[class][i]
		if run.iLen == 0 {
			continue
		}
		fill := r.placeholderImage()
		for j := 0; j < 4; j++ {
			r.sceneOpts.Images[j] = run.imgs[j]
			if r.sceneOpts.Images[j] == nil {
				r.sceneOpts.Images[j] = fill
			}
		}
		// One blend serves every family: each fragment returns either an opaque
		// index or a fully transparent "skip", and source-over with alpha 1
		// stores the index unchanged while alpha 0 leaves the destination byte
		// in place — the byte writers' overwrite and key-skip exactly (C-G4).
		r.sceneOpts.Blend = ebiten.BlendSourceOver
		r.offscreen.DrawTrianglesShader(
			s.verts[class][run.vOff:run.vOff+run.vLen],
			s.idx[class][run.iOff:run.iOff+run.iLen],
			shader, &r.sceneOpts)
		r.frameDraws++
	}
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
