package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// A paused phase-one drain preserves the applied orders for inspection without
// the work pump consuming the synthetic fixture's incomplete movement service.
// Each click still crosses the real pointer, command, and session boundaries
// [07 R-P0-11 §4][07 R-P0-11 §6].
func TestShiftClickReclaimQueuesFourDistinctFeatures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		enhanced      bool
		interfaceType int
	}{
		{name: "classic type zero", interfaceType: settings.InterfaceTypeLeftClick},
		{name: "classic type one", interfaceType: settings.InterfaceTypeRightClick},
		{name: "enhanced type zero", enhanced: true, interfaceType: settings.InterfaceTypeLeftClick},
		{name: "enhanced type one", enhanced: true, interfaceType: settings.InterfaceTypeRightClick},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat := testCatalogON05()
			terrain := testWorldON05(40, 40)
			def := cat.Features[content.CanonicalKey("armrock")]
			def.Metal = 100
			terrain.FeatureNames = []string{"armrock"}
			terrain.FeatureDefs = []*content.FeatureDef{def}
			b := newTestBattle(cat, terrain)
			b.interfaceType = tc.interfaceType
			b.cl.SetEnhanced(tc.enhanced)
			b.cl.SetFocused(true)
			b.sess.Features = features.NewService(terrain, nil, nil, nil)
			b.sess.Vis = visibility.New(terrain, 0)
			b.sess.Vis.SetLocal(0)
			actor := placeUnit(b, "armcons", 120<<16, 100<<16)
			actor.Def.CanReclamate = true
			var points [4][2]int32
			for i, cell := range []int32{14, 18, 22, 26} {
				if b.sess.Features.PlaceAt(int(cell), 12, def) == nil {
					t.Fatalf("place feature at cell %d", cell)
				}
				wx, wz := numeric.Fixed(int64(cell*16)<<16), numeric.Fixed(12*16<<16)
				sx, sy := b.cam.WorldToScreen(wx, 0, wz)
				points[i] = [2]int32{sx - camera.OriginX, sy - camera.OriginY}
			}
			replaceSelectionForTest(t, b, actor)
			b.sess.Clock.Paused = true
			b.battleState().SetLatch(input.LatchReclaim)

			in := input.NewState()
			var goals [4]orders.ResolvePos
			in.Kbd.SetKey(input.KeyShift, true)
			in.Kbd.ResetEdges() // Shift was held before the first click.
			for i, p := range points {
				for _, event := range []input.PointerEvent{
					{Kind: input.LeftDown, X: p[0], Y: p[1], Buttons: input.MouseButtons{Left: true}, Modifiers: input.Modifiers{Shift: true}},
					{X: p[0], Y: p[1], Buttons: input.MouseButtons{Left: true}, Modifiers: input.Modifiers{Shift: true}},
					{Kind: input.LeftUp, X: p[0], Y: p[1], Modifiers: input.Modifiers{Shift: true}},
				} {
					// The platform classifies press identity before this
					// layer. Reclaim must accept either press kind.
					if i == 1 && event.Kind == input.LeftDown {
						event.Kind = input.LeftDoubleClick
					}
					if event.Kind == input.PointerEventNone {
						in.UpdatePointerMotion(event)
					} else if !in.EnqueuePointer(event) {
						t.Fatal("pointer record was refused")
					}
					if !in.PublishPointer() {
						t.Fatal("pointer record was not published")
					}
					b.handleInput(in, b.cl)
				}
				pending := b.sess.PendingHumanCommands()
				if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 12 || !pending[0].Order.Queued || !pending[0].Order.Position.HasFeature {
					t.Fatalf("click %d dispatched %+v, want one queued feature reclaim", i+1, pending)
				}
				goals[i] = pending[0].Order.Position
				applyPendingBattleCommands(b)
				q := orders.QueueForUnit(actor)
				if q == nil {
					t.Fatalf("click %d left no order queue", i+1)
				}
				if q.LenPrimary() != i+1 {
					t.Fatalf("click %d left %d primary orders, want %d", i+1, q.LenPrimary(), i+1)
				}
				for j, n := range q.Primary() {
					if n.ID != orders.Lookup("Reclaim") || n.GoalX != goals[j].X || n.GoalZ != goals[j].Z {
						t.Fatalf("click %d queue row %d = %q at (%v,%v), want Reclaim at (%v,%v)", i+1, j, orders.DescriptorFor(n.ID).Name, n.GoalX, n.GoalZ, goals[j].X, goals[j].Z)
					}
				}
				if state := b.battleState().Input; state.Latch != input.LatchReclaim || !state.ShiftLatchSticky {
					t.Fatalf("click %d retired reclaim latch: %+v", i+1, state)
				}
			}
			in.Kbd.SetKey(input.KeyShift, false)
			in.UpdatePointerMotion(input.PointerEvent{X: points[3][0], Y: points[3][1]})
			in.PublishPointer()
			b.handleInput(in, b.cl)
			if state := b.battleState().Input; state.Latch != input.LatchNormal || state.ShiftLatchSticky {
				t.Fatalf("Shift release did not retire reclaim latch: %+v", state)
			}
		})
	}
}
