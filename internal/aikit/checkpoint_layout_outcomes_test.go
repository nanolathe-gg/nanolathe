package aikit

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type layoutOutcomeRules struct {
	orders.StrictRules
	before func(*orders.Queue)
}

func (r layoutOutcomeRules) BeforeCommand(q *orders.Queue) { r.before(q) }

type layoutOutcomeObserver struct{ count int }

func (o *layoutOutcomeObserver) RecordCheckpointOrder(orders.CheckpointOrderReceipt) { o.count++ }

func beginLayoutOutcome(t *testing.T, e *executor) (*checkpointCommand, func()) {
	t.Helper()
	e.m.Controller = ai.ControllerModern
	if err := e.m.EnableCheckpointApplications(checkpoint.Identity{}, &content.CheckpointKeys{}); err != nil {
		t.Fatal(err)
	}
	h := e.m.CheckpointApplicationHistory()
	a := h.BeginAttempt(0x11223344, h.NextSerial(), 0, func(enc *checkpoint.Encoder) error { enc.U8(0x55); return enc.Err() })
	c := &checkpointCommand{attempt: a}
	prior := e.checkpointApplication
	e.checkpointApplication = c
	restoreLayout := e.observeCheckpointLayout(a)
	return c, func() { restoreLayout(); e.checkpointApplication = prior }
}

type layoutOutcomeOps struct {
	data  []byte
	count uint32
}

func (o *layoutOutcomeOps) actor(tag uint16, actor checkpoint.Allocation) {
	o.data = binary.LittleEndian.AppendUint16(o.data, tag)
	o.data = binary.LittleEndian.AppendUint32(o.data, actor.Handle)
	o.data = binary.LittleEndian.AppendUint64(o.data, actor.Serial)
	o.count++
}
func (o *layoutOutcomeOps) prepare(actor checkpoint.Allocation) {
	o.actor(9, actor)
	o.data = append(o.data, 1)
	o.actor(9, actor)
	o.data = append(o.data, 2)
}
func (o *layoutOutcomeOps) insert(actor checkpoint.Allocation, index int64, node []byte) {
	o.actor(3, actor)
	o.data = append(o.data, 1) // primary segment
	o.data = binary.LittleEndian.AppendUint64(o.data, uint64(index))
	o.data = append(o.data, node...)
}

// The empty-key 136-byte node vector is independently laid out from §16.3.6.
// Neither the expected node nor operation bytes call an order constructor,
// receipt codec or checkpoint node writer.
func layoutOutcomeNode(row orders.ID, owner, target pool.Handle, x, y, z numeric.Fixed, tick uint32, caption bool, flags, static uint32) []byte {
	b := make([]byte, 136)
	if caption {
		b[9] = 1
	}
	binary.LittleEndian.PutUint32(b[10:], tick)
	binary.LittleEndian.PutUint32(b[14:], 0xffffffff)
	binary.LittleEndian.PutUint32(b[22:], flags)
	binary.LittleEndian.PutUint64(b[26:], uint64(x))
	binary.LittleEndian.PutUint64(b[34:], uint64(y))
	binary.LittleEndian.PutUint64(b[42:], uint64(z))
	b[62] = byte(row)
	binary.LittleEndian.PutUint32(b[64:], uint32(owner))
	binary.LittleEndian.PutUint32(b[89:], static)
	binary.LittleEndian.PutUint32(b[93:], uint32(target))
	return b
}

func assertLayoutOutcomeHash(t *testing.T, e *executor, c *checkpointCommand, ops layoutOutcomeOps, terminal uint8) {
	t.Helper()
	if got := c.attempt.CommittedOperations(); got != ops.count {
		t.Fatalf("operations %d, want %d", got, ops.count)
	}
	c.finish(1)
	s, err := e.m.CheckpointApplicationHistory().Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	initial := append([]byte("NLCPAIST"), 2, 0)
	initial = append(initial, make([]byte, 64)...)
	initial = append(initial, 0, 2)
	prev := sha256.Sum256(initial)
	b := append([]byte("NLCPAIAP"), 2, 0)
	b = append(b, prev[:]...)
	b = append(b, checkpointDecode(t, "0002443322110100000000000000000000005501")...)
	b = binary.LittleEndian.AppendUint32(b, ops.count)
	b = append(b, ops.data...)
	b = append(b, terminal)
	if want := sha256.Sum256(b); s.Count != 1 || s.Hash != want {
		t.Fatalf("count %d hash %x, want count 1 hash %x", s.Count, s.Hash, want)
	}
}

func TestCheckpointReclaimCoordinatesAndObserverScope(t *testing.T) {
	for _, unitTarget := range []bool{false, true} {
		u := &units.Unit{Handle: 7, AllocationSerial: 9, Alive: true, Def: &content.UnitDef{CanReclamate: true}}
		ter := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
		ter.PlotAt(1, 2).SetHeight(11)
		ter.PlotAt(2, 2).SetHeight(19)
		ter.PlotAt(1, 3).SetHeight(27)
		ter.PlotAt(2, 3).SetHeight(35)
		e := &executor{m: &ai.Manager{Terrain: ter}}
		q := orders.BindQueueBinding(u, &orders.QueueBinding{})
		prior := &layoutOutcomeObserver{}
		q.SetCheckpointObserver(prior)
		c, restore := beginLayoutOutcome(t, e)
		actor := checkpoint.Allocation{Handle: 7, Serial: 9}
		var x, y, z numeric.Fixed = 24 << 16, 23 << 16, 40 << 16
		row, target, static := orders.Lookup("Reclaim"), pool.Handle(0), uint32(0x100800)
		if unitTarget {
			x, y, z = -(1<<40)+3, (1<<41)+5, (13<<16)+7
			row, target, static = orders.Lookup("ReclaimUnit"), 11, 0x100200
			if !e.reclaimUnit(u, q, &units.Unit{Handle: target, Alive: true, X: x, Y: y, Z: z}, 37, true) {
				t.Fatal("unit reclaim refused")
			}
		} else if !e.reclaimFeature(u, q, 1, 2, 37, true) {
			t.Fatal("feature reclaim refused")
		}
		if prior.count != 0 || q.SetCheckpointObserver(nil) != prior || !c.accepted || c.rejected {
			t.Fatal("queue scope or exact success changed")
		}
		restore()
		var want layoutOutcomeOps
		want.actor(1, actor)
		want.actor(2, actor)
		want.prepare(actor)
		want.insert(actor, 0, layoutOutcomeNode(row, u.Handle, target, x, y, z, 37, true, 0x1000, static))
		assertLayoutOutcomeHash(t, e, c, want, 2)
	}
}

func TestCheckpointReclaimOuterOOMAfterNestedInsertion(t *testing.T) {
	for _, unitTarget := range []bool{false, true} {
		u := &units.Unit{Handle: 7, AllocationSerial: 9, Alive: true, Def: &content.UnitDef{CanReclamate: true}}
		e := &executor{m: &ai.Manager{}}
		calls := 0
		q := orders.BindQueueBinding(u, &orders.QueueBinding{Rules: layoutOutcomeRules{before: func(q *orders.Queue) {
			calls++
			// This callback succeeds, fills the last slot, and changes the live
			// actor identity. Observation must still name the earlier allocation.
			if q.PushHead(orders.Lookup("Move_Ground"), orders.Node{Owner: 7, StaticGate: 1}) == nil {
				t.Fatal("nested insertion refused")
			}
			u.AllocationSerial = 99
		}}})
		rows := make([]*orders.Node, orders.OOMGuardQueue-1)
		for i := range rows {
			rows[i] = &orders.Node{Flags: orders.FlagPurgeSurvivor, StaticGate: 4}
		}
		q.SetPrimary(rows)
		c, restore := beginLayoutOutcome(t, e)
		var legacy bool
		if unitTarget {
			legacy = e.reclaimUnit(u, q, &units.Unit{Handle: 11, Alive: true}, 37, true)
		} else {
			legacy = e.reclaimFeature(u, q, 1, 2, 37, true)
		}
		if !legacy || calls != 1 || c.accepted || !c.rejected || len(q.Primary()) != orders.OOMGuardQueue || len(q.Diagnostics()) != 1 {
			t.Fatal("outer refusal borrowed nested success or changed legacy return")
		}
		restore()
		actor := checkpoint.Allocation{Handle: 7, Serial: 9}
		var want layoutOutcomeOps
		want.actor(1, actor)
		want.actor(2, actor)
		want.prepare(actor)
		want.insert(actor, 0, layoutOutcomeNode(orders.Lookup("Move_Ground"), 7, 0, 0, 0, 0, 0, false, 0, 1))
		assertLayoutOutcomeHash(t, e, c, want, 4)
	}
}

func layoutClearFixture(t *testing.T) (*executor, *units.World, *units.Unit, *Command, *batch) {
	t.Helper()
	f := newGenFixture(t, &countBrain{})
	f.def.CanReclamate = true
	feature := &content.FeatureDef{Reclaimable: true, Metal: 100, FootprintX: 1, FootprintZ: 1}
	second := &content.FeatureDef{Reclaimable: true, Metal: 50, FootprintX: 1, FootprintZ: 1}
	ter := &world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 32*32), FeatureDefs: []*content.FeatureDef{feature, second}}
	for i := range ter.Plot {
		ter.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	ter.PlotAt(8, 8).SetFeature(0)
	ter.PlotAt(9, 8).SetFeature(1)
	e := &executor{m: &ai.Manager{Terrain: ter}, mapInfo: &MapInfo{CellW: 32, CellH: 32}}
	u := f.w.Unit(f.own)
	return e, f.w, u, &Command{Kind: CmdClear, count: 1, X: 136, Z: 136, Count: 96}, &batch{actors: []pool.Handle{u.Handle}, inst: []*units.Unit{u}}
}

func TestCheckpointClearSelectedTargetOutcomes(t *testing.T) {
	for _, mode := range []string{"success", "partial", "empty", "stale", "incapable"} {
		t.Run(mode, func(t *testing.T) {
			e, w, u, cmd, b := layoutClearFixture(t)
			actor := checkpoint.Allocation{Handle: uint32(u.Handle), Serial: u.AllocationSerial}
			calls := 0
			e.m.OrderBinding = &orders.QueueBinding{Rules: layoutOutcomeRules{before: func(*orders.Queue) {
				calls++
				if mode == "partial" {
					u.Def.CanReclamate = false
				}
			}}}
			switch mode {
			case "empty":
				e.m.Terrain.PlotAt(8, 8).SetFeature(world.PlotFeatureNone)
				e.m.Terrain.PlotAt(9, 8).SetFeature(world.PlotFeatureNone)
			case "stale":
				u.Alive = false
			case "incapable":
				u.Def.CanReclamate = false
			}
			c, restore := beginLayoutOutcome(t, e)
			got := e.execClear(cmd, b, 37, w)
			restore()
			var want layoutOutcomeOps
			terminal := uint8(3)
			if mode == "success" || mode == "partial" {
				if !got || !c.accepted || e.stats.Clears != 1 {
					t.Fatal("existing issued result or accepted outcome lost")
				}
				want.actor(1, actor)
				want.actor(2, actor)
				want.prepare(actor)
				want.insert(actor, 0, layoutOutcomeNode(orders.Lookup("Reclaim"), u.Handle, 0, 136<<16, 0, 136<<16, 37, true, 0x1000, 0x100800))
				terminal = 4
				if mode == "success" {
					want.prepare(actor)
					want.insert(actor, 1, layoutOutcomeNode(orders.Lookup("Reclaim"), u.Handle, 0, 152<<16, 0, 136<<16, 37, false, 0x1000, 0x100800))
					terminal = 2
					if c.rejected || calls != 2 || e.stats.ClearMetal != 150 {
						t.Fatal("successful selected target outcomes changed")
					}
				} else if !c.rejected || calls != 1 || e.stats.ClearMetal != 100 {
					t.Fatal("later rejected target vanished behind any-issued result")
				}
			} else if got || !c.rejected || c.accepted || orders.QueueOfUnit(u) != nil {
				t.Fatal("refusal admitted work or created a queue early")
			}
			assertLayoutOutcomeHash(t, e, c, want, terminal)
		})
	}
}

// A one-macro-cell-wide corridor admits only the two authored blockers,
// nearest first. Every other cell is void, so opening cannot go around them.
func layoutUnblockFixture(t *testing.T) (*executor, *units.World, *units.Unit, *Command, *batch, []*units.Unit) {
	t.Helper()
	builder := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", BMCode: 1, CanMove: true, CanReclamate: true, MaxDamage: 100}
	factory := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "factory"}, UnitName: "factory", MaxDamage: 100}
	blocker := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "blocker"}, UnitName: "blocker", MaxDamage: 100}
	w := fixtureWorld(&content.Catalog{Units: map[string]*content.UnitDef{"builder": builder, "factory": factory, "blocker": blocker}})
	create := func(d *content.UnitDef, x, y, z numeric.Fixed) *units.Unit {
		h, err := w.Create(d, 0, x, y, z)
		if err != nil {
			t.Fatal(err)
		}
		return w.Unit(h)
	}
	u := create(builder, 100<<16, 0, 100<<16)
	f := create(factory, 2048<<16, 0, 2048<<16)
	blocks := []*units.Unit{create(blocker, 2048<<16, 7<<16, 2176<<16), create(blocker, 2048<<16, 11<<16, 2240<<16)}
	m, ter := pocketMap(new(Rand), 256, 256, 0)
	for i := range m.cellVoid {
		m.cellVoid[i] = true
	}
	for z := int32(130); z <= 209; z++ {
		for x := int32(128); x <= 129; x++ {
			m.cellVoid[z*m.CellW+x] = false
		}
	}
	ter.PlotAt(128, 136).SetOccupantA(int16(blocks[0].Handle))
	ter.PlotAt(128, 140).SetOccupantA(int16(blocks[1].Handle))
	e := &executor{m: &ai.Manager{Terrain: ter}, mapInfo: m, table: &Table{byDef: map[*content.UnitDef]*UnitInfo{factory: testFactory(2, 2), blocker: {Def: blocker}}}}
	c := &Command{Kind: CmdUnblock, count: 1, Target: f.Handle, target: f}
	return e, w, u, c, &batch{actors: []pool.Handle{u.Handle}, inst: []*units.Unit{u}}, blocks
}

func TestCheckpointUnblockSelectedTargetOutcomes(t *testing.T) {
	for _, mode := range []string{"success", "missing", "unresolved", "open", "blocked", "stale", "empty"} {
		t.Run(mode, func(t *testing.T) {
			e, w, u, cmd, b, blocks := layoutUnblockFixture(t)
			actor := checkpoint.Allocation{Handle: uint32(u.Handle), Serial: u.AllocationSerial}
			calls := 0
			e.m.OrderBinding = &orders.QueueBinding{Rules: layoutOutcomeRules{before: func(*orders.Queue) {
				calls++
				if calls == 1 && mode == "missing" {
					w.FreeImmediate(blocks[1].Handle)
				}
				if mode == "unresolved" {
					u.Def.CanReclamate = false
				}
			}}}
			switch mode {
			case "open":
				e.m.Terrain.PlotAt(128, 136).SetOccupantA(0)
				e.m.Terrain.PlotAt(128, 140).SetOccupantA(0)
			case "blocked":
				e.mapInfo.cellVoid[136*256+128] = true
			case "stale":
				cmd.target.Alive = false
			case "empty":
				cmd.count = 0
			}
			c, restore := beginLayoutOutcome(t, e)
			got := e.execUnblock(cmd, b, 37, w)
			restore()
			var want layoutOutcomeOps
			terminal := uint8(3)
			switch mode {
			case "success", "missing", "unresolved":
				if !got || !c.accepted || e.stats.Unblocks != 1 {
					t.Fatal("existing issued result or accepted outcome lost")
				}
				want.actor(1, actor)
				want.actor(2, actor)
				want.prepare(actor)
				want.insert(actor, 0, layoutOutcomeNode(orders.Lookup("ReclaimUnit"), u.Handle, blocks[0].Handle, 2048<<16, 7<<16, 2176<<16, 37, true, 0x1000, 0x100200))
				terminal = 4
				if mode == "success" {
					want.prepare(actor)
					want.insert(actor, 1, layoutOutcomeNode(orders.Lookup("ReclaimUnit"), u.Handle, blocks[1].Handle, 2048<<16, 11<<16, 2240<<16, 37, false, 0x1000, 0x100200))
					terminal = 2
					if c.rejected || calls != 2 {
						t.Fatal("successful blockers not accepted")
					}
				} else if !c.rejected || calls != 1 {
					t.Fatal("later missing or rejected blocker vanished behind any-issued result")
				}
			case "open":
				if !got || !c.noop || c.accepted || c.rejected || e.stats.Unblocks != 0 || orders.QueueOfUnit(u) != nil {
					t.Fatal("open exit was not an explicit no-op")
				}
				terminal = 1
			default:
				if got || !c.rejected || c.accepted || orders.QueueOfUnit(u) != nil {
					t.Fatal("refusal admitted work or bound a queue")
				}
			}
			// selfGrid is rebuilt by this command but never belongs in its
			// application operation stream (DESIGN_MULTIPLAYER §16.3.31).
			assertLayoutOutcomeHash(t, e, c, want, terminal)
		})
	}
}

func TestCheckpointLayoutOutcomeObservationPreservesExecution(t *testing.T) {
	type result struct {
		issued   bool
		stats    ApplyStats
		calls    int
		rngState uint32
		draws    uint64
		queue    []byte
		self     []byte
	}
	for _, kind := range []string{"clear", "unblock"} {
		for _, partial := range []bool{false, true} {
			var baseline result
			for _, observation := range []string{"disabled", "enabled", "failed"} {
				e, w, u, cmd, b := layoutClearFixture(t)
				if kind == "unblock" {
					e, w, u, cmd, b, _ = layoutUnblockFixture(t)
				}
				stream := rng.NewSimulation(73)
				e.m.RNG = &stream
				got := result{}
				e.m.OrderBinding = &orders.QueueBinding{Rules: layoutOutcomeRules{before: func(*orders.Queue) {
					got.calls++
					stream.Uint32n(100)
					if partial {
						u.Def.CanReclamate = false
					}
				}}}
				var c *checkpointCommand
				restore := func() {}
				if observation != "disabled" {
					c, restore = beginLayoutOutcome(t, e)
					if observation == "failed" {
						e.m.CheckpointApplicationHistory().Fail(errors.New("authored failed observation"))
					}
				}
				if kind == "clear" {
					got.issued = e.execClear(cmd, b, 37, w)
				} else {
					got.issued = e.execUnblock(cmd, b, 37, w)
				}
				restore()
				got.stats, got.rngState, got.draws = e.stats, stream.State, stream.Draws()
				for _, n := range orders.QueueOfUnit(u).Primary() {
					got.queue = append(got.queue, checkpointLeafBytes(t, n.WriteCheckpointValue)...)
				}
				got.self = checkpointLeafBytes(t, func(enc *checkpoint.Encoder) error { return e.selfGrid.writeSelfCheckpoint(enc, "self") })
				if c != nil {
					c.finish(1)
					_, err := e.m.CheckpointApplicationHistory().Snapshot()
					if (err != nil) != (observation == "failed") {
						t.Fatalf("%s history error: %v", observation, err)
					}
				}
				if observation == "disabled" {
					baseline = got
					continue
				}
				if !bytes.Equal(got.queue, baseline.queue) || !bytes.Equal(got.self, baseline.self) || !reflect.DeepEqual(got, baseline) {
					t.Fatalf("%s partial=%v %s changed execution", kind, partial, observation)
				}
			}
		}
	}
}
