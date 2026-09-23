package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The order-button dispatcher writes the parsed latch only while the fired
// button's down-state is non-zero, and the idle latch otherwise [07 §9]. The
// stock RECLAIM button is a toggle (attribute 0x40), so its second press flips
// it up [07 R-WGT-01 §3] and must disarm RECLAIM — and with it the reclaim
// cursor and the Community wreck-snap preview, both of which read the latch.
// A radio button (0x10) stays down on a second press and so stays armed.
func TestOrderButtonSecondPressFollowsTheDownState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attribs    uint32
		wantSecond input.Latch
		wantDown   int
	}{
		{name: "toggle flips up and disarms", attribs: 0x40, wantSecond: input.LatchNormal, wantDown: 0},
		{name: "radio stays down and armed", attribs: 0x10, wantSecond: input.LatchReclaim, wantDown: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, cl, window := paletteViewer(t, []gui.Gadget{
				{Kind: gui.KindButton, Name: "ARMRECLAIM", Active: 1, Attribs: tc.attribs, Assoc: 1, QuickKey: 'E', Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}},
				{Kind: gui.KindButton, Name: "ARMSTOP", Active: 1, Assoc: 1, QuickKey: 'S', Rect: gui.Rect{X: 24, Y: 1, W: 20, H: 12}},
			})
			current, ok := b.currentSnapshot()
			if !ok {
				t.Fatal("no committed frame")
			}
			published := b.sess.Snapshot.BeginWrite()
			*published = *current
			published.CommandPage.CanReclaim = true
			if err := b.sess.Snapshot.Publish(current.Tick + 1); err != nil {
				t.Fatal(err)
			}
			b.viewerStep(0, cl)
			panel := b.hud.palettePanels[window]
			if f, _ := b.currentSnapshot(); !f.CommandPage.CanReclaim {
				t.Fatalf("CanReclaim not published: %+v", f.CommandPage)
			}
			press := func() {
				cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'e'})
				b.viewerStep(0, cl)
			}
			press()
			if got := b.battleState().Input.Latch; got != input.LatchReclaim || panel.DownAt(1) != 1 {
				t.Fatalf("first press: latch %v down %d, want RECLAIM and down", got, panel.DownAt(1))
			}
			press()
			if got := b.battleState().Input.Latch; got != tc.wantSecond || panel.DownAt(1) != tc.wantDown {
				t.Fatalf("second press: latch %v down %d, want %v down %d", got, panel.DownAt(1), tc.wantSecond, tc.wantDown)
			}
		})
	}
}

// With no retained widget to read, the dispatcher keeps the armed answer the
// button name asks for; direct callers depend on it.
func TestOrderButtonGateWithoutWidgetArms(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.handleHudOrderButton("reclaim")
	if got := b.battleState().Input.Latch; got != input.LatchReclaim {
		t.Fatalf("latch %v, want RECLAIM", got)
	}
}

// reclaimSnapFixture stands a reclaim-capable builder beside a reclaimable
// feature rooted at cell (20,20) and returns a screen point on open ground two
// cells past its footprint, inside a Community wreck-snap radius of 3.
func reclaimSnapFixture(t *testing.T, features community.Features) (*battleSession, *client.Client, int32, int32) {
	t.Helper()
	b, cl, _, _ := resourceFixture(t, true)
	reclaimable := *b.cat.Features["deposit"]
	reclaimable.Reclaimable = true
	b.cat.Features["deposit"] = &reclaimable
	b.sess.Features.InstanceAt(20, 20).Def = &reclaimable
	b.sess.World.FeatureDefs[0] = &reclaimable // the snap's cell test reads the world's table
	b.cat.Units["armcons"].CanReclamate = true
	b.sess.Community = features
	prefs := settings.DefaultPresentation()
	b.hostPresentation = &prefs
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	x, y := o5ScreenWorld(b.cam, (24*16+8)<<16, 0, (21*16+8)<<16)
	return b, cl, x, y
}

// A Community wreck-snap preview consumes the armed reclaim click and issues
// code 12 at the snapped feature, whichever click producer carries it — here
// the Enhanced client's command-drag short release, which used to reclaim at
// the raw pointer and so be refused by the shape gate beside the marker.
// Strict (no WreckSnap) keeps the raw click, which the gate refuses over open
// ground (community patch engine CP-CON-6; [07 R-CAM-01 §14] step 3).
func TestArmedReclaimShortReleaseTakesTheWreckSnap(t *testing.T) {
	for _, tc := range []struct {
		name     string
		features community.Features
		issued   bool
	}{
		{name: "community snaps", features: community.Features{WreckSnap: true, WreckSnapRadius: 3, WreckSnapRadiusMax: 3}, issued: true},
		{name: "strict raw click refused", features: community.Features{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, cl, sx, sy := reclaimSnapFixture(t, tc.features)
			b.battleState().SetLatch(input.LatchReclaim)
			if got := b.cursorShapeAt(input.LatchReclaim, sx, sy); got != render.CursorNormal {
				t.Fatalf("raw pointer shape = %s, want cursornormal over open ground", render.CursorName(got))
			}
			dragInput(b, cl, sx, sy, input.MouseButtonLeft, "press", input.Modifiers{})
			if b.modernDrag == nil {
				t.Fatal("armed reclaim press did not begin the command gesture")
			}
			dragInput(b, cl, sx, sy, input.MouseButtonLeft, "release", input.Modifiers{})
			pending := b.sess.PendingHumanCommands()
			if !tc.issued {
				if len(pending) != 0 || b.battleState().Input.Latch != input.LatchReclaim {
					t.Fatalf("refused raw click dispatched %+v, latch %v", pending, b.battleState().Input.Latch)
				}
				return
			}
			if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 12 || !pending[0].Order.Position.HasFeature {
				t.Fatalf("snapped reclaim dispatch = %+v, want one code-12 feature order", pending)
			}
			want := b.sess.PlacementSnapSample(20, 20, 1, 1)
			if got := pending[0].Order.Position; got.X != want.WX || got.Z != want.WZ {
				t.Fatalf("snapped reclaim at (%v,%v), want feature centre (%v,%v)", got.X, got.Z, want.WX, want.WZ)
			}
			if got := b.battleState().Input.Latch; got != input.LatchNormal {
				t.Fatalf("issued reclaim left latch %v, want idle", got)
			}
		})
	}
}

// The idle latch is not the armed RECLAIM latch. Hovering a reclaimable
// feature with a `canreclamate` builder selected shows `cursorreclamate` in
// the Type-0 idle row, and the click resolves contextual code 1, whose feature
// arm is Reclaim [07 §8][04 R-ORD-02 §1]. Neither involves the wreck snap:
// its preview is armed only while RECLAIM is prepared (community patch engine
// CP-CON-6), so an idle click beside a feature snaps nothing.
func TestIdleReclaimCursorAndClickDoNotSnap(t *testing.T) {
	b, cl, nearX, nearY := reclaimSnapFixture(t, community.Features{WreckSnap: true, WreckSnapRadius: 3, WreckSnapRadiusMax: 3})
	dragInput(b, cl, nearX, nearY, input.MouseButtonLeft, "held", input.Modifiers{})
	if b.communityReclaimSnapOverlayArmed() {
		t.Fatal("idle latch armed the wreck-snap preview")
	}
	onX, onY := o5ScreenWorld(b.cam, (21*16+8)<<16, 0, (21*16+8)<<16)
	if got := b.cursorShapeAt(input.LatchNormal, onX, onY); got != render.CursorReclamate {
		t.Fatalf("idle shape over feature = %s, want cursorreclamate", render.CursorName(got))
	}
	if got := b.cursorShapeAt(input.LatchNormal, nearX, nearY); got != render.CursorMove {
		t.Fatalf("idle shape beside feature = %s, want cursormove", render.CursorName(got))
	}
	dragInput(b, cl, onX, onY, input.MouseButtonLeft, "press", input.Modifiers{})
	dragInput(b, cl, onX, onY, input.MouseButtonLeft, "release", input.Modifiers{})
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 1 || !pending[0].Order.Position.HasFeature {
		t.Fatalf("idle click over feature = %+v, want one contextual code-1 feature order", pending)
	}
}
