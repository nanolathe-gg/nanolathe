package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

// paletteViewer drives the production host-frame route. The first frame opens
// the selected command window, deliberately flushing its inherited token tail
// before tests enqueue the token belonging to that open window.
func paletteViewer(t *testing.T, gadgets []gui.Gadget) (*battleSession, *client.Client, *gui.Window) {
	t.Helper()
	resetUnitInfoState(t)
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	// Hand-authored committed frames must not race the real monotonic tick
	// producer while the test services presentation input.
	b.millisSource = &fakeMillisSource{}
	w := &gui.Window{Rect: gui.Rect{W: 128, H: 80}, Gadgets: append([]gui.Gadget{{Kind: gui.KindPanel}}, gadgets...)}
	b.hud = &retailBattleHUD{fs: vfs.New(), windows: map[string]*gui.Window{"gen": w, "armfav1": w}}
	u := placeUnit(b, "armfav", numeric.Fixed(20<<16), numeric.Fixed(20<<16))
	replaceSelectionForTest(t, b, u)
	// The palette reads the committed capability aggregates. This fixture
	// authors its buttons directly, so publish the matching visible commands.
	current, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("selection did not publish a frame")
	}
	published := b.sess.Snapshot.BeginWrite()
	*published = *current
	published.CommandPage.CanDefend = true
	published.CommandPage.CanMove = true
	published.CommandPage.CanStop = true
	published.CommandPage.CanAttack = true
	if err := b.sess.Snapshot.Publish(current.Tick + 1); err != nil {
		t.Fatal(err)
	}
	cl := b.cl
	if cl == nil {
		t.Fatal("test battle has no presentation client")
	}
	b.viewerStep(0, cl)
	return b, cl, w
}

// TestPaletteViewerTokensReachTheRetainedService proves the production
// viewer's original-state pre-pass: StateFromSample intentionally has no token
// ring, so these actions would otherwise disappear before the palette saw them
// [07 R-WGT-01 §§1-3][07 R-WGT-02 §5].
func TestPaletteViewerTokensReachTheRetainedService(t *testing.T) {
	b, cl, _ := paletteViewer(t, []gui.Gadget{
		{Kind: gui.KindButton, Name: "DEFEND", Active: 1, QuickKey: 'F', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "MOVE", Active: 1, QuickKey: 'V', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindButton, Name: "STOP", Active: 1, QuickKey: 'S', Rect: gui.Rect{X: 47, Y: 1, W: 20, H: 12}},
	})
	for _, tc := range []struct {
		token rune
		want  input.Latch
	}{
		{'f', input.LatchFollow},
		{'v', input.LatchMove},
		{'s', input.LatchNormal},
	} {
		if !cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: tc.token}) {
			t.Fatalf("enqueue %q", tc.token)
		}
		b.viewerStep(0, cl)
		if got := b.battleState().Input.Latch; got != tc.want {
			t.Fatalf("%q latch=%v, want %v", tc.token, got, tc.want)
		}
		if got := cl.Input().PendingTokens(); got != 0 {
			t.Fatalf("%q left %d tokens", tc.token, got)
		}
	}
	if pending := b.sess.PendingHumanCommands(); len(pending) == 0 {
		t.Fatal("S command did not reach the command boundary")
	}
}

func TestPaletteViewerHonorsLowGreyAndSharedCapture(t *testing.T) {
	b, cl, w := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: 'A', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}})
	// Only bit zero greys a button. An upper status bit remains eligible.
	w.Gadgets[1].GrayedOut = 2
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchAttack {
		t.Fatalf("upper-bit grey latch=%v, want attack", got)
	}
	b.battleState().Input.Latch = input.LatchNormal
	w.Gadgets[1].GrayedOut = 1
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("low-bit grey latch=%v, want normal", got)
	}

	w.Gadgets[1].GrayedOut = 0
	in := cl.Input()
	in.Mouse.SetPosition(5, 5)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.viewerStep(0, cl)
	p := b.hud.palettePanels[w]
	if p == nil {
		t.Fatal("palette panel was not retained")
	}
	if got := p.CaptureIndex(); got != 1 {
		t.Fatalf("palette capture=%d, want index 1", got)
	}
	in.Mouse.ResetEdges()
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("captured quickkey fired latch=%v", got)
	}
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchAttack {
		t.Fatalf("release latch=%v, want attack", got)
	}
}

func TestPaletteViewerUsesLinkedAndSpecialTokens(t *testing.T) {
	b, cl, w := paletteViewer(t, []gui.Gadget{
		{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: 0xf2, Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindLabel, Name: "ATTACKLABEL", Active: 1, Link: "ATTACK", QuickKey: 'L', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
		{Kind: gui.KindTextBox, Name: "EDITOR", Active: 1, Attribs: 1, MaxChars: 12, Rect: gui.Rect{X: 47, Y: 1, W: 20, H: 12}},
	})
	// Prior's edit-token byte is an authored quickkey, not a TokenText
	// fallback. It reaches the same exact callback result as a mouse release.
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyPrior})
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchAttack {
		t.Fatalf("special token latch=%v, want attack", got)
	}

	b.battleState().Input.Latch = input.LatchNormal
	p := b.hud.palettePanels[w]
	if p == nil {
		t.Fatal("palette panel was not retained")
	}
	p.FocusEditor(3)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'l'})
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("editor captured linked key latch=%v", got)
	}

	cl.Input().Kbd.SetKey(input.KeyAlt, true)
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'l'})
	b.viewerStep(0, cl)
	cl.Input().Kbd.SetKey(input.KeyAlt, false)
	if got := b.battleState().Input.Latch; got != input.LatchAttack {
		t.Fatalf("Alt linked key latch=%v, want attack", got)
	}
}

func TestPaletteViewerUnitInfoAndSelectionOpenOwnTokens(t *testing.T) {
	b, cl, _ := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ATTACK", Active: 1, QuickKey: 'A', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}})
	info := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Name: "DONE", Active: 1, QuickKey: 'A'}}}
	unitInfoUI = &unitInfoScreen{window: info, panel: ui.NewPanel(info)}
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'})
	b.viewerStep(0, cl)
	if unitInfoOpen() || b.battleState().Input.Latch != input.LatchNormal {
		t.Fatal("UNITINFO quickkey leaked to the command palette")
	}

	// A changed selection reopens the command window. That transition flushes
	// its inherited tail before the palette's original-state token peek.
	other := placeUnit(b, "armfav", numeric.Fixed(30<<16), numeric.Fixed(20<<16))
	if !cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'a'}) {
		t.Fatal("enqueue selection-open token")
	}
	current, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("missing committed selection")
	}
	// This test needs the presentation-open boundary, not a simulation tick.
	// Publish the alternate selection directly so the previous fixture's
	// hand-authored capability aggregate remains part of the immutable frame.
	published := b.sess.Snapshot.BeginWrite()
	*published = *current
	published.Selection.Handles = []pool.Handle{other.Handle}
	published.Selection.Primary = other.Handle
	published.Selection.Count = 1
	if err := b.sess.Snapshot.Publish(current.Tick + 1); err != nil {
		t.Fatal(err)
	}
	b.viewerStep(0, cl)
	if got := b.battleState().Input.Latch; got != input.LatchNormal {
		t.Fatalf("selection-open tail activated palette latch=%v", got)
	}
	if pending := cl.Input().PendingTokens(); pending != 0 {
		t.Fatalf("selection-open tail pending=%d", pending)
	}
}
