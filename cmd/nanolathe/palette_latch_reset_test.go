package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// The shared idle transition releases STOP's authored group, including STOP,
// while Shift-queued orders retain it until Shift goes up [07 R-HUD-04 §3].
func TestPaletteOrderButtonsReleaseWithLatch(t *testing.T) {
	for _, action := range []string{"world order", "right cancel", "escape", "stop", "shift queue"} {
		t.Run(action, func(t *testing.T) {
			b, cl, window := paletteViewer(t, []gui.Gadget{
				{Kind: gui.KindButton, Name: "ARMATTACK", Active: 1, Attribs: 0x10, Assoc: 1, QuickKey: 'A', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
				{Kind: gui.KindButton, Name: "ARMSTOP", Active: 1, Attribs: 0x10, Assoc: 1, QuickKey: 'S', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
				{Kind: gui.KindButton, Name: "UNRELATED", Active: 1, Assoc: 2, Status: 2},
			})
			in := cl.Input()
			in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
			b.viewerStep(0, cl)
			panel := b.hud.palettePanels[window]
			if b.battleState().Input.Latch != input.LatchAttack || panel.DownAt(1) != 1 {
				t.Fatal("attack did not arm its latch and radio button")
			}
			in.Mouse.SetPosition(220, 160)
			switch action {
			case "world order", "shift queue":
				in.Kbd.SetKey(input.KeyShift, action == "shift queue")
				in.Mouse.SetButton(input.MouseButtonLeft, true)
				b.viewerStep(0, cl)
				in.Mouse.SetButton(input.MouseButtonLeft, false)
				b.viewerStep(0, cl)
				if action == "shift queue" {
					if b.battleState().Input.Latch != input.LatchAttack || panel.DownAt(1) != 1 {
						t.Fatal("Shift-queued order released its latch or button")
					}
					in.Kbd.SetKey(input.KeyShift, false)
					b.viewerStep(0, cl)
				}
			case "right cancel":
				in.Mouse.SetButton(input.MouseButtonRight, true)
				b.viewerStep(0, cl)
			case "escape":
				in.Kbd.SetKey(input.KeyEscape, true)
				b.viewerStep(0, cl)
			case "stop":
				in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 's'})
				b.viewerStep(0, cl)
			}
			if b.battleState().Input.Latch != input.LatchNormal || panel.DownAt(1) != 0 || panel.DownAt(2) != 0 {
				t.Fatalf("idle latch/buttons = %v/%d/%d", b.battleState().Input.Latch, panel.DownAt(1), panel.DownAt(2))
			}
			if panel.DownAt(3) != 2 {
				t.Fatal("idle reset changed an unrelated association")
			}
		})
	}
}

// Stock art follows the same retained radio state as the command dispatcher.
// Optional captures use the production HUD, without storing retail fixtures.
func TestRetailOrderButtonReleasedPaint(t *testing.T) {
	_, b, cl := retailBattleOptionsShell(t)
	b.millisSource = &fakeMillisSource{}
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Owner == b.sess.LocalOwner {
			actor := placeUnit(b, "armflash", u.X, u.Z)
			replaceSelectionForTest(t, b, actor)
			break
		}
	}
	b.viewerStep(0, cl)
	ctx, ok := b.hud.paletteContext(b)
	if !ok {
		t.Fatal("stock command page did not open")
	}
	p := b.hud.palettePanel(ctx.window)
	attack := -1
	for i, gad := range ctx.window.Gadgets {
		if commandButtonName(gad.Name) == "ATTACK" {
			attack = i
		}
	}
	if attack < 0 {
		t.Fatal("stock command page has no Attack")
	}
	step := func(kind input.PointerEventKind, x, y int32, buttons input.MouseButtons) {
		event := input.PointerEvent{Kind: kind, X: x, Y: y, Buttons: buttons}
		if kind == 0 {
			cl.Input().UpdatePointerMotion(event)
		} else {
			cl.Input().EnqueuePointer(event)
		}
		cl.Input().PublishPointer()
		b.viewerStep(0, cl)
	}
	r := ctx.window.PlacedRect(attack)
	step(input.LeftDown, r.X+r.W/2, r.Y+r.H/2, input.MouseButtons{Left: true})
	step(input.LeftUp, r.X+r.W/2, r.Y+r.H/2, input.MouseButtons{})
	if b.battleState().Input.Latch != input.LatchAttack || p.DownAt(attack) != 1 {
		t.Fatal("stock Attack button did not stay down while armed")
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, dir+"/hud-attack-armed.png")
	}
	// The unheld move leaves the palette before the world click. Held
	// samples outside its window retain its last pointer [07 R-WGT-01 §1].
	step(0, 220, 160, input.MouseButtons{})
	step(input.RightDown, 220, 160, input.MouseButtons{Right: true})
	if p.DownAt(attack) != 0 {
		t.Fatalf("stock Attack stayed down: latch=%v capture=%d", b.battleState().Input.Latch, p.CaptureIndex())
	}
	if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
		writeShellShot(t, cl, dir+"/hud-attack-idle.png")
	}
}
