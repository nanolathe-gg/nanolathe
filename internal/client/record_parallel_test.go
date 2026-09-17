package client

import (
	"hash/fnv"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The two-stage unit record must produce byte-identical commands whatever the
// worker count, because stage two reads indexed slots in bucket order and never
// in completion order (docs/DESIGN_GPU_RENDERER.md §13.9)[03 R-RAST-01 §7][I1].
//
// The scene deliberately mixes the branches unitGeometryPair takes: a plain
// mobile, a structure under construction (nanoframe reveal, forced key plane,
// outline), a carrier with an attached child (staged children and the forced
// child key plane), and a Digger (key bias and clip). Three frames are recorded
// per configuration so the retained cached lane is exercised as well as the
// first build.

func recordParallelScene(t *testing.T) (*Client, *frame.Frame) {
	t.Helper()
	c := newPieceFixtureClient(t)
	flatModel(c, "m_mobile", 60, 40)
	flatModel(c, "m_frame", 80, 55)
	flatModel(c, "m_carrier", 70, 33)
	flatModel(c, "m_cargo", 24, 77)
	flatModel(c, "m_digger", 50, 21)
	// A battle registry resolves every model before the first frame, which is
	// the condition stage one requires: nothing on the per-unit path may load.
	r := newModelTextureRegistry(nil, false)
	for _, name := range []string{"m_mobile", "m_frame", "m_carrier", "m_cargo", "m_digger"} {
		r.loads[modelTextureLoadKey{kind: modelLoadUnit, id: ckey(name)}] = c.models[name]
	}
	c.modelTextures = r

	// Five archetypes repeated across the window: enough units to clear
	// parallelUnitFloor, so the twelve-participant configuration really does
	// hand work to the workers instead of draining it on one goroutine.
	var units []frame.UnitView
	slot := pool.Handle(1)
	next := func() pool.Handle { s := slot; slot++; return s }
	for group := int32(0); group < 6; group++ {
		x, z := px(160+40*group), px(160+16*group)
		mobile := next()
		units = append(units, frame.UnitView{Slot: mobile, InstanceID: uint64(mobile) + 100, Owner: 0,
			X: x, Z: z, MoverMode: 1, Model: "m_mobile", DefName: "m_mobile", BMCode: true,
			CacheRevision: 1, CacheValidityRevision: 1})
		nano := next()
		units = append(units, frame.UnitView{Slot: nano, InstanceID: uint64(nano) + 100, Owner: 0,
			X: x + px(12), Z: z + px(16), MoverMode: 1, Model: "m_frame", DefName: "m_frame",
			BuildRemaining: 0.5, ZBuffer: true, CacheRevision: 1, CacheValidityRevision: 1})
		carrier, cargo := next(), next()
		units = append(units, frame.UnitView{Slot: carrier, InstanceID: uint64(carrier) + 100, Owner: 0,
			X: x + px(24), Z: z + px(32), MoverMode: 1, Model: "m_carrier", DefName: "m_carrier",
			BMCode: true, ZBuffer: true, CacheRevision: 1, CacheValidityRevision: 1,
			Cargo: []pool.Handle{cargo}})
		units = append(units, frame.UnitView{Slot: cargo, InstanceID: uint64(cargo) + 100, Owner: 0,
			X: x + px(26), Y: px(12), Z: z + px(32), MoverMode: 0, Model: "m_cargo", DefName: "m_cargo",
			BMCode: true, Carrier: carrier, CarriedPiece: 0,
			CacheRevision: 1, CacheValidityRevision: 1})
		digger := next()
		units = append(units, frame.UnitView{Slot: digger, InstanceID: uint64(digger) + 100, Owner: 0,
			X: x + px(36), Z: z + px(48), MoverMode: 1, Model: "m_digger", DefName: "m_digger",
			Digger: true, CacheRevision: 1, CacheValidityRevision: 1})
	}

	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Units:     units,
	}
	return c, cur
}

// modelDigest folds every field of every recorded Model command into one hash,
// in record order. Texture and reveal references are folded by identity: they
// address immutable loaded art, so equal pointers are equal pixels.
func modelDigest(t *testing.T, list *drawlist.List) uint64 {
	t.Helper()
	h := fnv.New64a()
	var b [8]byte
	put := func(v uint64) {
		for i := 0; i < 8; i++ {
			b[i] = byte(v >> (8 * i))
		}
		h.Write(b[:])
	}
	ptr := func(v any) uint64 {
		rv := reflect.ValueOf(v)
		if !rv.IsValid() || rv.IsNil() {
			return 0
		}
		return uint64(rv.Pointer())
	}
	var geometry func(g *drawlist.ModelGeometry, depth int)
	faces := func(fs []drawlist.ModelFace) {
		put(uint64(len(fs)))
		for i := range fs {
			f := &fs[i]
			put(uint64(f.Color))
			put(uint64(ptr(f.Texture)))
			if f.Shaded {
				put(1)
			} else {
				put(0)
			}
			put(uint64(len(f.Vertices)))
			for _, v := range f.Vertices {
				put(uint64(uint32(v.X)))
				put(uint64(uint32(v.Y)))
				put(uint64(uint32(v.Key)))
				put(uint64(uint32(v.U)))
				put(uint64(uint32(v.V)))
				put(uint64(v.Shade))
			}
		}
	}
	geometry = func(g *drawlist.ModelGeometry, depth int) {
		if g == nil || depth > 4 {
			put(0)
			return
		}
		put(1)
		put(uint64(uint32(g.Width)))
		put(uint64(uint32(g.Height)))
		put(uint64(uint32(g.OriginX)))
		put(uint64(uint32(g.OriginY)))
		put(uint64(uint32(g.AnchorX)))
		put(uint64(uint32(g.AnchorY)))
		put(uint64(uint32(g.Scale)))
		put(uint64(g.Fallback))
		put(uint64(g.Waterline))
		put(uint64(g.WaterlineKey))
		put(uint64(g.DiggerKey))
		for _, flag := range []bool{g.Eligible, g.KeyPlane, g.Digger} {
			if flag {
				put(1)
			} else {
				put(0)
			}
		}
		faces(g.Faces)
		faces(g.LiveFaces)
		faces(g.Outline)
		if g.Reveal != nil {
			put(uint64(uint32(g.Reveal.Line)))
			put(uint64(uint32(g.Reveal.Floor)))
			put(uint64(g.Reveal.Below))
			put(uint64(g.Reveal.Band))
			put(uint64(g.Reveal.Above))
		} else {
			put(0)
		}
		geometry(g.Shadow, depth+1)
		geometry(g.Supersample, depth+1)
		put(uint64(len(g.Children)))
		for _, child := range g.Children {
			put(uint64(uint32(child.KeyDelta)))
			geometry(child.Geometry, depth+1)
		}
	}
	for _, m := range list.ModelCommands() {
		if m.ShadowOnly {
			put(2)
		} else {
			put(3)
		}
		geometry(m.Geometry, 0)
	}
	return h.Sum64()
}

// recordParallelFrames records n frames of the scene through the two-stage
// path with the given participant count and returns the per-frame digests.
func recordParallelFrames(t *testing.T, workers, frames int) []uint64 {
	t.Helper()
	c, cur := recordParallelScene(t)
	c.geometryOnlyModels = true
	c.recordModelGeometry = true
	c.parallelRecord = true
	c.recordPool = newRecordPool(workers)
	defer c.recordPool.close()

	out := make([]uint64, 0, frames)
	for i := 0; i < frames; i++ {
		c.list.Reset()
		c.modelScratch.reset()
		c.modelScratch.active = true
		c.selectionChrome = c.selectionChrome[:0]
		c.pruneCachedModelBodies(cur)
		c.drawWorldPass(cur, true)
		c.drawWorldPassB(cur, true)
		c.modelScratch.active = false
		if len(c.unitJobs) < parallelUnitFloor {
			t.Fatalf("frame %d prepared %d stage-one jobs, below the %d-job floor: the scene never reaches the workers",
				i, len(c.unitJobs), parallelUnitFloor)
		}
		out = append(out, modelDigest(t, &c.list))
	}
	return out
}

func TestParallelUnitRecordIsWorkerCountIndependent(t *testing.T) {
	const frames = 3
	one := recordParallelFrames(t, 1, frames)
	many := recordParallelFrames(t, 12, frames)
	if len(one) != frames || len(many) != frames {
		t.Fatalf("digest counts %d and %d, want %d", len(one), len(many), frames)
	}
	for i := range one {
		if one[i] != many[i] {
			t.Fatalf("frame %d: single-participant digest %016x, twelve-participant digest %016x", i, one[i], many[i])
		}
	}
	// A scene that records nothing would pass the comparison vacuously.
	c, cur := recordParallelScene(t)
	c.geometryOnlyModels, c.recordModelGeometry = true, true
	c.list.Reset()
	c.modelScratch.reset()
	c.modelScratch.active = true
	c.drawWorldPass(cur, true)
	c.drawWorldPassB(cur, true)
	c.modelScratch.active = false
	if n := len(c.list.ModelCommands()); n < 4 {
		t.Fatalf("scene recorded %d model commands, want the mobile, nanoframe, carrier and Digger", n)
	}
	if got := modelDigest(t, &c.list); got != one[0] {
		t.Fatalf("sequential digest %016x, two-stage digest %016x", got, one[0])
	}
}
