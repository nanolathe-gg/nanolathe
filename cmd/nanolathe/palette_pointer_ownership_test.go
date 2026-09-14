package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func paletteOwnershipPointer(b *battleSession, cl *client.Client, kind input.PointerEventKind, x, y int32, held bool) {
	e := input.PointerEvent{Kind: kind, X: x, Y: y, Buttons: input.MouseButtons{Right: held}}
	cl.Input().UpdatePointerMotion(e)
	if kind != 0 {
		cl.Input().EnqueuePointer(e)
	}
	cl.Input().PublishPointer()
	b.viewerStep(0, cl)
}

// The GUI fetch consumes a down inside its window before any gadget hit test.
// Only an unconsumed down reaches battlefield cancellation [07 §3][07 §9].
func TestPaletteWindowOwnsRightDownBeforeCancellation(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		for _, paused := range []bool{false, true} {
			for _, placement := range []bool{false, true} {
				for _, point := range []struct {
					name  string
					x, y  int32
					owned bool
				}{
					{"button", 5, 5, true},
					{"grey button", 28, 5, true},
					{"hidden button", 52, 5, true},
					{"blank", 60, 40, true},
					{"last pixel", 127, 79, true},
					{"outside", 128, 79, false},
				} {
					t.Run(fmt.Sprintf("type1=%v/paused=%v/placement=%v/%s", alternate, paused, placement, point.name), func(t *testing.T) {
						b, cl, _ := paletteViewer(t, []gui.Gadget{
							{Kind: gui.KindButton, Name: "MOVE", Active: 1, Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
							{Kind: gui.KindButton, Name: "STOP", Active: 1, GrayedOut: 1, Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
							{Kind: gui.KindButton, Name: "ATTACK", Rect: gui.Rect{X: 48, Y: 1, W: 20, H: 12}},
						})
						if alternate {
							b.applyInterfaceTypeSetting(settings.Settings{InterfaceType: settings.InterfaceTypeRightClick})
						}
						b.applyBattleSchedule(ui.PauseIntent(paused))
						// An unheld position update avoids the held-outside freeze of
						// the generic GUI service [07 R-WGT-01 §1].
						paletteOwnershipPointer(b, cl, 0, point.x, point.y, false)
						if placement {
							def, _ := b.cat.Unit("armsolar")
							b.armPlacement(def)
						} else {
							b.battleState().Input.Latch = input.LatchAttack
						}
						want := b.battleState().Input.Latch
						before := len(b.sess.PendingHumanCommands())
						paletteOwnershipPointer(b, cl, input.RightDown, point.x, point.y, true)
						if !point.owned {
							want = input.LatchNormal
						}
						if got := b.battleState().Input.Latch; got != want {
							t.Fatalf("right down latch=%v, want %v", got, want)
						}
						if placement && (b.battleState().Input.BuildDef != "") != point.owned {
							t.Fatal("placement definition did not follow window ownership")
						}
						paletteOwnershipPointer(b, cl, input.RightUp, point.x, point.y, false)
						if b.battleState().Input.Latch != want || len(b.sess.PendingHumanCommands()) != before {
							t.Fatal("right release changed latch or emitted a world command")
						}
					})
				}
			}
		}
	}
}

func TestPaletteOwnedRightReleaseStillSubtractsFactoryProduct(t *testing.T) {
	b, cl, _ := paletteCallbackFactory(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ARMFAV", Active: 1, Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}}, []string{"armfav"}, 0, 2)
	b.battleState().Input.Latch = input.LatchAttack
	paletteOwnershipPointer(b, cl, input.RightDown, 5, 5, true)
	if len(paletteFactoryCommands(b)) != 0 {
		t.Fatal("factory subtraction fired before release")
	}
	paletteOwnershipPointer(b, cl, input.RightUp, 5, 5, false)
	commands := paletteFactoryCommands(b)
	if len(commands) != 1 || commands[0].FactoryBuild.Count != -1 || b.battleState().Input.Latch != input.LatchAttack {
		t.Fatalf("factory right release commands=%v latch=%v", commands, b.battleState().Input.Latch)
	}
}
