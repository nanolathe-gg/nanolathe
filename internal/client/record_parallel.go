package client

// Two-stage unit recording (docs/DESIGN_GPU_RENDERER.md §13.9).
//
// The recorder's per-unit work — piece transforms, projection, material
// resolution, polygon collection and the cached-lane rebase — is independent
// per unit and is the largest single item in the modern recorder's frame
// (§13.6 rows R3b/R4). Stage one computes it for every unit the bucket build
// admits, on a persistent worker pool, into a slot indexed by the unit's
// position in the committed frame's unit slice. Stage two is the unchanged
// sequential walk: it visits the buckets in exactly the order [03 R-RAST-01 §7]
// fixes and appends the Model commands from those slots.
//
// Determinism [I1]: no result is read from a slot until stage two reaches that
// unit's place in the bucket order, so scheduling cannot reach the recorded
// list. The pool is a presentation-side worker set; it reads the committed
// frame and writes nothing a simulation phase can observe [I6].

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// geometryPair is one unit's stage-one product: exactly the pair
// unitGeometryPair returns, built on a worker's own scratch arena. id is the
// presentation identity the slot was computed for, so stage two can prove the
// slot belongs to the unit it is about to record and fall back to computing
// inline if it ever does not.
type geometryPair struct {
	body, live *drawlist.ModelGeometry
	id         uint64
	ready      bool
}

// recordWorker is one stage-one participant. clone is a shallow copy of the
// recording client that shares every immutable and read-only field with it and
// owns its own scratch arena, so two workers never hand out the same borrowed
// slot. Writes a worker makes to its clone's own fields are dropped, which is
// safe only because stage one is a pure function of the committed frame and
// the pre-created cache entries — everything that must outlive the frame is
// reached through a pointer the clone shares with the recording client.
type recordWorker struct {
	clone Client
}

// recordPool is the persistent stage-one pool. The goroutines are started once
// and parked on their wake channel between frames: the frame budget is 8 ms at
// the Enhanced presentation rate and spawning a goroutine per unit per frame
// would spend a visible part of it on the scheduler.
type recordPool struct {
	workers []*recordWorker
	wake    []chan struct{}
	quit    chan struct{}
	done    sync.WaitGroup

	// cursor hands out job indices. Per-unit cost varies by an order of
	// magnitude (a commander against a solar collector), so a shared cursor
	// balances better than a fixed stripe.
	cursor atomic.Int64
	jobs   []int32
	units  []frame.UnitView
	pairs  []geometryPair
}

// newRecordPool starts the workers. n is the number of participants including
// the recording goroutine, which drains the same job list rather than blocking
// on the pool.
func newRecordPool(n int) *recordPool {
	if n < 1 {
		n = 1
	}
	// n == 1 leaves the recording goroutine as the only participant. The slot
	// array and the bucket-order consumption are unchanged, which is what the
	// determinism test compares an N-worker frame against.
	p := &recordPool{quit: make(chan struct{})}
	for i := 0; i < n-1; i++ {
		w := &recordWorker{}
		// One slot of buffer so the recording goroutine never blocks handing
		// out a wake: a worker that has not yet looped back to its select
		// would otherwise stall the frame's critical path for a scheduling
		// quantum.
		wake := make(chan struct{}, 1)
		p.workers = append(p.workers, w)
		p.wake = append(p.wake, wake)
		go p.serve(w, wake)
	}
	return p
}

func (p *recordPool) serve(w *recordWorker, wake chan struct{}) {
	for {
		select {
		case <-p.quit:
			return
		case <-wake:
			p.drain(&w.clone)
			p.done.Done()
		}
	}
}

// drain takes job indices until the list is exhausted. Each job writes only its
// own slot, so no two participants touch the same memory.
func (p *recordPool) drain(c *Client) {
	for {
		i := p.cursor.Add(1) - 1
		if i < 0 || int(i) >= len(p.jobs) {
			return
		}
		idx := p.jobs[i]
		v := p.units[idx]
		body, live := c.unitGeometryPair(v, false)
		p.pairs[idx] = geometryPair{body: body, live: live, id: unitPresentationID(v), ready: true}
	}
}

// parallelUnitFloor is the job count below which stage one stays on the
// recording goroutine. It is a timing threshold, not a behavioural one.
const parallelUnitFloor = 8

// recordPoolSize is the participant count, including the recording goroutine.
// One participant per hardware thread: the recorder is the frame's critical
// path and the window's own render thread is mostly waiting on the device
// while stage one runs.
func recordPoolSize() int {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	return n
}

// parallelUnitRecording reports whether this client may run stage one on the
// pool. It is the modern recorder's geometry lane only: the classic composer's
// per-unit work writes palette planes through a different set of borrowed
// slots and keeps the sequential path (§13.9).
//
// A standalone model registry is excluded because it loads and binds models on
// first use, which is a write to shared registry maps; a battle registry has
// every model bound before the first frame. A parity trace sink is excluded
// because it is a diagnostic capture path, not a measured one.
func (c *Client) parallelUnitRecording() bool {
	return c != nil && c.geometryOnlyModels && c.modelScratch.active && c.parallelRecord &&
		c.modelTextures != nil && !c.modelTextures.standalone &&
		c.rendererTraceSink == nil
}

// recordUnitGeometry collects this frame's stage-one job list and runs it. The
// admission it applies is exactly the one the two sequential passes apply — the
// pass-A window and mover-mode split of [03 R-RAST-01 §7], then presentUnit's
// own strategic-view, carrier-link and model-name gates — so every slot it
// fills is read, and a unit it declines simply computes inline as before.
func (c *Client) recordUnitGeometry(cur *frame.Frame, win worldWindow) {
	if cur == nil || !c.parallelUnitRecording() {
		return
	}
	if c.strategicView() {
		// The strategic view replaces every unit model with a marker (§16.10),
		// so there is no per-unit geometry to prepare.
		return
	}
	if c.recordPool == nil {
		c.recordPool = newRecordPool(recordPoolSize())
	}
	jobs := c.unitJobs[:0]
	for _, d := range c.worldBuckets.ordered() {
		if d.unit == nil || d.index < 0 {
			continue
		}
		if d.unit.MoverMode == moverModeGrounded {
			if !win.admitsPassARow(d.row) {
				continue
			}
		}
		if isCarried(*d.unit) || c.worldBuckets.isFactoryOccupant(d.index) || (d.unit.Model == "" && c.modelForUnit(*d.unit) == nil) {
			continue
		}
		jobs = append(jobs, d.index)
	}
	c.unitJobs = jobs
	c.prepareUnitGeometry(cur.Units, jobs)
}

// prepareUnitGeometry is stage one. jobs lists the indices into units of every
// unit the bucket walk will present on its own — the same admission both unit
// passes apply, resolved once here so the two passes read one slot array.
func (c *Client) prepareUnitGeometry(units []frame.UnitView, jobs []int32) {
	p := c.recordPool
	if p == nil || len(jobs) == 0 {
		return
	}
	// Every map the per-unit path can grow is grown here, on the recording
	// goroutine, so stage one reads those maps and writes only through the
	// pointers they already hold. Distinct units own distinct entries; it is
	// the insertion, not the entry, that cannot be concurrent.
	for _, idx := range jobs {
		id := unitPresentationID(units[idx])
		c.orientationCache(id)
		if c.cachedModelBodies == nil {
			c.cachedModelBodies = make(map[uint64]*cachedModelBody)
		}
		if c.cachedModelBodies[id] == nil {
			// An entry with no geometry and no image is indistinguishable from
			// a missing one at every read: both cachedGeometryMustRebuild and
			// the classic cachedBodyMustRebuild test the product, not the
			// entry, and replaceCachedGeometry fills the entry it finds.
			c.cachedModelBodies[id] = &cachedModelBody{}
		}
	}

	p.units = units
	p.jobs = jobs
	p.pairs = resizeScratch(p.pairs, len(units))
	clear(p.pairs)

	// A handful of units is not worth waking anyone for: the pool's own cost is
	// the wakes and the barrier, and below this the recording goroutine
	// finishes the list before a worker would have been scheduled. The slots
	// and their bucket-order consumption are the same either way, so this
	// changes timing only.
	if len(jobs) < parallelUnitFloor {
		p.drain(c)
		return
	}

	for _, w := range p.workers {
		// Carry the worker's own arena across the refresh: the recording
		// client's scratch slices must not be shared, and the worker's grown
		// capacity is what keeps a steady-state frame allocation-free.
		arena := w.clone.modelScratch
		w.clone = *c
		w.clone.modelScratch = arena
		w.clone.modelScratch.reset()
		w.clone.modelScratch.active = true
		// A worker never records. Stage two owns the list, and a command
		// appended to a clone's copy would be silently dropped rather than
		// racing, so the field is cleared to make that a nil-safe no-op.
		w.clone.recordPool = nil
		w.clone.parallelRecord = false
		w.clone.pendingPair = nil
	}
	p.cursor.Store(0)
	p.done.Add(len(p.wake))
	for _, wake := range p.wake {
		wake <- struct{}{}
	}
	p.drain(c)
	p.done.Wait()
}

// takeUnitGeometry hands stage two the slot for one unit, or nil when the unit
// was not part of stage one or the slot does not belong to it. The slot is
// consumed: a unit is presented once per frame, and clearing it means a stale
// slot can never be read twice.
func (c *Client) takeUnitGeometry(index int32, v frame.UnitView) *geometryPair {
	p := c.recordPool
	if p == nil || index < 0 || int(index) >= len(p.pairs) {
		return nil
	}
	slot := &p.pairs[index]
	if !slot.ready || slot.id != unitPresentationID(v) {
		return nil
	}
	slot.ready = false
	return slot
}
