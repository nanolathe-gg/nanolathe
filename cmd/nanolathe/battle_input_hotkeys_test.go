package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// pressKeys drives one keyboard edge through the battle dispatcher, with any
// modifiers held for the same frame — the way retail makes its held-key queries
// at dispatch time [07 §2][07 R-CAM-01 §2].
func pressKeys(b *battleSession, keys ...input.Key) {
	applyPendingBattleCommands(b)
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	for _, k := range keys {
		in.Kbd.SetKey(k, true)
	}
	b.handleInput(in, nil)
	applyPendingBattleCommands(b)
}

// hotkeyCatalog is testCatalogON05 with authored category tokens and a
// compiled category registry, so the Ctrl+letter selects resolve through
// catalog data rather than a hand-written unit list [R-P0-03].
func hotkeyCatalog(t *testing.T) *content.Catalog {
	t.Helper()
	cat := testCatalogON05()
	set := func(name, category string) {
		def, ok := cat.Unit(name)
		if !ok || def == nil {
			t.Fatalf("fixture catalog has no %q", name)
		}
		def.Category = category
	}
	set("armcons", "CTRL_C COMMANDER")
	// The publisher takes the page count from the definition's compiled
	// page-count byte, not from the catalog's button list [07 R-HUD-03 §6].
	// The fixture's nine armcons buttons are page 0 (orders) plus two build
	// pages.
	if def, ok := cat.Unit("armcons"); ok {
		def.BuildPageCount = 3
	}
	set("armsolar", "CTRL_B")
	set("armfav", "CTRL_V")
	reg, err := content.CompileCategories(cat.Units)
	if err != nil {
		t.Fatalf("compile categories: %v", err)
	}
	cat.Categories = reg
	return cat
}

func selectedHandles(t *testing.T, b *battleSession) []pool.Handle {
	t.Helper()
	f, ok := b.currentSnapshot()
	if !ok {
		t.Fatalf("no committed frame")
	}
	out := append([]pool.Handle(nil), f.Selection.Handles...)
	return out
}

// TestSpeedHotkeyClampAndAnnouncement locks [07 R-CAM-01 §3]: the setter clamps
// 1..20 on signed compares, announces only when the clamped target changes, and
// prints the offset from normal with a `%c` that is a space for a negative
// offset. The announcement is posted to the ring as kind 2.
func TestSpeedHotkeyClampAndAnnouncement(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))

	pressKeys(b, input.KeyEqual)
	if got := b.sess.Clock.Requested; got != 11 {
		t.Fatalf("`=` set speed %d, want 11", got)
	}
	if got := newestRingText(b); got != "Game Speed  +1" {
		t.Fatalf("speed-up text %q, want %q", got, "Game Speed  +1")
	}
	lines := b.messageRing().Visible()
	if len(lines) == 0 {
		t.Fatalf("the speed announcement did not reach the ring")
	}
	last := lines[len(lines)-1]
	if last.Text != "Game Speed  +1" || last.Class != frame.MessageClassSpeed {
		t.Fatalf("ring line %q kind %d, want %q kind %d", last.Text, last.Class, "Game Speed  +1", frame.MessageClassSpeed)
	}

	// Back to normal, then below it: the negative offset keeps the space the
	// `%c` writes, so the label is three spaces wide before the sign.
	pressKeys(b, input.KeyMinus)
	if got := newestRingText(b); got != "Game Speed Normal" {
		t.Fatalf("speed 10 text %q, want %q", got, "Game Speed Normal")
	}
	pressKeys(b, input.KeyMinus)
	pressKeys(b, input.KeyMinus)
	if got := newestRingText(b); got != "Game Speed   -2" {
		t.Fatalf("speed 8 text %q, want %q", got, "Game Speed   -2")
	}

	// The clamp: nineteen more decrements cannot go below 1, and the setter is
	// silent once the clamped value stops changing.
	for i := 0; i < 19; i++ {
		pressKeys(b, input.KeyMinus)
	}
	if got := b.sess.Clock.Requested; got != 1 {
		t.Fatalf("floor clamp left speed %d, want 1", got)
	}
	b.messageRing().Clear()
	pressKeys(b, input.KeyMinus)
	if got := newestRingText(b); got != "" {
		t.Fatalf("a refused decrement announced %q", got)
	}
	for i := 0; i < 30; i++ {
		pressKeys(b, input.KeyEqual)
	}
	if got := b.sess.Clock.Requested; got != 20 {
		t.Fatalf("ceiling clamp left speed %d, want 20", got)
	}
}

// TestBookmarkStoreRecallRoundTrip locks the Ctrl+F5..F8 / F5..F8 rows: store
// takes the current origin, recall jumps back to it and cancels the follow
// [07 R-CAM-01 §2][07 R-CAM-01 §12].
func TestBookmarkStoreRecallRoundTrip(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(60, 60))
	b.cam.JumpTo(140, 90)
	storedX, storedZ := b.cam.X, b.cam.Z

	pressKeys(b, input.KeyCtrl, input.KeyF6)
	if !b.cam.Follow.Bookmarks[1].Valid {
		t.Fatalf("Ctrl+F6 did not set the slot's valid byte")
	}
	b.cam.SetTracked(7)
	b.cam.JumpTo(0, 0)
	pressKeys(b, input.KeyF6)
	if b.cam.X != storedX || b.cam.Z != storedZ {
		t.Fatalf("F6 recalled (%d,%d), want (%d,%d)", b.cam.X, b.cam.Z, storedX, storedZ)
	}
	if b.cam.Tracked() != 0 {
		t.Fatalf("a bookmark recall must cancel the follow triple")
	}
	// A slot that was never stored still recalls — its zero origin, clamped.
	pressKeys(b, input.KeyF8)
	if b.cam.X != b.cam.Follow.Bookmarks[3].Origin.X && b.cam.Follow.Bookmarks[3].Valid {
		t.Fatalf("unwritten slot 3 was refused")
	}
	// Ctrl+F5 and F5 are different arms of the same key.
	b.cam.JumpTo(200, 150)
	pressKeys(b, input.KeyCtrl, input.KeyF5)
	if !b.cam.Follow.Bookmarks[0].Valid {
		t.Fatalf("Ctrl+F5 stored nothing")
	}
	b.cam.JumpTo(0, 0)
	pressKeys(b, input.KeyF5)
	if b.cam.X == 0 && b.cam.Z == 0 {
		t.Fatalf("F5 did not recall the stored origin")
	}
}

// TestCtrlLetterSelectsInsteadOfArmingLatch locks the census's Ctrl column: a
// Ctrl-composed token never reaches an unmodified key's case [07 R-CAM-01 §2].
func TestCtrlLetterSelectsInsteadOfArmingLatch(t *testing.T) {
	cat := hotkeyCatalog(t)
	b := newTestBattle(cat, testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	one := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	two := placeUnit(b, "armsolar", numeric.Fixed(320*65536), numeric.Fixed(220*65536))

	// Plain `a` arms the attack latch; Ctrl+A selects and leaves the latch idle.
	pressKeys(b, input.KeyA)
	if b.battleState().Input.Latch != input.LatchAttack {
		t.Fatalf("`a` did not arm the attack latch")
	}
	b.battleState().Input.Latch = input.LatchNormal
	pressKeys(b, input.KeyCtrl, input.KeyA)
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("Ctrl+A armed the attack latch; retail's 0xAA can never reach the `a` case")
	}
	sel := selectedHandles(t, b)
	if len(sel) != 2 || !containsHandle(sel, one.Handle) || !containsHandle(sel, two.Handle) {
		t.Fatalf("Ctrl+A selected %v, want both own units", sel)
	}
}

// TestCtrlCategorySelectUsesCatalog locks the `CTRL_%c` row and Ctrl+C's
// commander follow [07 R-CAM-01 §2][07 R-CAM-01 §12].
func TestCtrlCategorySelectUsesCatalog(t *testing.T) {
	cat := hotkeyCatalog(t)
	b := newTestBattle(cat, testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	solar := placeUnit(b, "armsolar", numeric.Fixed(320*65536), numeric.Fixed(220*65536))

	pressKeys(b, input.KeyCtrl, input.KeyB)
	sel := selectedHandles(t, b)
	if len(sel) != 1 || sel[0] != solar.Handle {
		t.Fatalf("Ctrl+B selected %v, want only the CTRL_B member", sel)
	}
	// Ctrl+C replaces (no Shift) and then follows the last Commander-category
	// own unit.
	pressKeys(b, input.KeyCtrl, input.KeyC)
	sel = selectedHandles(t, b)
	if len(sel) != 1 || sel[0] != commander.Handle {
		t.Fatalf("Ctrl+C selected %v, want only the CTRL_C member", sel)
	}
	if b.cam.Tracked() != commander.Handle {
		t.Fatalf("Ctrl+C tracked %d, want the commander %d", b.cam.Tracked(), commander.Handle)
	}
	// Shift adds instead of replacing.
	pressKeys(b, input.KeyCtrl, input.KeyShift, input.KeyB)
	sel = selectedHandles(t, b)
	if len(sel) != 2 {
		t.Fatalf("Shift+Ctrl+B selected %v, want the union of both categories", sel)
	}
	// A letter no stock category authors selects nothing and changes nothing.
	before := len(sel)
	pressKeys(b, input.KeyCtrl, input.KeyQ)
	if got := len(selectedHandles(t, b)); got != before {
		t.Fatalf("Ctrl+Q changed the selection from %d to %d", before, got)
	}
}

// TestCtrlZSelectsMatchingDefinitions locks the Ctrl+Z row [07 R-CAM-01 §2].
func TestCtrlZSelectsMatchingDefinitions(t *testing.T) {
	cat := hotkeyCatalog(t)
	b := newTestBattle(cat, testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	first := placeUnit(b, "armsolar", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	second := placeUnit(b, "armsolar", numeric.Fixed(280*65536), numeric.Fixed(180*65536))
	other := placeUnit(b, "armcons", numeric.Fixed(340*65536), numeric.Fixed(240*65536))

	replaceSelectionForTest(t, b, first)
	pressKeys(b, input.KeyCtrl, input.KeyZ)
	sel := selectedHandles(t, b)
	if len(sel) != 2 || !containsHandle(sel, first.Handle) || !containsHandle(sel, second.Handle) {
		t.Fatalf("Ctrl+Z selected %v, want both armsolar units", sel)
	}
	if containsHandle(sel, other.Handle) {
		t.Fatalf("Ctrl+Z pulled in a different definition")
	}
}

// TestCtrlDSelfDestructsSelection locks the Ctrl+D row: the SELFDESTRUCT
// descriptor is issued for the selection through the human command channel
// [07 R-CAM-01 §2][04 R-ORD-01 §2].
func TestCtrlDSelfDestructsSelection(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	u := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	replaceSelectionForTest(t, b, u)

	want := orders.Lookup("SelfDestructFG")
	if want == 0 {
		t.Skip("order table has no SelfDestructFG")
	}
	pressKeys(b, input.KeyCtrl, input.KeyD)
	if b.battleState().Input.Latch == input.LatchBlast {
		t.Fatalf("Ctrl+D armed the BLAST latch as well as self-destructing")
	}
	q := orders.QueueForUnit(u)
	if q == nil {
		t.Fatalf("no order queue for the selected unit")
	}
	found := false
	for _, n := range q.Primary() {
		if n != nil && n.ID == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("Ctrl+D queued no SelfDestructFG record")
	}
}

// TestFollowCameraCyclesSelection locks the `t` / `T` row: the tracked object
// walks the selection in slot order and wraps, and an empty selection nulls it
// [07 R-CAM-01 §2][07 R-CAM-01 §12].
func TestFollowCameraCyclesSelection(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	first := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	second := placeUnit(b, "armsolar", numeric.Fixed(280*65536), numeric.Fixed(180*65536))
	replaceSelectionForTest(t, b, first, second)

	pressKeys(b, input.KeyT)
	if b.cam.Tracked() != first.Handle {
		t.Fatalf("`t` tracked %d, want the first selected unit %d", b.cam.Tracked(), first.Handle)
	}
	pressKeys(b, input.KeyT)
	if b.cam.Tracked() != second.Handle {
		t.Fatalf("`t` did not advance to the second selected unit")
	}
	pressKeys(b, input.KeyT)
	if b.cam.Tracked() != first.Handle {
		t.Fatalf("`t` did not wrap back to the first selected unit")
	}
	// Shift takes the other arm: the previous selected unit.
	pressKeys(b, input.KeyShift, input.KeyT)
	if b.cam.Tracked() != second.Handle {
		t.Fatalf("`T` went forwards, want the previous selected unit")
	}
	// Nothing selected nulls the tracked object.
	_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
	applyPendingBattleCommands(b)
	pressKeys(b, input.KeyT)
	if b.cam.Tracked() != 0 {
		t.Fatalf("`t` with nothing selected left tracked %d", b.cam.Tracked())
	}
}

// TestNextUnitCycleGlidesWithoutSelecting locks the `n` row [07 R-CAM-01 §2].
func TestNextUnitCycleGlidesWithoutSelecting(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(80, 80))
	b.sess.LocalOwner = 0
	far := placeUnit(b, "armcons", numeric.Fixed(1000*65536), numeric.Fixed(900*65536))
	applyPendingBattleCommands(b)

	before := len(selectedHandles(t, b))
	pressKeys(b, input.KeyN)
	if b.currentUnit != far.Handle {
		t.Fatalf("`n` recorded current unit %d, want %d", b.currentUnit, far.Handle)
	}
	if !b.cam.Follow.Gliding {
		t.Fatalf("`n` did not start a glide")
	}
	if got := len(selectedHandles(t, b)); got != before {
		t.Fatalf("`n` changed the selection from %d to %d", before, got)
	}
	if !b.visitedUnits[far.Handle] {
		t.Fatalf("`n` did not mark its pick visited")
	}
	// With every unit visited the cycle clears and restarts on the same unit.
	pressKeys(b, input.KeyN)
	if b.currentUnit != far.Handle {
		t.Fatalf("the restarted cycle picked %d, want %d", b.currentUnit, far.Handle)
	}
}

// TestBuildPageCommaAndPeriod locks the `,` / `.` rows [07 R-CAM-01 §2].
func TestBuildPageCommaAndPeriod(t *testing.T) {
	b := newTestBattle(hotkeyCatalog(t), testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	builder := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	replaceSelectionForTest(t, b, builder)
	f, ok := b.currentSnapshot()
	if !ok || f.CommandPage.PageCount < 2 {
		t.Fatalf("fixture builder publishes %d pages, want at least 2", f.CommandPage.PageCount)
	}
	start := f.CommandPage.Page

	pressKeys(b, input.KeyPeriod)
	f, _ = b.currentSnapshot()
	if f.CommandPage.Page == start {
		t.Fatalf("`.` left the build page at %d", start)
	}
	next := f.CommandPage.Page
	pressKeys(b, input.KeyComma)
	f, _ = b.currentSnapshot()
	if f.CommandPage.Page == next {
		t.Fatalf("`,` left the build page at %d", next)
	}
}

// TestSwitchAltDigitRouting exercises the production token path, including
// Alt's digit token and the shifted-character exclusion [07 R-CAM-01 §4]
// [07 R-CAM-01 §14].
func TestSwitchAltDigitRouting(t *testing.T) {
	cat := hotkeyCatalog(t)
	b := newTestBattle(cat, testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	builder := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	member := placeUnit(b, "armsolar", numeric.Fixed(280*65536), numeric.Fixed(180*65536))
	extra := placeUnit(b, "armsolar", numeric.Fixed(360*65536), numeric.Fixed(240*65536))

	// Assign the builder and member to group 1 through the same Ctrl token
	// handler the player uses, then leave the builder as the page target.
	replaceSelectionForTest(t, b, builder, member)
	pressKeys(b, input.KeyCtrl, input.Key1)
	replaceSelectionForTest(t, b, builder)

	// The absent/default-clear bit maps plain digits to pages.
	b.switchAlt = false
	pressKeys(b, input.Key2)
	f, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("default plain digit left no committed frame")
	}
	if f.CommandPage.Page != 1 {
		t.Fatalf("default plain digit page = %d, want page 1", f.CommandPage.Page)
	}
	pressKeys(b, input.KeyAlt, input.Key1)
	got := selectedHandles(t, b)
	if len(got) != 2 || !containsHandle(got, builder.Handle) || !containsHandle(got, member.Handle) {
		t.Fatalf("default Alt+1 recalled %v, want group members", got)
	}
	// Shift remains the recall preserve argument when Alt produces a digit
	// token, so the unrelated selection survives Shift+Alt+1.
	replaceSelectionForTest(t, b, extra)
	pressKeys(b, input.KeyShift, input.KeyAlt, input.Key1)
	got = selectedHandles(t, b)
	if len(got) != 3 || !containsHandle(got, extra.Handle) || !containsHandle(got, builder.Handle) || !containsHandle(got, member.Handle) {
		t.Fatalf("default Shift+Alt+1 selected %v, want preserved selection plus group", got)
	}

	// The set bit swaps the two arms: plain digits recall and Alt+digits page.
	b.switchAlt = true
	replaceSelectionForTest(t, b, extra)
	pressKeys(b, input.Key1)
	got = selectedHandles(t, b)
	if len(got) != 2 || !containsHandle(got, builder.Handle) || !containsHandle(got, member.Handle) {
		t.Fatalf("set SwitchAlt plain 1 recalled %v, want group members", got)
	}
	replaceSelectionForTest(t, b, builder)
	pressKeys(b, input.KeyAlt, input.Key2)
	f, ok = b.currentSnapshot()
	if !ok {
		t.Fatal("set SwitchAlt Alt+2 left no committed frame")
	}
	if f.CommandPage.Page != 1 {
		t.Fatalf("set SwitchAlt Alt+2 page = %d, want page 1", f.CommandPage.Page)
	}
	// Shift+digit without Alt is a shifted character. Digit 2 has no dispatch
	// case, so it cannot become an additive group recall under SwitchAlt=1.
	replaceSelectionForTest(t, b, extra)
	pressKeys(b, input.KeyShift, input.Key2)
	got = selectedHandles(t, b)
	if len(got) != 1 || got[0] != extra.Handle {
		t.Fatalf("Shift+2 under SwitchAlt recalled a group: %v", got)
	}
}

// TestF4TogglesPanelHoldAndF12ClearsRing locks the F4 and F12 rows
// [07 R-CAM-01 §2][07 §6].
func TestF4TogglesPanelHoldAndF12ClearsRing(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	if b.panelHoldFlag {
		t.Fatalf("interface-flags bit 0x80 starts set")
	}
	pressKeys(b, input.KeyF4)
	if !b.panelHoldFlag {
		t.Fatalf("F4 did not set interface-flags bit 0x80")
	}
	pressKeys(b, input.KeyF4)
	if b.panelHoldFlag {
		t.Fatalf("F4 did not clear interface-flags bit 0x80")
	}
	// Ctrl+F4 is a different token and has no case.
	pressKeys(b, input.KeyCtrl, input.KeyF4)
	if b.panelHoldFlag {
		t.Fatalf("Ctrl+F4 reached the F4 case")
	}

	b.messageRing().Append("one", 1, 0, 10, 1)
	b.messageRing().Append("two", 1, 0, 10, 1)
	if len(b.messageRing().Visible()) == 0 {
		t.Fatalf("the fixture ring is empty before F12")
	}
	pressKeys(b, input.KeyF12)
	if got := len(b.messageRing().Visible()); got != 0 {
		t.Fatalf("F12 left %d lines in the ring", got)
	}
	if b.messageRing().Producer != 0 || b.messageRing().Display != 0 {
		t.Fatalf("F12 left producer %d display %d, want both zero", b.messageRing().Producer, b.messageRing().Display)
	}
}

// TestF3GlidesToLiveMessageSource locks the F3 row [07 R-CAM-01 §2].
func TestF3GlidesToLiveMessageSource(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(80, 80))
	b.sess.LocalOwner = 0
	speaker := placeUnit(b, "armcons", numeric.Fixed(900*65536), numeric.Fixed(800*65536))
	applyPendingBattleCommands(b)

	// A record with no live source glides nowhere.
	b.messageRing().Append("ghost", 1, pool.Handle(4000), 10, 1)
	pressKeys(b, input.KeyF3)
	if b.cam.Follow.Gliding {
		t.Fatalf("F3 glided to a dead source")
	}
	b.messageRing().Append("live", 1, speaker.Handle, 10, 1)
	pressKeys(b, input.KeyF3)
	if !b.cam.Follow.Gliding {
		t.Fatalf("F3 did not glide to the live source")
	}
}

// TestEscapeDeselectsAndCancels locks the Escape row. Token 0xE3 is F2, not
// Escape; Escape closes the options window, cancels an armed latch, and
// otherwise deselects everything [07 R-CAM-01 §2 "Escape versus F2"].
func TestEscapeDeselectsAndCancels(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.LocalOwner = 0
	u := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	replaceSelectionForTest(t, b, u)

	b.battleState().Input.Latch = input.LatchAttack
	pressKeys(b, input.KeyEscape)
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("Escape did not return the armed latch to idle")
	}
	if u.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("Escape over an armed latch also deselected")
	}
	pressKeys(b, input.KeyEscape)
	if u.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("Escape with an idle latch did not deselect")
	}
}

// TestPresentationHotkeysPreservePartialFingerprint samples the I6 boundary
// through the documented fingerprint subset. Selection, group,
// speed, pause and order keys are deliberately excluded — [07 R-CAM-01 §1]
// lists them as the inputs that *do* become simulation state.
func TestPresentationHotkeysPreservePartialFingerprint(t *testing.T) {
	cat := hotkeyCatalog(t)
	b := newTestBattle(cat, testWorldON05(60, 60))
	b.sess.LocalOwner = 0
	placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	placeUnit(b, "armsolar", numeric.Fixed(320*65536), numeric.Fixed(220*65536))
	applyPendingBattleCommands(b)
	b.messageRing().Append("line", 1, 0, 10, 1)

	before, err := b.sess.PartialStateFingerprint()
	if err != nil {
		t.Fatalf("parity hash: %v", err)
	}
	presentation := [][]input.Key{
		{input.KeyT},
		{input.KeyShift, input.KeyT},
		{input.KeyN},
		{input.KeyF1},
		{input.KeyF3},
		{input.KeyF4},
		{input.KeyF12},
		{input.KeyCtrl, input.KeyF5},
		{input.KeyF5},
		{input.KeyCtrl, input.KeyF8},
		{input.KeyF8},
	}
	for _, combo := range presentation {
		in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
		for _, k := range combo {
			in.Kbd.SetKey(k, true)
		}
		b.handleInput(in, nil)
	}
	after, err := b.sess.PartialStateFingerprint()
	if err != nil {
		t.Fatalf("parity hash: %v", err)
	}
	if before != after {
		t.Fatalf("a presentation-only hotkey changed the partial fingerprint:\n before %s\n after  %s", before, after)
	}
	// The camera and ring are exactly what those keys are allowed to touch.
	if b.cam == nil {
		t.Fatalf("fixture lost its camera")
	}
	_ = units.ClassifierEligibleStatus
}
