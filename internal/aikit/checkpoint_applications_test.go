package aikit

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func modernApplicationFixture(t *testing.T, mode int) (*executor, *units.World, *units.Unit, *UnitInfo) {
	t.Helper()
	d := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", ObjectName: "fixture", MaxDamage: 100, BMCode: 1, Builder: true, CanMove: true, CanReclamate: true, FootprintX: 1, FootprintZ: 1}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": d}}
	m := &ai.Manager{Controller: ai.ControllerModern, Catalog: cat}
	random := rng.NewSimulation(17)
	m.RNG, m.OrderBinding = &random, &orders.QueueBinding{SimRNG: &random}
	if mode != 0 {
		if err := m.EnableCheckpointApplications(checkpoint.Identity{}, checkpointCommandKeys(t, cat)); err != nil {
			t.Fatal(err)
		}
		if mode == 2 {
			m.CheckpointApplicationHistory().Fail(errors.New("fixture failed history"))
		}
	}
	e := &executor{m: m, table: BuildTable(cat, nil)}
	w := fixtureWorld(cat)
	h, err := w.Create(d, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return e, w, w.Unit(h), e.table.Of(d)
}

func modernActor(u *units.Unit) checkpoint.Allocation {
	return checkpoint.Allocation{Handle: uint32(u.Handle), Serial: u.AllocationSerial}
}
func modernBatch(c []Command, actors ...*units.Unit) *batch {
	b := &batch{cmds: c, rowNear: -17}
	for _, u := range actors {
		b.actors = append(b.actors, u.Handle)
		b.inst = append(b.inst, u)
	}
	return b
}
func modernApply(e *executor, b *batch, w *units.World, p Persona) {
	e.checkpointBatchSerial = e.m.CheckpointApplicationHistory().NextSerial()
	e.apply(b, 91, w, &p)
	e.checkpointBatchSerial = 0
}

// These vectors assemble the history envelope, intent fields and operation
// tags independently of ApplicationHistory and the executor's adapters.
// The retained order node uses its separately tested value leaf.
type modernVector struct {
	hash  checkpoint.Digest
	count uint64
}

func newModernVector() modernVector {
	b := append([]byte("NLCPAIST"), 2, 0)
	b = append(b, make([]byte, 64)...)
	b = append(b, 0, 2)
	return modernVector{hash: sha256.Sum256(b)}
}
func (v *modernVector) append(t *testing.T, ordinal uint32, intent ai.ModernApplicationIntent, apm, terminal uint8, count uint32, ops func(*checkpoint.Encoder)) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("NLCPAIAP")
	b.Write([]byte{2, 0})
	b.Write(v.hash[:])
	enc := checkpoint.NewEncoder(&b)
	enc.U8(0)
	enc.U8(2)
	enc.U32(91)
	enc.U64(1)
	enc.U32(ordinal)
	enc.U8(intent.Kind)
	enc.Bool(intent.Queued)
	enc.U32(intent.RawTarget)
	enc.I32(intent.X)
	enc.I32(intent.Z)
	enc.I32(intent.ProductIndex)
	enc.String(intent.ProductKey)
	enc.I32(intent.Slot)
	enc.I32(intent.Count)
	enc.I32(intent.Spot)
	enc.I32(intent.Spacing)
	enc.Bool(intent.Keep)
	enc.Bool(intent.Exact)
	enc.I32(intent.RowNear)
	enc.Count(len(intent.Operands.Actors))
	for _, a := range intent.Operands.Actors {
		enc.Allocation(a)
	}
	enc.Bool(intent.Operands.Target != nil)
	if intent.Operands.Target != nil {
		enc.Allocation(*intent.Operands.Target)
	}
	enc.Bool(intent.Operands.Product != nil)
	if intent.Operands.Product != nil {
		enc.Definition(*intent.Operands.Product)
	}
	enc.U8(apm)
	enc.U32(count)
	if ops != nil {
		ops(enc)
	}
	enc.U8(terminal)
	if enc.Err() != nil {
		t.Fatal(enc.Err())
	}
	v.hash = sha256.Sum256(b.Bytes())
	v.count++
}
func (v modernVector) check(t *testing.T, e *executor) {
	t.Helper()
	got, err := e.m.CheckpointApplicationHistory().Snapshot()
	if err != nil || got.Hash != v.hash || got.Count != v.count || got.NextSerial != 2 {
		t.Fatalf("history got %+v (%v), want count %d hash %x", got, err, v.count, v.hash)
	}
}
func modernOp(enc *checkpoint.Encoder, kind uint16, actor checkpoint.Allocation) {
	enc.U16(kind)
	enc.Allocation(actor)
}
func modernPreparation(enc *checkpoint.Encoder, actor checkpoint.Allocation) {
	modernOp(enc, 9, actor)
	enc.U8(1)
	modernOp(enc, 9, actor)
	enc.U8(2)
}
func modernInsert(enc *checkpoint.Encoder, actor checkpoint.Allocation, segment uint8, index int64, node orders.Node) {
	modernOp(enc, 3, actor)
	enc.U8(segment)
	enc.I64(index)
	enc.Fail(node.WriteCheckpointValue(enc))
}
func modernBuild(enc *checkpoint.Encoder, actor checkpoint.Allocation, req ai.BuildRequest, verdict uint8) {
	modernOp(enc, 5, actor)
	enc.U32(uint32(req.Builder))
	enc.String(req.UnitKey)
	enc.I64(int64(req.X))
	enc.I64(int64(req.Z))
	enc.U8(0)
	enc.I64(int64(req.Count))
	enc.I64(int64(req.Kind))
	enc.U32(req.Tick)
	enc.U8(verdict)
}
func modernNodes(q *orders.Queue) [2][]orders.Node {
	var out [2][]orders.Node
	if q == nil {
		return out
	}
	for i, ns := range [][]*orders.Node{q.Primary(), q.Secondary()} {
		for _, n := range ns {
			out[i] = append(out[i], *n)
		}
	}
	return out
}

type modernBefore struct {
	orders.StrictRules
	before func()
}

func (r modernBefore) BeforeCommand(*orders.Queue) { r.before() }

type modernPriorObserver struct{ count int }

func (o *modernPriorObserver) RecordCheckpointOrder(orders.CheckpointOrderReceipt) { o.count++ }

func TestModernApplicationsActorOrderPartialAndAPM(t *testing.T) {
	type result struct {
		nodes  [2][]orders.Node
		stats  ApplyStats
		tokens int64
		state  uint32
		draws  uint64
		calls  int
	}
	var baseline result
	for mode := 0; mode < 3; mode++ {
		e, w, u, _ := modernApplicationFixture(t, mode)
		h, err := w.Create(u.Def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		stale := w.Unit(h)
		w.FreeImmediate(h)
		if _, err = w.Create(u.Def, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		calls := 0
		e.m.OrderBinding.Rules = modernBefore{before: func() { calls++; e.m.RNG.Uint32n(7) }}
		q := orders.QueueForUnit(u)
		prior := &modernPriorObserver{}
		q.SetCheckpointObserver(prior)
		b := modernBatch([]Command{{Kind: CmdMove, Queued: true, count: 3, X: 5, Z: -3}, {Kind: CmdAttack, count: 1, Target: stale.Handle, target: stale}, {Kind: CmdStop, count: 1}}, u, stale, u)
		e.tokens, e.lastFill = 2000, 91
		modernApply(e, b, w, Persona{APM: 60, Burst: 2})
		got := result{modernNodes(q), e.stats, e.tokens, e.m.RNG.State, e.m.RNG.Draws(), calls}
		if mode == 0 {
			baseline = got
		} else if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("mode %d altered gameplay", mode)
		}
		if q.SetCheckpointObserver(nil) != prior || e.checkpointApplication != nil {
			t.Fatal("observer scope not restored")
		}
		if mode != 1 {
			continue
		}
		if q.LenPrimary() != 2 || calls != 2 || e.stats.Applied != 1 || e.stats.DroppedAPM != 1 {
			t.Fatal("fixture did not execute partial group and APM refusal")
		}
		actor, old := modernActor(u), modernActor(stale)
		v := newModernVector()
		firstInserted := *q.PrimaryAt(0)
		firstInserted.Flags |= orders.FlagActive // the later insertion moved this marker
		v.append(t, 0, ai.ModernApplicationIntent{Kind: 1, Queued: true, X: 5, Z: -3, RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor, old, actor}}}, 2, 4, 6, func(enc *checkpoint.Encoder) {
			modernPreparation(enc, actor)
			modernInsert(enc, actor, 1, 0, firstInserted)
			modernPreparation(enc, actor)
			modernInsert(enc, actor, 1, 1, *q.PrimaryAt(1))
		})
		v.append(t, 1, ai.ModernApplicationIntent{Kind: 2, RawTarget: uint32(stale.Handle), RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor}, Target: &old}}, 2, 3, 0, nil)
		v.append(t, 2, ai.ModernApplicationIntent{Kind: 8, RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor}}}, 3, 3, 0, nil)
		v.check(t, e)
	}
}

func TestModernStopRecordsEmptyOperations(t *testing.T) {
	e, w, u, _ := modernApplicationFixture(t, 1)
	modernApply(e, modernBatch([]Command{{Kind: CmdStop, count: 1}}, u), w, Persona{})
	actor := modernActor(u)
	v := newModernVector()
	v.append(t, 0, ai.ModernApplicationIntent{Kind: 8, RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor}}}, 1, 2, 2, func(enc *checkpoint.Encoder) { modernOp(enc, 1, actor); modernOp(enc, 2, actor) })
	v.check(t, e)
}

func TestModernTypedApplicationsActualEffects(t *testing.T) {
	for _, kind := range []CmdKind{CmdProduce, CmdBuild, CmdReplace} {
		name := "produce"
		if kind == CmdBuild {
			name = "build"
		} else if kind == CmdReplace {
			name = "replace"
		}
		for _, outcome := range []string{"insert", "nil without insert", "failure after insert"} {
			t.Run(name+"/"+outcome, func(t *testing.T) {
				type result struct {
					nodes [2][]orders.Node
					stats ApplyStats
					req   ai.BuildRequest
					calls int
					state uint32
					draws uint64
				}
				var baseline result
				for mode := 0; mode < 3; mode++ {
					e, w, u, p := modernApplicationFixture(t, mode)
					g := NewRand(7, 0)
					e.mapInfo, e.m.Terrain = pocketMap(&g, 32, 32, 0)
					e.mapInfo.Spots = []MetalSpot{{X: 64, Z: 64}}
					extent, _ := world.NewFootprintExtent(1, 1)
					e.places = make([]*placeDef, int(p.Index)+1)
					e.places[p.Index] = &placeDef{ok: true, extent: extent, footX: 1, footZ: 1, mobile: true}
					oldH, err := w.Create(u.Def, 0, 16<<16, 0, 16<<16)
					if err != nil {
						t.Fatal(err)
					}
					old := w.Unit(oldH)
					c := Command{Kind: kind, count: 1, Product: p, Count: -4, X: 64, Z: 64, Exact: true}
					if kind == CmdReplace {
						c.Target, c.target = old.Handle, old
					}
					calls := 0
					var request ai.BuildRequest
					e.m.SetQueueBuildTyped(func(req ai.BuildRequest) error {
						calls++
						request = req
						if kind == CmdProduce && orders.QueueOfUnit(u) != nil {
							t.Fatal("producer queue bound before lazy callback")
						}
						if outcome != "nil without insert" {
							q := orders.BindQueueBinding(u, e.m.OrderBinding)
							restore := e.m.CheckpointApplicationHistory().ActiveAttempt().ObserveQueue(q, u)
							defer restore()
							id := orders.Lookup("MobileBuild")
							if kind == CmdProduce {
								id = orders.Lookup("BuildingBuild")
							}
							q.CoalesceTail(id, orders.Node{Owner: req.Builder, BuildDefKey: req.UnitKey, Param2: uint32(req.Count), CreationTick: req.Tick})
						}
						if outcome == "failure after insert" {
							return ai.WithCheckpointBuildVerdict(errors.New("fixture limit"), ai.CheckpointBuildLimit)
						}
						return nil
					})
					modernApply(e, modernBatch([]Command{c}, u), w, Persona{})
					q := orders.QueueOfUnit(u)
					got := result{modernNodes(q), e.stats, request, calls, e.m.RNG.State, e.m.RNG.Draws()}
					if mode == 0 {
						baseline = got
					} else if !reflect.DeepEqual(got, baseline) {
						t.Fatalf("mode %d altered gameplay", mode)
					}
					if calls != 1 || request.Tick != 0 || request.Count != 1 {
						t.Fatalf("actual callback request missing or changed: %+v calls %d stats %+v", request, calls, e.stats)
					}
					if q != nil && q.SetCheckpointObserver(nil) != nil {
						t.Fatal("typed observer retained")
					}
					if mode != 1 {
						continue
					}
					actor := modernActor(u)
					key, err := e.m.CheckpointApplicationKey(p.Def)
					if err != nil {
						t.Fatal(err)
					}
					intent := ai.ModernApplicationIntent{Kind: uint8(kind), ProductIndex: p.Index, ProductKey: p.Key, Count: -4, X: 64, Z: 64, Exact: true, RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor}, Product: &key}}
					if kind == CmdReplace {
						a := modernActor(old)
						intent.RawTarget = uint32(old.Handle)
						intent.Operands.Target = &a
					}
					count := uint32(1)
					terminal := uint8(3)
					if kind != CmdProduce {
						count += 2
						terminal = 4
					}
					if kind == CmdReplace {
						count += 3
					}
					if outcome != "nil without insert" {
						count += 3
						terminal = 2
						if outcome == "failure after insert" {
							terminal = 4
						}
					}
					v := newModernVector()
					v.append(t, 0, intent, 1, terminal, count, func(enc *checkpoint.Encoder) {
						if kind != CmdProduce {
							modernOp(enc, 1, actor)
							modernOp(enc, 2, actor)
						}
						index := 0
						if kind == CmdReplace {
							modernPreparation(enc, actor)
							reclaimed := *q.PrimaryAt(0)
							reclaimed.Flags |= orders.FlagActive // retained at insertion, before the build
							modernInsert(enc, actor, 1, 0, reclaimed)
							index++
						}
						if outcome != "nil without insert" {
							modernPreparation(enc, actor)
							modernInsert(enc, actor, 1, int64(index), *q.PrimaryAt(index))
						}
						verdict := uint8(1)
						if outcome == "failure after insert" {
							verdict = 5
						}
						modernBuild(enc, actor, request, verdict)
					})
					v.check(t, e)
				}
			})
		}
	}
}

func TestModernStockpileCompletionAndCappedCount(t *testing.T) {
	for _, outcome := range []string{"insert", "coalesce", "allocation refusal"} {
		t.Run(outcome, func(t *testing.T) {
			type result struct {
				nodes       [2][]orders.Node
				stats       ApplyStats
				ammo        int32
				state       uint32
				draws       uint64
				diagnostics int
			}
			var baseline result
			for mode := 0; mode < 3; mode++ {
				e, w, u, _ := modernApplicationFixture(t, mode)
				u.Slots[0].Weapon = &content.WeaponDef{ID: 1, Stockpile: true, ReloadTime: 300}
				u.Slots[0].Ammo = 190
				q := orders.QueueForUnit(u)
				id := orders.Lookup("BuildWeapon")
				switch outcome {
				case "coalesce":
					q.CoalesceTail(id, orders.Node{Owner: u.Handle, Param2: 2})
				case "allocation refusal":
					nodes := make([]*orders.Node, orders.OOMGuardQueue)
					for i := range nodes {
						nodes[i] = &orders.Node{}
					}
					q.SetSecondary(nodes)
				}
				prior := &modernPriorObserver{}
				q.SetCheckpointObserver(prior)
				modernApply(e, modernBatch([]Command{{Kind: CmdStockpile, count: 1, Count: 200}}, u), w, Persona{})
				got := result{modernNodes(q), e.stats, u.Slots[0].Ammo, e.m.RNG.State, e.m.RNG.Draws(), len(q.Diagnostics())}
				if mode == 0 {
					baseline = got
				} else if !reflect.DeepEqual(got, baseline) {
					t.Fatalf("mode %d changed stockpile behavior", mode)
				}
				if q.SetCheckpointObserver(nil) != prior {
					t.Fatal("stockpile observer not restored")
				}
				if e.stats.Applied != 1 {
					t.Fatal("diagnostics changed existing producer return")
				}
				if mode != 1 {
					continue
				}
				actor := modernActor(u)
				n := uint32(2)
				terminal := uint8(2)
				if outcome == "allocation refusal" {
					n = 1
					terminal = 3
				}
				v := newModernVector()
				v.append(t, 0, ai.ModernApplicationIntent{Kind: 14, Count: 200, RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor}}}, 1, terminal, n, func(enc *checkpoint.Encoder) {
					count := int64(10)
					switch outcome {
					case "insert":
						modernInsert(enc, actor, 2, 0, *q.Secondary()[0])
					case "coalesce":
						modernOp(enc, 10, actor)
						enc.U8(2)
						enc.I64(0)
						enc.U32(2)
						enc.U32(8)
						enc.Fail(q.Secondary()[0].WriteCheckpointValue(enc))
						count = 8
					}
					modernOp(enc, 4, actor)
					enc.U8(uint8(id))
					enc.I64(count)
					enc.Bool(outcome == "coalesce")
				})
				v.check(t, e)
			}
		})
	}
}

func TestModernCommandPanicRestoresScopeWithoutCompletion(t *testing.T) {
	for _, typed := range []bool{false, true} {
		e, w, u, p := modernApplicationFixture(t, 1)
		q := orders.QueueForUnit(u)
		priorObserver := &modernPriorObserver{}
		q.SetCheckpointObserver(priorObserver)
		priorCommand := &checkpointCommand{}
		e.checkpointApplication = priorCommand
		e.grids[2].checkpointObservation = checkpointGridObservation{slot: 3}
		priorGrid := e.grids[2].checkpointObservation
		payload := errors.New("fixture command panic")
		c := Command{Kind: CmdMove, count: 1}
		calls := 0
		if typed {
			c.Kind, c.Product = CmdProduce, p
			e.m.SetQueueBuildTyped(func(ai.BuildRequest) error { calls++; panic(payload) })
		} else {
			e.m.OrderBinding.Rules = modernBefore{before: func() { calls++; panic(payload) }}
		}
		var caught any
		func() {
			defer func() { caught = recover() }()
			modernApply(e, modernBatch([]Command{c}, u), w, Persona{})
		}()
		if caught != payload || calls != 1 {
			t.Fatalf("original panic changed: %v (%d calls)", caught, calls)
		}
		if e.checkpointApplication != priorCommand || e.grids[2].checkpointObservation != priorGrid || q.SetCheckpointObserver(nil) != priorObserver {
			t.Fatal("command panic lost enclosing diagnostic scopes")
		}
		h := e.m.CheckpointApplicationHistory()
		if h.ActiveAttempt() == nil {
			t.Fatal("panic fabricated a completed command")
		}
		if _, err := h.Snapshot(); err == nil {
			t.Fatal("panic produced usable history")
		}
	}
}

func TestModernOuterRefusalAfterNestedPreparationInsert(t *testing.T) {
	e, w, u, _ := modernApplicationFixture(t, 1)
	q := orders.QueueForUnit(u)
	nodes := make([]*orders.Node, orders.OOMGuardQueue-1)
	for i := range nodes {
		nodes[i] = &orders.Node{}
	}
	q.SetPrimary(nodes)
	e.m.OrderBinding.Rules = modernBefore{before: func() { q.PushHead(orders.Lookup("Stop"), orders.Node{Param1: 23}) }}
	modernApply(e, modernBatch([]Command{{Kind: CmdMove, count: 1, Queued: true}}, u), w, Persona{})
	if q.LenPrimary() != orders.OOMGuardQueue || len(q.Diagnostics()) != 1 || e.stats.Applied != 1 {
		t.Fatal("fixture did not preserve nested work and existing return semantics")
	}
	actor := modernActor(u)
	v := newModernVector()
	v.append(t, 0, ai.ModernApplicationIntent{Kind: 1, Queued: true, RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor}}}, 1, 4, 3, func(enc *checkpoint.Encoder) {
		modernPreparation(enc, actor)
		modernInsert(enc, actor, 1, 0, *q.Head())
	})
	v.check(t, e)
}

// Purge/drop cleanup may retire or mutate the observed actor. The journal
// keeps its original allocation while gameplay still reads the actual raw
// handle when constructing the later order and typed request.
func TestModernCleanupIdentityMutationKeepsObservedActor(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind CmdKind
	}{{"move", CmdMove}, {"build", CmdBuild}, {"replace", CmdReplace}} {
		for _, drop := range []bool{false, true} {
			name := tc.name + "/purge"
			if drop {
				name = tc.name + "/drop"
			}
			t.Run(name, func(t *testing.T) {
				type result struct {
					nodes          [2][]orders.Node
					stats          ApplyStats
					request        ai.BuildRequest
					cancels, calls int
					actor          checkpoint.Allocation
					state          uint32
					draws          uint64
				}
				var baseline result
				for mode := 0; mode < 3; mode++ {
					e, w, u, p := modernApplicationFixture(t, mode)
					actor := modernActor(u)
					g := NewRand(7, 0)
					e.mapInfo, e.m.Terrain = pocketMap(&g, 32, 32, 0)
					e.mapInfo.Spots = []MetalSpot{{X: 64, Z: 64}}
					extent, _ := world.NewFootprintExtent(1, 1)
					e.places = make([]*placeDef, int(p.Index)+1)
					e.places[p.Index] = &placeDef{ok: true, extent: extent, footX: 1, footZ: 1, mobile: true}
					oldHandle, err := w.Create(u.Def, 0, 16<<16, 0, 16<<16)
					if err != nil {
						t.Fatal(err)
					}
					old := w.Unit(oldHandle)
					q := orders.QueueForUnit(u)
					previous := &orders.Node{ID: orders.Lookup("MobileBuild"), Owner: u.Handle, DynamicGate: 2}
					if drop {
						previous.Flags = orders.FlagPurgeSurvivor | orders.FlagAutoOp
					}
					q.SetPrimary([]*orders.Node{previous})
					cancels, calls := 0, 0
					e.m.OrderBinding.SetLookup(w.Unit)
					e.m.OrderBinding.Work = orders.NewWorkAdapter(orders.WorkAdapterConfig{CancelNotice: func(*units.Unit, *orders.Node, uint32) bool {
						cancels++
						u.Handle += 100
						u.AllocationSerial += 1000
						e.m.RNG.Uint32n(7)
						return true
					}})
					var request ai.BuildRequest
					e.m.SetQueueBuildTyped(func(req ai.BuildRequest) error {
						calls++
						request = req
						q.CoalesceTail(orders.Lookup("MobileBuild"), orders.Node{Owner: req.Builder, BuildDefKey: req.UnitKey, Param2: uint32(req.Count), CreationTick: req.Tick})
						return nil
					})
					c := Command{Kind: tc.kind, count: 1, X: 64, Z: 64}
					if tc.kind != CmdMove {
						c.Product, c.Exact = p, true
					}
					if tc.kind == CmdReplace {
						c.Target, c.target = old.Handle, old
					}
					modernApply(e, modernBatch([]Command{c}, u), w, Persona{})
					got := result{modernNodes(q), e.stats, request, cancels, calls, modernActor(u), e.m.RNG.State, e.m.RNG.Draws()}
					if mode == 0 {
						baseline = got
					} else if !reflect.DeepEqual(got, baseline) {
						t.Fatalf("mode %d changed cleanup or gameplay arguments", mode)
					}
					if cancels != 1 || e.stats.Applied != 1 || q.Head().Owner != u.Handle {
						t.Fatalf("cleanup fixture did not issue through the changed raw handle: %+v", got)
					}
					if tc.kind != CmdMove && (calls != 1 || request.Builder != u.Handle || request.Tick != 0) {
						t.Fatalf("actual typed request was changed: %+v", request)
					}
					if mode != 1 {
						continue
					}
					intent := ai.ModernApplicationIntent{Kind: uint8(tc.kind), X: 64, Z: 64, RowNear: -17, Operands: ai.ApplicationOperands{Actors: []checkpoint.Allocation{actor}}}
					count := uint32(5)
					if tc.kind != CmdMove {
						key, err := e.m.CheckpointApplicationKey(p.Def)
						if err != nil {
							t.Fatal(err)
						}
						intent.ProductIndex, intent.ProductKey, intent.Exact, intent.Operands.Product = p.Index, p.Key, true, &key
						count++
					}
					if tc.kind == CmdReplace {
						target := modernActor(old)
						intent.RawTarget, intent.Operands.Target = uint32(old.Handle), &target
						count += 3
					}
					v := newModernVector()
					v.append(t, 0, intent, 1, 2, count, func(enc *checkpoint.Encoder) {
						modernOp(enc, 1, actor)
						modernOp(enc, 2, actor)
						first := *q.PrimaryAt(0)
						first.Flags |= orders.FlagActive
						modernPreparation(enc, actor)
						modernInsert(enc, actor, 1, 0, first)
						if tc.kind == CmdReplace {
							modernPreparation(enc, actor)
							modernInsert(enc, actor, 1, 1, *q.PrimaryAt(1))
						}
						if tc.kind != CmdMove {
							modernBuild(enc, actor, request, 1)
						}
					})
					v.check(t, e)
				}
			})
		}
	}
}
