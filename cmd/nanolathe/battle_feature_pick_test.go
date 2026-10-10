package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Authored geometry and frozen descent heights isolate the host picking policy
// from the retail sinking simulation. The footprint never moves [05 "Feature
// sinking and water interaction"], while its projected body does.
func submergedWreckBattle(t *testing.T, mode gameplay.Mode, height int32) (*battleSession, *units.Unit) {
	t.Helper()
	cat := testCatalogON05()
	def := &content.FeatureDef{Object: "wreck", Reclaimable: true, FootprintX: 2, FootprintZ: 2, Metal: 250, Energy: 75}
	def.CanonicalKey = "armcons_dead"
	cat.Features[def.CanonicalKey] = def
	terrain := testWorldON05(40, 40)
	terrain.SeaLevel = 128
	terrain.FeatureNames, terrain.FeatureDefs = []string{def.CanonicalKey}, []*content.FeatureDef{def}
	b := newTestBattle(cat, terrain)
	b.sess.SetGameplay(mode)
	b.sess.SeedSessionRNG(7, 11)
	b.sess.Features = features.NewService(terrain, nil, nil, nil)
	b.sess.Vis = visibility.New(terrain, 0)
	b.sess.Vis.SetLocal(0)
	if b.sess.Features.PlaceCorpse(world.Cell{X: 10, Z: 10}, [3]numeric.Fixed{176 << 16, numeric.Fixed(height) << 16, 176 << 16}, features.Orientation{}, def, true, 0) == nil {
		t.Fatal("place wreck")
	}
	actor := placeUnit(b, "armcons", 400<<16, 400<<16)
	actor.Def.CanReclamate = true
	replaceSelectionForTest(t, b, actor)
	b.sess.Clock.Paused = true
	b.sess.Econ = &economy.Service{}
	b.sess.Econ.Players[0].Stock = [2]float32{31, 47}

	// The quad has actual body geometry, unlike the root-bounds-only unit hull
	// fixture. It is ours, encoded through the production format writer.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "objects3d"), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Name: "root", Parent: -1, FirstChild: -1, NextSibling: -1, Selection: -1,
		Vertices:   []formats.ThreeDOVertex{{X: -8 << 16, Z: -8 << 16}, {X: -8 << 16, Z: 8 << 16}, {X: 8 << 16, Z: 8 << 16}, {X: 8 << 16, Z: -8 << 16}},
		Primitives: []formats.ThreeDOPrimitive{{IsColored: 1, ColorIndex: 9, VertexIndices: []uint16{0, 1, 2, 3}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "objects3d", "wreck.3do"), data, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close(); installTestHullModels() })
	b.cl.SetModelFS(fs)
	installTestHullModels()
	return b, actor
}

func wreckPointer(b *battleSession, height numeric.Fixed) (int32, int32) {
	z := int32(176 - (height.Floor() >> 1))
	zoom := b.cam.EffectiveZoom()
	return zoom.Project(176 - b.cam.X), zoom.Project(z - b.cam.Z)
}

func mapWreckFixture(f *frame.Frame) {
	f.Visibility.Valid, f.Visibility.W, f.Visibility.H = true, 20, 20
	f.Visibility.WordVisible = make([]uint16, 400)
	for i := range f.Visibility.WordVisible {
		f.Visibility.WordVisible[i] = 1
	}
}

func TestSubmergedWreckPickingIsModernAndRendererIndependent(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		for _, enhanced := range []bool{false, true} {
			for _, view := range []struct {
				scale camera.ViewScale
				zoom  camera.Zoom
			}{{camera.ViewScaleNative, 0}, {camera.ViewScaleDetail, 0}, {camera.ViewScaleDetail, camera.ZoomUnit * 13 / 10}} {
				for _, iface := range []int{settings.InterfaceTypeLeftClick, settings.InterfaceTypeRightClick} {
					t.Run(fmt.Sprintf("%s/enhanced=%v/scale=%d/zoom=%d/interface=%d", mode, enhanced, view.scale, view.zoom, iface), func(t *testing.T) {
						for _, surface := range []bool{false, true} {
							b, actor := submergedWreckBattle(t, mode, 0)
							b.cam.Scale, b.cam.Zoom, b.interfaceType = view.scale, view.zoom, iface
							b.cl.SetEnhanced(enhanced)
							f, _ := b.currentSnapshot()
							mapWreckFixture(f)
							beforeSim, beforeCRT := *b.sess.SimRNG(), *b.sess.CrtRNG()
							beforeStock := b.sess.Econ.Players[0].Stock
							height := numeric.Fixed(0)
							if surface {
								height = 128 << 16
							}
							sx, sy := wreckPointer(b, height)
							_, target, pos := b.pickTarget(sx, sy)
							want := surface || mode == gameplay.Modern
							if target != nil || pos.HasFeature != want || (b.hoverFeature(sx, sy) != nil) != want {
								t.Fatalf("surface=%v: target=%v feature=%v hover=%v, want feature %v", surface, target, pos.HasFeature, b.hoverFeature(sx, sy), want)
							}
							name, shape := "Move_Ground", render.CursorNormal
							if want {
								name, shape = "Reclaim", render.CursorReclamate
							}
							if got := orders.DescriptorFor(orders.Resolve(1, actor, nil, pos)).Name; got != name {
								t.Fatalf("contextual order=%s, want %s", got, name)
							}
							if got := b.cursorShapeAt(input.LatchReclaim, sx, sy); got != shape {
								t.Fatalf("armed shape=%s, want %s", render.CursorName(got), render.CursorName(shape))
							}
							if issued := b.orderSelected(12, sx, sy, true); issued != want {
								t.Fatalf("reclaim issued=%v, want %v", issued, want)
							}
							pending := b.sess.PendingHumanCommands()
							if want {
								if len(pending) != 1 || !pending[0].Order.Queued || pending[0].Order.Target != 0 {
									t.Fatalf("queued feature command: %+v", pending)
								}
								p := pending[0].Order.Position
								if world.WorldToCell(p.X) < 10 || world.WorldToCell(p.X) > 11 || world.WorldToCell(p.Z) < 10 || world.WorldToCell(p.Z) > 11 || p.Y != 128<<16 {
									t.Fatalf("command missed stamped footprint: %+v", p)
								}
								applyPendingBattleCommands(b)
								q := orders.QueueForUnit(actor)
								if q == nil || q.LenPrimary() == 0 {
									t.Fatal("paused command admission produced no reclaim record")
								}
								n := q.Primary()[q.LenPrimary()-1]
								if orders.DescriptorFor(n.ID).Name != "Reclaim" || n.GoalX != p.X || n.GoalZ != p.Z {
									t.Fatalf("ordinary queue did not retain the feature target: %+v", n)
								}
							} else if len(pending) != 0 {
								t.Fatalf("refused click queued %+v", pending)
							}
							if *b.sess.SimRNG() != beforeSim || *b.sess.CrtRNG() != beforeCRT || b.sess.Econ.Players[0].Stock != beforeStock {
								t.Fatal("picking or paused admission changed RNG/resources")
							}
						}
					})
				}
			}
		}
	}
}

func TestSubmergedWreckPickingBoundariesAndOrdinaryPayout(t *testing.T) {
	b, _ := submergedWreckBattle(t, gameplay.Modern, 64)
	f, _ := b.currentSnapshot()
	mapWreckFixture(f)
	sx, sy := wreckPointer(b, 64<<16)
	_, _, pos := b.pickTargetFor(input.LatchReclaim, sx, sy)
	if !pos.HasFeature {
		t.Fatal("mid-descent body not picked")
	}
	_, _, attack := b.pickTargetFor(input.LatchAttack, sx, sy)
	wx, wy, wz := b.cursorWorld(sx, sy)
	if attack.X != wx || attack.Y != wy || attack.Z != wz {
		t.Fatal("explicit ground attack was redirected")
	}

	// Unit priority must survive even when projected model faces overlap.
	f.Units = append(f.Units, frame.UnitView{Slot: 63, DefName: "armsolar", Model: "armsolar", Owner: 0, X: 176 << 16, Y: 64 << 16, Z: 176 << 16})
	_, target, unitPos := b.pickTarget(sx, sy)
	if target == nil || target.Handle != 63 || unitPos.HasFeature || unitPos.X != wx || unitPos.Z != wz {
		t.Fatal("wreck replaced the directly hovered unit")
	}
	f.Units = f.Units[:len(f.Units)-1]

	// Feature display visibility and explored footprint memory are independent
	// gates: an ownership bypass cannot expose an unexplored command target.
	f.Visibility.WordVisible = make([]uint16, 400)
	if b.hoverFeature(sx, sy) != nil {
		t.Fatal("unmapped footprint admitted")
	}
	mapWreckFixture(f)
	f.Features[0].OwnerKnown = false
	f.Visibility.CoverageBytes, f.Visibility.Visible = true, make([]uint8, 400)
	if b.hoverFeature(sx, sy) != nil {
		t.Fatal("hidden body admitted")
	}
	f.Features[0].OwnerKnown, f.Visibility.CoverageBytes = true, false
	for _, omit := range []string{"sprite", "scenery", "unreclaimable", "missing model", "waterline"} {
		t.Run(omit, func(t *testing.T) {
			saved := f.Features[0]
			def := b.cat.Features["armcons_dead"]
			object := def.Object
			switch omit {
			case "sprite":
				def.Object = ""
			case "scenery":
				f.Features[0].DefName = "armrock"
			case "unreclaimable":
				f.Features[0].Reclaimable = false
			case "missing model":
				f.Features[0].Model = "missing"
			case "waterline":
				f.Features[0].Y = 128 << 16
			}
			if b.hoverFeature(sx, sy) != nil {
				t.Fatal("out-of-scope feature admitted")
			}
			f.Features[0], def.Object = saved, object
		})
	}
	if !b.orderSelected(12, sx, sy, false) {
		t.Fatal("mid-descent reclaim refused")
	}
	command := b.sess.PendingHumanCommands()[0].Order
	metal, energy, ok := b.sess.Features.ReclaimAt(int(world.WorldToCell(command.Position.X)), int(world.WorldToCell(command.Position.Z)))
	if !ok || metal != 250 || energy != 75 {
		t.Fatalf("ordinary feature payout=%v/%v/%v", metal, energy, ok)
	}
	if _, _, ok := b.sess.Features.ReclaimAt(int(world.WorldToCell(command.Position.X)), int(world.WorldToCell(command.Position.Z))); ok {
		t.Fatal("feature paid twice")
	}
}

func TestSubmergedWreckOverlapKeepsCommittedOrderAndViewportBoundary(t *testing.T) {
	b, _ := submergedWreckBattle(t, gameplay.Modern, 0)
	f, _ := b.currentSnapshot()
	mapWreckFixture(f)
	sx, sy := wreckPointer(b, 0)
	wx, _, wz := b.cursorWorld(sx, sy)
	first := f.Features[0]
	second := first
	second.InstanceID, second.CX = first.InstanceID+1, 15
	ground := frame.FeatureView{InstanceID: 99, DefName: "armrock", Reclaimable: true, CX: world.WorldToCell(wx), CZ: world.WorldToCell(wz), FootX: 1, FootZ: 1}
	f.Features = []frame.FeatureView{ground, first, second}
	_, _, pos := b.pickTargetFor(input.LatchReclaim, sx, sy)
	if !pos.HasFeature || pos.X != first.X || pos.Z != first.Z {
		t.Fatal("ground feature displaced the first projected wreck")
	}
	f.Features = []frame.FeatureView{ground, second, first}
	_, _, pos = b.pickTargetFor(input.LatchReclaim, sx, sy)
	centerX, centerZ := world.PlacementCenter(second.CX, second.CZ, 2, 2)
	if pos.X != centerX || pos.Z != centerZ {
		t.Fatal("model overlap ignored committed feature order")
	}

	// Sliding the body under the HUD cannot open a model targeting path.
	f.Features = []frame.FeatureView{first}
	b.cam.X = 100
	sx, sy = wreckPointer(b, 0)
	_, _, pos = b.pickTarget(sx, sy)
	if pos.HasFeature || b.hoverFeature(sx, sy) != nil {
		t.Fatal("HUD body admitted")
	}
}

func TestSubmergedWreckContextualWorkPreservesOtherActorsOrders(t *testing.T) {
	b, worker := submergedWreckBattle(t, gameplay.Modern, 0)
	// Publish a real mover alongside the worker before selecting the mixed
	// group. Its prior movement must survive the new visual work gesture.
	stock := b.sess.Econ
	b.sess.Econ, b.sess.Clock.Paused = nil, false
	mover := placeUnit(b, "armfav", 320<<16, 320<<16)
	mover.Def.CanMove = true
	replaceSelectionForTest(t, b, worker, mover)
	b.sess.Econ, b.sess.Clock.Paused = stock, true
	q := orders.QueueForUnit(mover)
	prior := orders.NewMoveNode(orders.Lookup("Move_Ground"), 350<<16, 360<<16, b.sess.Clock.GlobalTick, mover.Handle, false)
	q.Push(prior.ID, prior)
	f, _ := b.currentSnapshot()
	mapWreckFixture(f)
	sx, sy := wreckPointer(b, 0)
	beforeSim, beforeCRT := *b.sess.SimRNG(), *b.sess.CrtRNG()
	beforeStock := b.sess.Econ.Players[0].Stock
	_, _, move := b.pickTargetFor(input.LatchMove, sx, sy)
	wx, wy, wz := b.cursorWorld(sx, sy)
	if move.X != wx || move.Y != wy || move.Z != wz {
		t.Fatal("explicit MOVE redirected")
	}
	if !b.orderSelected(1, sx, sy, false) {
		t.Fatal("contextual reclaim refused")
	}
	command := b.sess.PendingHumanCommands()[0].Order
	if command.Code != 1 || len(command.Handles) != 1 || command.Handles[0] != worker.Handle || command.Position.X != 176<<16 || command.Position.Z != 176<<16 {
		t.Fatalf("contextual work command: %+v", command)
	}
	applyPendingBattleCommands(b)
	if q.LenPrimary() != 1 || q.Primary()[0].GoalX != prior.GoalX || q.Primary()[0].GoalZ != prior.GoalZ {
		t.Fatal("non-worker's movement changed")
	}
	workerQueue := orders.QueueForUnit(worker)
	if workerQueue.LenPrimary() != 1 || orders.DescriptorFor(workerQueue.Primary()[0].ID).Name != "Reclaim" {
		t.Fatal("contextual worker did not reclaim")
	}
	if *b.sess.SimRNG() != beforeSim || *b.sess.CrtRNG() != beforeCRT || b.sess.Econ.Players[0].Stock != beforeStock {
		t.Fatal("contextual work admission changed RNG/resources")
	}
	// Contextual resurrection has its own gate. Keep code 1: changing it to
	// armed code 12 would reject this authored resurrection-only worker.
	worker.Def.CanReclamate, worker.Def.CanResurrect = false, true
	f, _ = b.currentSnapshot()
	mapWreckFixture(f)
	if !b.orderSelected(1, sx, sy, false) {
		t.Fatal("contextual resurrection refused")
	}
	command = b.sess.PendingHumanCommands()[0].Order
	if command.Code != 1 || len(command.Handles) != 1 || command.Handles[0] != worker.Handle {
		t.Fatalf("resurrection actor capture: %+v", command)
	}
	applyPendingBattleCommands(b)
	if workerQueue.LenPrimary() != 1 || orders.DescriptorFor(workerQueue.Primary()[0].ID).Name != "Resurrect" {
		t.Fatal("contextual resurrection became armed reclaim")
	}

	// With only a mover, the same body click remains an ordinary contextual
	// move at the pointer's raw water point, without actor filtering.
	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{mover.Handle}}}); err != nil {
		t.Fatal(err)
	}
	f, _ = b.currentSnapshot()
	mapWreckFixture(f)
	if !b.orderSelected(1, sx, sy, false) {
		t.Fatal("pure-mover click refused")
	}
	command = b.sess.PendingHumanCommands()[0].Order
	if len(command.Handles) != 1 || command.Handles[0] != mover.Handle || command.Position.X != wx || command.Position.Y != wy || command.Position.Z != wz {
		t.Fatalf("pure mover redirected: %+v", command)
	}
}

func TestSubmergedWreckAlreadyInPickedCellKeepsOrdinaryPoint(t *testing.T) {
	b, _ := submergedWreckBattle(t, gameplay.Modern, 127)
	f, _ := b.currentSnapshot()
	mapWreckFixture(f)
	sx, sy := wreckPointer(b, 127<<16)
	wx, wy, wz := b.cursorWorld(sx, sy)
	_, _, pos := b.pickTargetFor(input.LatchReclaim, sx, sy)
	if !pos.HasFeature || pos.X != wx || pos.Y != wy || pos.Z != wz {
		t.Fatal("already-valid feature point was changed")
	}
}
