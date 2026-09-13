package main

import (
	"bytes"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// paletteCallbackFrame publishes a hand-authored presentation view without
// retaining any slices owned by the buffer slot that BeginWrite resets. These
// are host-frame callback cases, so their frame is deliberately fixed.
func paletteCallbackFrame(t *testing.T, b *battleSession, change func(*frame.Frame)) {
	t.Helper()
	current, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("no committed callback frame")
	}
	next := *current
	next.Units = append([]frame.UnitView(nil), current.Units...)
	for i := range next.Units {
		next.Units[i].Pieces = append([]frame.PieceView(nil), current.Units[i].Pieces...)
	}
	next.Selection.Handles = append([]pool.Handle(nil), current.Selection.Handles...)
	next.CommandPage.ProductKeys = append([]string(nil), current.CommandPage.ProductKeys...)
	next.CommandPage.GeneratedProducts = append([]frame.GeneratedProductPlacement(nil), current.CommandPage.GeneratedProducts...)

	published := b.sess.Snapshot.BeginWrite()
	*published = next
	change(published)
	if err := b.sess.Snapshot.Publish(current.Tick + 1); err != nil {
		t.Fatal(err)
	}
}

func paletteCallbackToken(t *testing.T, b *battleSession, cl *client.Client, token rune) {
	t.Helper()
	if !cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: token}) {
		t.Fatalf("enqueue callback token %q", token)
	}
	b.viewerStep(0, cl)
}

type paletteCallbackCueSpy struct{ aliases []string }

func (s *paletteCallbackCueSpy) PlaySample(sample *audio.Sample, _ float64, _ float64) error {
	if sample != nil {
		s.aliases = append(s.aliases, sample.Alias)
	}
	return nil
}

func paletteCallbackCues(t *testing.T, b *battleSession, aliases ...string) *paletteCallbackCueSpy {
	t.Helper()
	b.sess.InitAudio(nil)
	for _, alias := range aliases {
		// A raw unsigned 8-bit sample is a valid authored-free test fixture
		// [fmt wav]; this observes the real callback's mode-zero audio edge.
		if _, err := b.sess.Audio.Cache.Put(alias, bytes.Repeat([]byte{0x80}, 64)); err != nil {
			t.Fatalf("author cue %q: %v", alias, err)
		}
	}
	previous := audio.GlobalOutput()
	spy := &paletteCallbackCueSpy{}
	audio.SetGlobalOutput(spy)
	t.Cleanup(func() { audio.SetGlobalOutput(previous) })
	return spy
}

func paletteCallbackFactory(t *testing.T, gadgets []gui.Gadget, products []string, page, count uint16) (*battleSession, *client.Client, pool.Handle) {
	t.Helper()
	b, cl, _ := paletteViewer(t, gadgets)
	b.hud.cat = b.cat
	factory := b.sess.Units.Unit(1)
	if factory == nil || factory.Def == nil {
		t.Fatal("palette fixture has no selected unit")
	}
	// The established viewer fixture supplies ARM FAV and its window. The
	// callback only needs a builder identity, so this keeps the fixture small.
	factory.Def.Builder = true
	paletteCallbackFrame(t, b, func(f *frame.Frame) {
		f.Selection.Handles = []pool.Handle{factory.Handle}
		f.Selection.Primary = factory.Handle
		f.Selection.Count = 1
		f.CommandPage = frame.CommandPageView{
			Builder: factory.Handle, Page: page, PageCount: count,
			ProductKeys: append([]string(nil), products...),
			MoveStance:  4, FireStance: 4, CloakState: 3, OnOffState: 3,
		}
	})
	b.viewerStep(0, cl)
	return b, cl, factory.Handle
}

func paletteCallbackClick(t *testing.T, b *battleSession, cl *client.Client, window *gui.Window, index int, right, shift bool) {
	t.Helper()
	r := window.PlacedRect(index)
	in := cl.Input()
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	in.Mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
	button := input.MouseButtonLeft
	if right {
		button = input.MouseButtonRight
	}
	in.Mouse.SetButton(button, true)
	b.viewerStep(0, cl)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(button, false)
	b.viewerStep(0, cl)
	in.Mouse.ResetEdges()
	if shift {
		in.Kbd.SetKey(input.KeyShift, false)
	}
}

// TestPaletteCallbackBuildOrdersAndPager uses the retained palette's actual
// TokenText route. BUILD and ORDERS own their own cue rows, whereas NEXT/PREV
// wrap only among build pages and are gated by the committed page count
// [07 §9][07 R-HUD-03 §6][07 R-HUD-04 §5].
func TestPaletteCallbackBuildOrdersAndPager(t *testing.T) {
	buttons := []gui.Gadget{
		{Kind: gui.KindButton, Name: "ARMORDERS", Active: 1, QuickKey: 'o', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "ARMBUILD", Active: 1, QuickKey: 'b', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "ARMNEXT", Active: 1, QuickKey: 'n', Rect: gui.Rect{X: 47, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "ARMPREV", Active: 1, QuickKey: 'p', Rect: gui.Rect{X: 70, Y: 1, W: 20, H: 12}},
	}
	b, cl, factory := paletteCallbackFactory(t, buttons, nil, 0, 4)
	spy := paletteCallbackCues(t, b, ordersButtonCue, buildButtonCue, cueNextBuildMenu)

	paletteCallbackToken(t, b, cl, 'o')
	paletteCallbackToken(t, b, cl, 'b')
	paletteCallbackToken(t, b, cl, 'n')
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 3 {
		t.Fatalf("callback commands=%v, want orders, build, next", pending)
	}
	for i, want := range []int{0, 1, 2} {
		if got := pending[i]; got.Kind != session.HumanBuildPage || got.BuildPage.Builder != factory || got.BuildPage.Page != want {
			t.Fatalf("callback %d=%+v, want page %d for factory %v", i, got, want, factory)
		}
	}
	if got, want := spy.aliases, []string{ordersButtonCue, buildButtonCue, cueNextBuildMenu}; !slices.Equal(got, want) {
		t.Fatalf("BUILD/ORDERS/NEXT cues=%v, want %v", got, want)
	}

	// PREV follows the pending NEXT page before any publication [07 R-HUD-03 §6].
	paletteCallbackToken(t, b, cl, 'p')
	pending = b.sess.PendingHumanCommands()
	if got := pending[len(pending)-1]; got.Kind != session.HumanBuildPage || got.BuildPage.Page != 1 {
		t.Fatalf("PREV callback=%+v, want pending previous page 1", got)
	}

	// A one-page count hides the gadget and cannot dispatch or play a
	// page cue [07 R-HUD-03 §6].
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.CommandPage.PageCount = 1 })
	beforeCommands, beforeCues := len(b.sess.PendingHumanCommands()), len(spy.aliases)
	paletteCallbackToken(t, b, cl, 'n')
	if got := len(b.sess.PendingHumanCommands()); got != beforeCommands {
		t.Fatalf("one-page NEXT added commands: %v", b.sess.PendingHumanCommands())
	}
	if got := len(spy.aliases); got != beforeCues {
		t.Fatalf("one-page NEXT added cue %v", spy.aliases[beforeCues:])
	}
}

// Installed product names select both factory and placement definitions [07 §9].
// The published membership list deliberately has the reverse order.
func TestPaletteCallbackProductNamesFactoryAndPlacement(t *testing.T) {
	buttons := []gui.Gadget{
		{Kind: gui.KindButton, Name: "ARMFAV", Active: 1, QuickKey: 'a', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "ARMSOLAR", Active: 1, QuickKey: 's', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
	}
	b, cl, factory := paletteCallbackFactory(t, buttons, []string{"armsolar", "armfav"}, 0, 2)
	spy := paletteCallbackCues(t, b, cueAddBuild, cueSubBuild)

	paletteCallbackToken(t, b, cl, 'a')
	cl.Input().Kbd.SetKey(input.KeyShift, true)
	paletteCallbackToken(t, b, cl, 'a')
	cl.Input().Kbd.SetKey(input.KeyShift, false)
	pending := paletteFactoryCommands(b)
	if len(pending) != 2 {
		t.Fatalf("factory token commands=%v, want two", pending)
	}
	for i, want := range []int{1, 5} {
		if got := pending[i]; got.Kind != session.HumanFactoryBuild || got.FactoryBuild.Builder != factory || got.FactoryBuild.Product != "armfav" || got.FactoryBuild.Count != want {
			t.Fatalf("factory token %d=%+v, want armfav count %d", i, got, want)
		}
	}

	// Right release travels through the same retained Panel service and is the
	// cancellation arm: -1 normally and -5 with Shift [07 R-P0-11 §1].
	w := b.hud.windows["armfav1"]
	paletteCallbackClick(t, b, cl, w, 1, true, false)
	paletteCallbackClick(t, b, cl, w, 1, true, true)
	pending = paletteFactoryCommands(b)
	for i, want := range []int{-1, -5} {
		got := pending[2+i]
		if got.Kind != session.HumanFactoryBuild || got.FactoryBuild.Product != "armfav" || got.FactoryBuild.Count != want {
			t.Fatalf("factory right callback %d=%+v, want armfav count %d", i, got, want)
		}
	}
	if got, want := spy.aliases, []string{cueAddBuild, cueAddBuild, cueSubBuild, cueSubBuild}; !slices.Equal(got, want) {
		t.Fatalf("factory cues=%v, want %v", got, want)
	}

	paletteCallbackToken(t, b, cl, 's')
	if got := b.battleState().Input.Latch; got != input.LatchMobileBuild {
		t.Fatalf("named building latch=%v, want MOBILEBUILD", got)
	}
	if got := b.PlacementProduct(); got != content.CanonicalKey("armsolar") {
		t.Fatalf("named building placement product=%q, want armsolar", got)
	}
	if got := spy.aliases[len(spy.aliases)-1]; got != cueAddBuild {
		t.Fatalf("mobile placement cue=%q, want %q", got, cueAddBuild)
	}
	// A non-resolving name cannot silently take a product at the same index.
	b.disarmPlacement()
	w.Gadgets[2].Name = "UNRESOLVED"
	ctx, ok := b.hud.paletteContext(b)
	if !ok {
		t.Fatal("missing palette context")
	}
	before := len(b.sess.PendingHumanCommands())
	b.hud.activatePaletteGadget(b, ctx, 2, false, input.Modifiers{})
	if b.PlacementProduct() != "" || len(b.sess.PendingHumanCommands()) != before {
		t.Fatal("unknown gadget name selected an ordinal product")
	}

}

// Modifier-latch commands share the stream but are not palette callbacks.
func paletteFactoryCommands(b *battleSession) []session.HumanCommand {
	var commands []session.HumanCommand
	for _, command := range b.sess.PendingHumanCommands() {
		if command.Kind == session.HumanFactoryBuild {
			commands = append(commands, command)
		}
	}
	return commands
}

// This user-requested host extension shares the existing counted factory queue
// producer; Alt overrides Shift rather than multiplying their batch sizes.
func TestPaletteAltBuildBatch(t *testing.T) {
	buttons := []gui.Gadget{
		{Kind: gui.KindButton, Name: "ARMFAV", Active: 1, QuickKey: 'a', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "ARMSOLAR", Active: 1, Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
	}
	b, cl, factory := paletteCallbackFactory(t, buttons, []string{"armfav", "armsolar"}, 0, 2)
	w := b.hud.windows["armfav1"]
	spy := paletteCallbackCues(t, b, cueAddBuild, cueSubBuild)
	for _, tc := range []struct {
		alt, shift, right bool
		want              int
	}{
		{true, false, false, 20},
		{true, true, false, 20},
		{true, false, true, -20},
		{true, true, true, -20},
		{false, true, false, 5},
		{false, false, false, 1},
	} {
		before := len(paletteFactoryCommands(b))
		cl.Input().Kbd.SetKey(input.KeyAlt, tc.alt)
		paletteCallbackClick(t, b, cl, w, 1, tc.right, tc.shift)
		pending := paletteFactoryCommands(b)
		if len(pending) != before+1 {
			t.Fatalf("modifiers %+v produced %v", tc, pending)
		}
		got := pending[before].FactoryBuild
		if got.Builder != factory || got.Product != "armfav" || got.Count != tc.want {
			t.Fatalf("modifiers %+v produced %+v", tc, got)
		}
	}
	if want := []string{cueAddBuild, cueAddBuild, cueSubBuild, cueSubBuild, cueAddBuild, cueAddBuild}; !slices.Equal(spy.aliases, want) {
		t.Fatalf("counted build cues %v, want %v", spy.aliases, want)
	}
	// A building button still arms one placement rather than issuing a batch.
	cl.Input().Kbd.SetKey(input.KeyAlt, true)
	before := len(paletteFactoryCommands(b))
	paletteCallbackClick(t, b, cl, w, 2, false, false)
	if b.PlacementProduct() != "armsolar" || len(paletteFactoryCommands(b)) != before {
		t.Fatal("Alt building click did not retain placement action")
	}
}
