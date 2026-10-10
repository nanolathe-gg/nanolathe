package aikit

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func checkpointLayoutAttempt(t *testing.T) (*ai.Manager, *ai.ApplicationAttempt) {
	t.Helper()
	m := &ai.Manager{Controller: ai.ControllerModern}
	if err := m.EnableCheckpointApplications(checkpoint.Identity{}, &content.CheckpointKeys{}); err != nil {
		t.Fatal(err)
	}
	h := m.CheckpointApplicationHistory()
	a := h.BeginAttempt(0x11223344, h.NextSerial(), 0, func(e *checkpoint.Encoder) error {
		e.U8(0x55)
		return e.Err()
	})
	return m, a
}

// The expected stream uses literal operation headers and the standard binary
// package, never a production checkpoint writer. Fixture intent is one byte.
type checkpointLayoutOps struct {
	bytes []byte
	count uint32
}

func (v *checkpointLayoutOps) grid(slot int64, field, action uint8, payload []byte) {
	v.bytes = binary.LittleEndian.AppendUint16(v.bytes, 8)
	v.bytes = binary.LittleEndian.AppendUint64(v.bytes, uint64(slot))
	v.bytes = append(v.bytes, field, action)
	v.bytes = append(v.bytes, payload...)
	v.count++
}

func layoutWord(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
func layoutCell(index int64, value uint32) []byte {
	v := binary.LittleEndian.AppendUint64(nil, uint64(index))
	return binary.LittleEndian.AppendUint32(v, value)
}

func assertCheckpointLayoutHistory(t *testing.T, m *ai.Manager, a *ai.ApplicationAttempt, want checkpointLayoutOps, terminal uint8) {
	t.Helper()
	if got := a.CommittedOperations(); got != want.count {
		t.Fatalf("committed operations = %d, want %d", got, want.count)
	}
	a.Finish(1, terminal)
	s, err := m.CheckpointApplicationHistory().Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	initial := append([]byte("NLCPAIST"), 2, 0)
	initial = append(initial, make([]byte, 64)...)
	initial = append(initial, 0, 2)
	previous := sha256.Sum256(initial)
	attempt := append([]byte("NLCPAIAP"), 2, 0)
	attempt = append(attempt, previous[:]...)
	// Player/controller, tick, serial, ordinal, intent and APM.
	attempt = append(attempt, checkpointDecode(t, "0002443322110100000000000000000000005501")...)
	attempt = binary.LittleEndian.AppendUint32(attempt, want.count)
	attempt = append(attempt, want.bytes...)
	attempt = append(attempt, terminal)
	if expected := sha256.Sum256(attempt); s.Count != 1 || s.Hash != expected {
		t.Fatalf("history count %d hash %x, want count 1 hash %x", s.Count, s.Hash, expected)
	}
}

// Authored operands pin every published field/action pair, including cost's
// independent presence and full-width raw handles/indices (§16.3.31).
func TestCheckpointLayoutMutationVectors(t *testing.T) {
	cases := []struct {
		w    checkpointGridWrite
		want string
	}{
		{checkpointGridWrite{field: 1, action: 1, value: 0x12345678}, "010178563412"},
		{checkpointGridWrite{field: 2, action: 2, boolean: true, value: 3}, "02020103000000"},
		{checkpointGridWrite{field: 2, action: 2}, "02020000000000"},
		{checkpointGridWrite{field: 2, action: 3, index: 1 << 40, value: -2}, "02030000000000010000feffffff"},
		{checkpointGridWrite{field: 3, action: 1, value: 0xabcd}, "0301cdab0000"},
		{checkpointGridWrite{field: 4, action: 1, value: -3}, "0401fdffffff"},
		{checkpointGridWrite{field: 5, action: 1, value: -4}, "0501fcffffff"},
		{checkpointGridWrite{field: 6, action: 2, value: 5}, "060205000000"},
		{checkpointGridWrite{field: 6, action: 3, index: 6, value: 0xffffffff}, "06030600000000000000ffffffff"},
		{checkpointGridWrite{field: 7, action: 1, value: 0x80000000}, "070100000080"},
		{checkpointGridWrite{field: 8, action: 1, boolean: true}, "080101"},
		{checkpointGridWrite{field: 9, action: 4, value: 0}, "090400000000"},
		{checkpointGridWrite{field: 9, action: 5, index: 1 << 40, value: -7}, "09050000000000010000f9ffffff"},
		{checkpointGridWrite{field: 10, action: 2, value: 8}, "0a0208000000"},
		{checkpointGridWrite{field: 10, action: 3, index: 9, value: 0x80000000}, "0a03090000000000000000000080"},
		{checkpointGridWrite{field: 11, action: 1, value: 0xffffffff}, "0b01ffffffff"},
	}
	for _, tc := range cases {
		got := checkpointLeafBytes(t, func(e *checkpoint.Encoder) error { return tc.w.writeCheckpoint(e, 3) })
		if want := checkpointDecode(t, "0300000000000000"+tc.want); !bytes.Equal(got, want) {
			t.Fatalf("write %+v: got %x, want %x", tc.w, got, want)
		}
	}
	for _, w := range []checkpointGridWrite{
		{}, {field: 12, action: 1}, {field: 2, action: 1}, {field: 1, action: 2},
		{field: 9, action: 2}, {field: 9, action: 3}, {field: 6, action: 4}, {field: 2, action: 5},
		{field: 2, action: 3, index: -1}, {field: 9, action: 5, index: -1},
		{field: 6, action: 2, value: -1}, {field: 9, action: 4, value: 1 << 32},
	} {
		var out bytes.Buffer
		if err := w.writeCheckpoint(checkpoint.NewEncoder(&out), 0); err == nil {
			t.Fatalf("unpublished write admitted: %+v", w)
		}
	}
	for _, slot := range []int64{-1, 4} {
		var out bytes.Buffer
		if err := (checkpointGridWrite{field: 11, action: 1}).writeCheckpoint(checkpoint.NewEncoder(&out), slot); err == nil || out.Len() != 0 {
			t.Fatalf("invalid slot %d: %v, bytes %x", slot, err, out.Bytes())
		}
	}
}

func TestCheckpointLayoutReplacementAndEqualWrites(t *testing.T) {
	m, a := checkpointLayoutAttempt(t)
	e := &executor{m: m}
	restore := e.observeCheckpointLayout(a)
	g := &e.grids[2]
	g.ensure(1)
	g.ensure(1) // skipped length gate emits nothing
	g.overlay(0, 0, 1, 1)
	g.overlay(0, 0, 1, 1)   // the equal blocked value still emits
	g.overlay(-2, -2, 1, 1) // outside the window emits nothing
	g.ensure(0)             // present empty replacement, unlike an absent cost slice
	restore()
	var want checkpointLayoutOps
	for _, n := range []uint32{1, 0} {
		want.grid(2, 2, 2, append([]byte{1}, layoutWord(n)...))
		want.grid(2, 10, 2, layoutWord(n))
		want.grid(2, 6, 2, layoutWord(n))
		if n == 1 {
			want.grid(2, 2, 3, layoutCell(0, 0xffffffff))
			want.grid(2, 2, 3, layoutCell(0, 0xffffffff))
		}
	}
	if g.cost == nil || len(g.cost) != 0 || len(g.seen) != 0 || len(g.reach) != 0 {
		t.Fatal("ensure result changed")
	}
	assertCheckpointLayoutHistory(t, m, a, want, 4)
}

func checkpointBlockedGrid() exitGrid {
	g := exitGrid{}
	g.ensure(81 * 81)
	for i := range g.cost {
		g.cost[i] = -1
	}
	return g
}

func TestCheckpointLayoutFloodOrderAndWrap(t *testing.T) {
	m, a := checkpointLayoutAttempt(t)
	e := &executor{m: m}
	g := &e.grids[1]
	*g = checkpointBlockedGrid()
	g.stamp = ^uint32(0)
	g.cost[82], g.cost[81], g.cost[83] = 0, 0, 0
	g.seeds = []int32{82}
	restore := e.observeCheckpointLayout(a)
	if !g.flood(nil) {
		t.Fatal("authored route did not reach its edge")
	}
	restore()
	var want checkpointLayoutOps
	want.grid(1, 11, 1, layoutWord(0))
	want.grid(1, 11, 1, layoutWord(1))
	want.grid(1, 10, 3, layoutCell(82, 1))
	want.grid(1, 6, 3, layoutCell(82, 1))
	want.grid(1, 10, 3, layoutCell(81, 1))
	want.grid(1, 10, 3, layoutCell(83, 1))
	want.grid(1, 6, 3, layoutCell(83, 1)) // LIFO visits right before left
	want.grid(1, 6, 3, layoutCell(81, 1))
	want.grid(1, 7, 1, layoutWord(1))
	assertCheckpointLayoutHistory(t, m, a, want, 2)
}

func TestCheckpointLayoutRejectedSiteKeepsWrites(t *testing.T) {
	m, a := checkpointLayoutAttempt(t)
	e := &executor{m: m, keep: true, nGuard: 1}
	g := &e.grids[0]
	*g = checkpointBlockedGrid()
	g.fac, g.built, g.reachStamp = 7, 50, 19
	g.cost[81], g.cost[82], g.reach[81] = 0, 0, 19
	g.seeds = []int32{82}
	e.guardFacs[0] = OwnUnit{H: 7}
	restore := e.observeCheckpointLayout(a)
	if !e.guardRefuses(&UnitInfo{}, 0, 2, 2, 2, &units.World{}, 50) {
		t.Fatal("site closing the sole exit was accepted")
	}
	restore()
	if e.nextPending != 0 || e.pending != ([16]pendingSite{}) || g.reachStamp != 19 {
		t.Fatal("refused cut changed reservations or the unobstructed reach stamp")
	}
	var want checkpointLayoutOps
	want.grid(0, 11, 1, layoutWord(1))
	want.grid(0, 10, 3, layoutCell(81, 1))
	want.grid(0, 10, 3, layoutCell(82, 1))
	assertCheckpointLayoutHistory(t, m, a, want, 4)
}

func TestCheckpointLayoutPendingOrderAndWrap(t *testing.T) {
	m, a := checkpointLayoutAttempt(t)
	e := &executor{m: m, keep: true, nGuard: 1, nextPending: 15}
	for _, slot := range []int{1, 3} {
		e.grids[slot] = checkpointBlockedGrid()
		e.grids[slot].fac = pool.Handle(9 - slot) // handle order opposes physical order
	}
	// This occupied-looking slot has no owner and must be skipped.
	e.grids[2] = checkpointBlockedGrid()
	restore := e.observeCheckpointLayout(a)
	e.guardPlaced(0, 0, 1, 1)
	if e.nextPending != 15 || e.pending != ([16]pendingSite{}) {
		t.Fatal("ordinary guardPlaced invented a pending row")
	}
	e.placedRow(-1, 2, 3, 4, rowGroup(0xfe), ^uint32(0))
	restore()
	if e.nextPending != 0 || e.pending[15] != (pendingSite{cx: -1, cz: 2, fx: 3, fz: 4, g: 0xfe, tick: ^uint32(0)}) {
		t.Fatal("physical reservation/cursor wrap changed")
	}
	var want checkpointLayoutOps
	for _, slot := range []int64{1, 3} {
		want.grid(slot, 2, 3, layoutCell(0, 0xffffffff))
		want.grid(slot, 11, 1, layoutWord(1))
		want.grid(slot, 7, 1, layoutWord(1))
		want.grid(slot, 8, 1, []byte{1})
	}
	for _, slot := range []int64{1, 3} {
		want.grid(slot, 2, 3, layoutCell(81, 0xffffffff))
		want.grid(slot, 2, 3, layoutCell(162, 0xffffffff))
		want.grid(slot, 11, 1, layoutWord(2))
		want.grid(slot, 7, 1, layoutWord(2))
		want.grid(slot, 8, 1, []byte{1})
	}
	// Literal tag, slot, row and wrapped cursor; no owner encoder builds it.
	want.bytes = append(want.bytes, checkpointDecode(t, "07000f00000000000000ffffffff020000000300000004000000feffffffff0000000000000000")...)
	want.count++
	assertCheckpointLayoutHistory(t, m, a, want, 4)
}

func TestCheckpointLayoutScopeAndCapture(t *testing.T) {
	outerManager, outer := checkpointLayoutAttempt(t)
	innerManager, inner := checkpointLayoutAttempt(t)
	e := &executor{}
	baseline := executorCheckpointBytes(t, e)
	restoreOuter := e.observeCheckpointLayout(outer)
	e.observeCheckpointLayout(nil)() // nil scope leaves the outer observation intact
	for i := range e.grids {
		if got := e.grids[i].checkpointObservation; got.attempt != outer || got.slot != int64(i) {
			t.Fatalf("slot %d observation %+v", i, got)
		}
		var out bytes.Buffer
		if err := e.grids[i].writeGuardCheckpoint(checkpoint.NewEncoder(&out), "guard"); err == nil || !strings.Contains(err.Error(), "checkpointObservation") || out.Len() != 0 {
			t.Fatalf("active capture admitted: %v, bytes %x", err, out.Bytes())
		}
	}
	if e.selfGrid.checkpointObservation != (checkpointGridObservation{}) {
		t.Fatal("self grid was observed")
	}
	restoreInner := e.observeCheckpointLayout(inner)
	e.grids[0].ensure(1)
	restoreInner()
	e.grids[0].overlay(0, 0, 1, 1)
	restoreOuter()
	if outer.CommittedOperations() != 1 || inner.CommittedOperations() != 3 {
		t.Fatal("nested scope fanned out or lost prior observation")
	}
	outer.Finish(1, 4)
	inner.Finish(1, 4)
	for _, m := range []*ai.Manager{outerManager, innerManager} {
		if _, err := m.CheckpointApplicationHistory().Snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	// Stale slot metadata is not retained when no attempt is installed.
	e = &executor{}
	e.grids[2].checkpointObservation.slot = 99
	if !bytes.Equal(baseline, executorCheckpointBytes(t, e)) {
		t.Fatal("transient slot entered full checkpoint")
	}
}

func TestCheckpointLayoutGridBuildAndPendingOverlays(t *testing.T) {
	m, a := checkpointLayoutAttempt(t)
	m.Terrain = &world.Terrain{}
	e := &executor{m: m, mapInfo: &MapInfo{CellW: 2, CellH: 2,
		cellLo: make([]uint8, 4), cellVoid: []bool{true, true, true, true}}}
	for i := range e.grids {
		e.grids[i].built = 10
	}
	e.grids[2].built = 0 // actual replacement is physical slot two
	e.grids[2].seeds = []int32{91, 92, 93}
	e.pending[0] = pendingSite{cx: 0, cz: 0, fx: 2, fz: 2, tick: 100} // expired exactly at TTL
	e.pending[3] = pendingSite{cx: 4, cz: 4, fx: 2, fz: 2, tick: 999}
	e.pending[15] = pendingSite{cx: 2, cz: 2, fx: 2, fz: 2, tick: 999}
	f := OwnUnit{H: 0x1234, X: 32, Z: 32, Info: testFactory(2, 2)}
	restore := e.observeCheckpointLayout(a)
	g := e.gridFor(&f, nil, 1000)
	if g != &e.grids[2] || g.fac != f.H || g.built != 1000 || !g.sealed || !reflect.DeepEqual(g.seeds, []int32{3360, 3361}) {
		t.Fatal("authored grid build changed")
	}
	before := a.CommittedOperations()
	if e.gridFor(&f, nil, 1000) != g || a.CommittedOperations() != before {
		t.Fatal("fresh cache hit emitted writes or rebuilt")
	}
	// Failure clears the chosen slot's factory, even if already zero.
	e.mapInfo = nil
	f.H = 99
	for range 2 {
		if e.gridFor(&f, nil, 1000) != nil {
			t.Fatal("missing map unexpectedly built a grid")
		}
	}
	restore()
	var want checkpointLayoutOps
	want.grid(2, 2, 2, append([]byte{1}, layoutWord(6561)...))
	want.grid(2, 10, 2, layoutWord(6561))
	want.grid(2, 6, 2, layoutWord(6561))
	want.grid(2, 3, 1, layoutWord(0x1234))
	want.grid(2, 4, 1, layoutWord(0xffffffd9)) // origin -39
	want.grid(2, 5, 1, layoutWord(0xffffffd9))
	// This all-void two-cell map prices every window cell blocked.
	for i := int64(0); i < 6561; i++ {
		want.grid(2, 2, 3, layoutCell(i, 0xffffffff))
	}
	for _, i := range []int64{3198, 3199, 3279, 3280} {
		want.grid(2, 2, 3, layoutCell(i, 0xffffffff))
	}
	want.grid(2, 9, 4, layoutWord(0))
	want.grid(2, 9, 5, layoutCell(0, 3360))
	want.grid(2, 9, 5, layoutCell(1, 3361))
	want.grid(2, 1, 1, layoutWord(1000))
	want.grid(2, 2, 3, layoutCell(3362, 0xffffffff)) // physical pending slot 3
	want.grid(2, 2, 3, layoutCell(3280, 0xffffffff)) // physical pending slot 15
	want.grid(2, 11, 1, layoutWord(1))
	want.grid(2, 7, 1, layoutWord(1))
	want.grid(2, 8, 1, []byte{1})
	want.grid(0, 3, 1, layoutWord(0))
	want.grid(0, 3, 1, layoutWord(0))
	assertCheckpointLayoutHistory(t, m, a, want, 4)
}

// Real execBuild placement mutates the guard before its typed producer fails.
// Observation success/failure must not become another gameplay admission gate.
func TestCheckpointLayoutLaterBuildFailureDoesNotUndoWrites(t *testing.T) {
	type result struct {
		grid    []byte
		pending [16]pendingSite
		next    int
		stats   ApplyStats
		random  uint32
		draws   uint64
		calls   int
		request ai.BuildRequest
	}
	run := func(mode string) result {
		f := newCapFixture(t)
		e, m := &f.h.ex, f.h.m
		m.Controller = ai.ControllerModern
		_, m.Terrain = pocketMap(new(Rand), 80, 80, 0)
		extent, err := world.NewFootprintExtent(4, 4)
		if err != nil {
			t.Fatal(err)
		}
		yard, err := world.ParseYardMap("oooooooooooooooo", 4, 4)
		if err != nil {
			t.Fatal(err)
		}
		e.places = make([]*placeDef, int(f.tower.Index)+1)
		e.places[f.tower.Index] = &placeDef{ok: true, extent: extent, yard: yard, footX: 4, footZ: 4,
			rules: world.PlacementRules{MaxSlope: 100, MinWaterDepth: -10000, MaxWaterDepth: 10000}}
		e.grids[0] = checkpointBlockedGrid()
		e.grids[0].fac, e.grids[0].built, e.grids[0].sealed = f.lab, 100, true
		var got result
		m.SetQueueBuildTyped(func(req ai.BuildRequest) error {
			got.calls++
			got.request = req
			m.RNG.Uint32n(97)
			return errors.New("authored later refusal")
		})
		var a *ai.ApplicationAttempt
		if mode != "disabled" {
			if err := m.EnableCheckpointApplications(checkpoint.Identity{}, &content.CheckpointKeys{}); err != nil {
				t.Fatal(err)
			}
			h := m.CheckpointApplicationHistory()
			a = h.BeginAttempt(100, h.NextSerial(), 0, func(enc *checkpoint.Encoder) error { enc.U8(1); return enc.Err() })
			if mode == "failed" {
				h.Fail(errors.New("authored diagnostic failure"))
			}
		}
		restore := e.observeCheckpointLayout(a)
		b := batch{actors: []pool.Handle{f.con}, inst: []*units.Unit{f.w.Unit(f.con)}}
		c := Command{Kind: CmdBuild, count: 1, Product: f.tower, X: 512, Z: 512, Exact: true, Keep: true, Queued: true}
		if e.execBuild(&c, &b, 100, f.w) {
			t.Fatal("failing typed producer reported success")
		}
		restore()
		if got.calls != 1 || e.grids[0].stamp == 0 || (mode == "enabled" && a.CommittedOperations() == 0) {
			t.Fatalf("%s did not reach guard mutation and later producer: calls %d stamp %d operations %d", mode, got.calls, e.grids[0].stamp, a.CommittedOperations())
		}
		if a != nil {
			a.Finish(1, 4)
			_, err := m.CheckpointApplicationHistory().Snapshot()
			if (err != nil) != (mode == "failed") {
				t.Fatalf("%s history error %v", mode, err)
			}
		}
		got.grid = checkpointLeafBytes(t, func(enc *checkpoint.Encoder) error { return e.grids[0].writeGuardCheckpoint(enc, "guard") })
		got.pending, got.next, got.stats = e.pending, e.nextPending, e.stats
		got.random, got.draws = m.RNG.State, m.RNG.Draws()
		return got
	}
	baseline := run("disabled")
	for _, mode := range []string{"enabled", "failed"} {
		if got := run(mode); !reflect.DeepEqual(got, baseline) {
			t.Fatalf("%s observation changed placement, producer, RNG or statistics", mode)
		}
	}
}

func TestCheckpointLayoutDisabledAllocationsAndSelfExclusion(t *testing.T) {
	e := &executor{keep: true, nGuard: 1}
	e.grids[0] = checkpointBlockedGrid()
	e.grids[0].fac = 7
	if allocs := testing.AllocsPerRun(100, func() {
		e.observeCheckpointLayout(nil)()
		e.placedRow(0, 0, 1, 1, grpLoose, 7)
	}); allocs != 0 {
		t.Fatalf("disabled layout observation allocated %g", allocs)
	}
	m, a := checkpointLayoutAttempt(t)
	e.m = m
	restore := e.observeCheckpointLayout(a)
	e.selfGrid.ensure(1)
	e.selfGrid.overlay(0, 0, 1, 1)
	e.selfGrid.flood(nil)
	e.freeKey(&MoveClass{})
	e.dedupe.ensure(0)
	restore()
	assertCheckpointLayoutHistory(t, m, a, checkpointLayoutOps{}, 1)
}
