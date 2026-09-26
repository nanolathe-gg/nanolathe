package main

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The unarmed water unit is deliberately in CTRL_W: Zero's authored category
// must not become its armed selector (DESIGN_INTERFACE_HUD_INPUT §3.13).
func zeroSelectionFixture(t *testing.T, scheme int) (*battleSession, *frame.Frame) {
	t.Helper()
	defs := map[string]*content.UnitDef{
		"ground":  {CanAttack: true, BMCode: 1},
		"air":     {CanAttack: true, BMCode: 1, CanFly: true},
		"tower":   {CanAttack: true},
		"water":   {Category: "CTRL_W", BMCode: 1},
		"builder": {Category: "CTRL_B", BMCode: 1},
		"factory": {Category: "CTRL_F"},
	}
	reg, err := content.CompileCategories(defs)
	if err != nil {
		t.Fatal(err)
	}
	b := &battleSession{
		cat:   &content.Catalog{Units: defs, Categories: reg},
		cam:   &camera.Camera{ViewW: 640, ViewH: 480, MapW: 1600, MapH: 1600},
		sess:  &session.Session{Snapshot: &frame.Buffer{}},
		shell: &gameShell{presentation: settings.Presentation{CommunitySelection: scheme}},
	}
	f := b.sess.Snapshot.BeginWrite()
	for i, name := range []string{"ground", "air", "tower", "water", "builder", "factory", "ground", "ground", "ground"} {
		d := defs[name]
		v := frame.UnitView{Slot: pool.Handle(i + 1), DefID: uint16(d.UnitDefID), DefName: name,
			Flags: units.ClassifierEligibleStatus, PriorHealthSample: 100,
			X: numeric.FixedFromInt(int64(200 + i*20)), Z: numeric.FixedFromInt(120)}
		switch i {
		case 6:
			v.Owner = 1
		case 7:
			v.BuildRemaining = .5
		case 8:
			v.X = numeric.FixedFromInt(1200)
		}
		f.Units = append(f.Units, v)
	}
	if err := b.sess.Snapshot.Publish(1); err != nil {
		t.Fatal(err)
	}
	return b, b.sess.Snapshot.Current()
}

func lastZeroSelection(t *testing.T, b *battleSession) session.HumanCommand {
	t.Helper()
	pending := b.sess.PendingHumanCommands()
	if len(pending) == 0 {
		t.Fatal("selection did not enqueue a command")
	}
	return pending[len(pending)-1]
}

func TestZeroSelectionKeepsRetailAndCommunityMeaning(t *testing.T) {
	for _, tc := range []struct {
		scheme int
		shift  bool
		want   []pool.Handle
	}{
		{0, false, []pool.Handle{1, 2, 3, 4, 5, 6}},
		{1, false, []pool.Handle{4}},
		{2, false, []pool.Handle{1, 2, 3}},
		{2, true, []pool.Handle{1, 2, 3, 4, 5, 6}},
	} {
		b, _ := zeroSelectionFixture(t, tc.scheme)
		kbd := &input.KeyboardState{}
		kbd.SetKey(input.KeyS, true)
		kbd.SetKey(input.KeyShift, tc.shift)
		b.dispatchCtrlLetters(kbd)
		if got := lastZeroSelection(t, b).Selection.Handles; !slices.Equal(got, tc.want) {
			t.Fatalf("scheme %d shift %v selected %v, want %v", tc.scheme, tc.shift, got, tc.want)
		}
	}
	for _, key := range []input.Key{input.KeyB, input.KeyF} {
		b, _ := zeroSelectionFixture(t, 2)
		kbd := &input.KeyboardState{}
		kbd.SetKey(key, true)
		b.dispatchCtrlLetters(kbd)
		want := pool.Handle(5)
		if key == input.KeyF {
			want = 6
		}
		if got := lastZeroSelection(t, b).Selection.Handles; !slices.Equal(got, []pool.Handle{want}) {
			t.Fatalf("Zero idle key %v selected %v", key, got)
		}
	}
}

func TestZeroRectangleFilterCapabilitiesAndPrecedence(t *testing.T) {
	band := client.SelectionBand{Record: client.Rect{MinX: 128, MinY: 32, MaxX: 639, MaxY: 447}}
	for _, tc := range []struct {
		scheme int
		keys   []input.Key
		want   []pool.Handle
	}{
		{0, []input.Key{input.KeyW}, []pool.Handle{1, 2, 3, 4, 5, 6}},
		{1, []input.Key{input.KeyW}, []pool.Handle{1, 2, 3, 4, 5, 6}},
		{2, nil, []pool.Handle{1, 2, 3, 4, 5, 6}},
		{2, []input.Key{input.KeyW}, []pool.Handle{1, 2}},
		{2, []input.Key{input.KeyB}, []pool.Handle{5}},
		{2, []input.Key{input.KeyY}, []pool.Handle{6}},
		{2, []input.Key{input.KeyW, input.KeyB, input.KeyY}, []pool.Handle{1, 2}},
		{2, []input.Key{input.KeyB, input.KeyY}, []pool.Handle{5}},
	} {
		b, f := zeroSelectionFixture(t, tc.scheme)
		kbd := &input.KeyboardState{}
		for _, key := range tc.keys {
			kbd.SetKey(key, true)
		}
		kbd.ResetEdges() // Held keys, not press edges, choose the release filter.
		if got := b.eligibleHandlesInBand(f, band, b.zeroDragFilter(kbd)); !slices.Equal(got, tc.want) {
			t.Fatalf("scheme %d keys %v selected %v, want %v", tc.scheme, tc.keys, got, tc.want)
		}
	}
}

func TestZeroMegamapFilterSamplesReleaseAndKeepsShiftToggle(t *testing.T) {
	b, _ := zeroSelectionFixture(t, 2)
	b.sess.World = testWorldON05(100, 100)
	b.shell.presentation.Overview = settings.OverviewMegamap
	b.setSurfaceSize(640, 480)
	b.setMegamapShown(true, nil)
	lens := b.megamapLens()
	if !lens.Valid() {
		t.Fatal("invalid fixture megamap lens")
	}
	in := input.NewState()
	for _, event := range []input.PointerEvent{
		{Kind: input.LeftDown, X: lens.X, Y: lens.Y, Buttons: input.MouseButtons{Left: true}},
		{Kind: input.LeftUp, X: lens.X + lens.W - 1, Y: lens.Y + lens.H - 1, Modifiers: input.Modifiers{Shift: true}},
	} {
		if event.Kind == input.LeftUp {
			in.Kbd.SetKey(input.KeyB, true) // Pressed after the rectangle began.
		}
		in.EnqueuePointer(event)
		in.PublishPointer()
		b.serviceMegamapPointer(in, b.pointerSample(in, 0), nil)
	}
	c := lastZeroSelection(t, b)
	if c.Kind != session.HumanSelectionToggle || !slices.Equal(c.Selection.Handles, []pool.Handle{5}) {
		t.Fatalf("release filter/toggle = %+v", c)
	}
}

func TestZeroWorldRectangleFiltersOnRelease(t *testing.T) {
	for _, key := range []input.Key{input.KeyB, input.KeyY} {
		b, _ := zeroSelectionFixture(t, 2)
		b.sess.World = testWorldON05(100, 100)
		b.millisSource = &fakeMillisSource{}
		in := input.NewState()
		in.Mouse.SetPosition(180, 80)
		in.Mouse.SetButton(input.MouseButtonLeft, true)
		b.handleInput(in, nil)
		in.Mouse.ResetEdges()
		in.Mouse.SetPosition(620, 420)
		b.handleInput(in, nil)
		in.Mouse.ResetEdges()
		in.Kbd.SetKey(key, true)
		in.Kbd.SetKey(input.KeyShift, true)
		in.Mouse.SetButton(input.MouseButtonLeft, false)
		b.handleInput(in, nil)
		want := pool.Handle(5)
		if key == input.KeyY {
			want = 6
		}
		c := lastZeroSelection(t, b)
		if c.Kind != session.HumanSelectionToggle || !slices.Equal(c.Selection.Handles, []pool.Handle{want}) {
			t.Fatalf("world release %v selected %+v", key, c)
		}
	}
}

func TestZeroRectangleOwnsPaletteFilterQuickkeys(t *testing.T) {
	for _, scheme := range []int{0, 1, 2} {
		for _, active := range []bool{false, true} {
			for _, key := range []struct {
				key  input.Key
				text rune
			}{{input.KeyW, 'w'}, {input.KeyB, 'b'}, {input.KeyY, 'y'}} {
				b, cl, _ := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ATTACK", Active: 1,
					Attribs: 0x40, Assoc: 1, QuickKey: uint8(key.text - 'a' + 'A'), Rect: gui.Rect{X: 1, Y: 1, W: 20, H: 12}}})
				b.shell = &gameShell{presentation: settings.Presentation{CommunitySelection: scheme}}
				in := cl.Input()
				if active {
					in.Mouse.SetPosition(180, 80)
					in.Mouse.SetButton(input.MouseButtonLeft, true)
					b.viewerStep(0, cl)
					in.Mouse.ResetEdges()
					in.Mouse.SetPosition(500, 400)
					b.viewerStep(0, cl)
					if !b.battleState().Input.DragActive {
						t.Fatal("world selection drag did not begin")
					}
				}
				in.Kbd.SetKey(key.key, true)
				in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: key.text})
				b.viewerStep(0, cl)
				want := input.LatchAttack
				if scheme == 2 && active {
					want = input.LatchNormal
				}
				if got := b.battleState().Input.Latch; got != want || !in.Kbd.KeyHeld(key.key) || in.PendingTokens() != 0 {
					t.Fatalf("scheme=%d active=%v key=%v latch=%v want=%v held=%v pending=%d", scheme, active, key.key, got, want, in.Kbd.KeyHeld(key.key), in.PendingTokens())
				}
			}
		}
	}
}

func TestZeroRectanglePreservesOtherTokens(t *testing.T) {
	for _, tc := range []struct {
		token input.Token
		alt   bool
	}{
		{input.Token{Kind: input.TokenText, Rune: 'b', Ctrl: true}, false},
		{input.Token{Kind: input.TokenText, Rune: 'b'}, true},
		{input.Token{Kind: input.TokenEdit, Key: input.KeyB}, false},
		{input.Token{Kind: input.TokenText, Rune: 'a'}, false},
	} {
		b, _ := zeroSelectionFixture(t, 2)
		b.battleState().Input.DragActive = true
		in := input.NewState()
		in.Kbd.SetKey(input.KeyAlt, tc.alt)
		in.EnqueueToken(tc.token)
		if b.serviceZeroDragKey(in) || in.PendingTokens() != 1 {
			t.Fatalf("claimed unrelated token %+v alt=%v", tc.token, tc.alt)
		}
	}
	b, _ := zeroSelectionFixture(t, 2)
	b.megamap.boxActive = true
	in := input.NewState()
	in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'W'})
	tail := input.Token{Kind: input.TokenText, Rune: 'a'}
	in.EnqueueToken(tail)
	if !b.serviceZeroDragKey(in) || !slices.Equal(in.PeekTokens(), []input.Token{tail}) {
		t.Fatal("megamap filter key did not preserve the queued tail")
	}
}

func TestZeroFactoryHundredBatchModifierScope(t *testing.T) {
	for _, enabled := range []int{0, 1} {
		b := &battleSession{shell: &gameShell{presentation: settings.Presentation{FactoryHundredBatch: enabled}}}
		for bits := 0; bits < 8; bits++ {
			mods := input.Modifiers{Shift: bits&1 != 0, Ctrl: bits&2 != 0, Alt: bits&4 != 0}
			want := 1
			if mods.Shift {
				want = 5
			}
			if enabled != 0 && mods.Ctrl && mods.Shift {
				want = 100
			}
			if mods.Alt {
				want = 20
			}
			for _, right := range []bool{false, true} {
				sign := 1
				if right {
					sign = -1
				}
				if got := b.factoryBuildDelta(mods, right); got != sign*want {
					t.Fatalf("batch=%d modifiers=%+v right=%v count=%d, want %d", enabled, mods, right, got, sign*want)
				}
				stockpile := 1
				if mods.Shift {
					stockpile = 5
				}
				if got := stockpileClickDelta(mods, right); got != sign*stockpile {
					t.Fatal("factory preference changed stockpile count")
				}
			}
		}
	}
}
