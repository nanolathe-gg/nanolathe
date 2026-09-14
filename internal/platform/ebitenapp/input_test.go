package ebitenapp

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

func TestApplyInputPublishesObservedPointerTransitions(t *testing.T) {
	in := input.NewState()
	applyInput(in, sampledInput{x: 10, y: 20, timestamp: 7})
	if got, ok := in.CurrentPointer(); !ok || got.Kind != input.PointerEventNone || got.X != 10 || got.Y != 20 || got.Timestamp != 7 {
		t.Fatalf("initial current = %+v, valid=%v; want motion fallback", got, ok)
	}

	buttons := input.MouseButtons{Left: true, Right: true}
	modifiers := input.Modifiers{Shift: true, Ctrl: true}
	applyInput(in, sampledInput{x: 30, y: 40, buttons: buttons, modifiers: modifiers, timestamp: 8})
	left, ok := in.CurrentPointer()
	if !ok || left.Kind != input.LeftDown || left.X != 30 || left.Y != 40 || left.Modifiers != modifiers || left.Buttons != buttons || left.Timestamp != 8 {
		t.Fatalf("left observation = %+v, valid=%v", left, ok)
	}
	if !in.Mouse.Held(input.MouseButtonLeft) || !in.Mouse.Held(input.MouseButtonRight) {
		t.Fatal("live buttons were not updated before publication")
	}

	// Both changes were observed in the same polling pass. The next service
	// gets the second observed record; this order is not asserted as native
	// chronology because Ebiten did not provide one.
	applyInput(in, sampledInput{x: 31, y: 41, buttons: buttons, modifiers: modifiers, timestamp: 9})
	right, ok := in.CurrentPointer()
	if !ok || right.Kind != input.RightDown || right.X != 30 || right.Y != 40 || right.Modifiers != modifiers || right.Buttons != buttons || right.Timestamp != 8 {
		t.Fatalf("right observation = %+v, valid=%v", right, ok)
	}
}

func TestApplyInputDoesNotManufactureHeldEditorKeyTokens(t *testing.T) {
	in := input.NewState()
	first := sampledInput{}
	first.keys[input.KeyEscape] = true
	applyInput(in, first)
	tokens := in.DrainTokens()
	if len(tokens) != 1 || tokens[0] != (input.Token{Kind: input.TokenEdit, Key: input.KeyEscape}) {
		t.Fatalf("initial editor token = %#v", tokens)
	}

	applyInput(in, first)
	if tokens := in.DrainTokens(); len(tokens) != 0 {
		t.Fatalf("held editor key produced repeated tokens %#v", tokens)
	}
	if !in.Kbd.KeyHeld(input.KeyEscape) || in.Kbd.KeyDown(input.KeyEscape) {
		t.Fatal("held key state or edge is wrong after the second poll")
	}
}

func TestApplyInputQueuesNavigationTransitionsOnce(t *testing.T) {
	in := input.NewState()
	sample := sampledInput{characters: []rune{'x'}}
	sample.keys[input.KeyUp] = true
	sample.keys[input.KeyDown] = true
	applyInput(in, sample)
	got := in.PeekTokens()
	want := []input.Token{{Kind: input.TokenEdit, Key: input.KeyUp}, {Kind: input.TokenEdit, Key: input.KeyDown}, {Kind: input.TokenText, Rune: 'x'}}
	if len(got) != len(want) {
		t.Fatalf("tokens=%#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d=%#v, want %#v", i, got[i], want[i])
		}
	}
	in.DiscardTokens(1)
	sample.characters = nil
	applyInput(in, sample)
	if got := in.PeekTokens(); len(got) != 2 || got[0].Key != input.KeyDown || got[1].Rune != 'x' {
		t.Fatalf("retained tokens=%#v", got)
	}
}

func TestApplyInputKeepsPublishedRecordSeparateFromNewLiveState(t *testing.T) {
	in := input.NewState()
	applyInput(in, sampledInput{x: 1, y: 2, buttons: input.MouseButtons{Left: true}, modifiers: input.Modifiers{Ctrl: true}, timestamp: 3})
	first, ok := in.CurrentPointer()
	if !ok || first.Kind != input.LeftDown {
		t.Fatalf("first current = %+v, valid=%v", first, ok)
	}

	// Live mutations can happen after the host service has published its one
	// record. The next service, not another fetch, selects the next record.
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	in.Kbd.SetKey(input.KeyCtrl, false)
	in.Kbd.SetKey(input.KeyAlt, true)
	if got, ok := in.CurrentPointer(); !ok || got != first {
		t.Fatalf("current changed with live state: %+v, valid=%v", got, ok)
	}
	if in.Mouse.Held(input.MouseButtonLeft) || in.Kbd.KeyHeld(input.KeyCtrl) || !in.Kbd.KeyHeld(input.KeyAlt) {
		t.Fatal("newer live state was not retained independently")
	}
}

// Special-key initial transitions and Ctrl composition are established input
// tokens, independent of the unavailable native repeat chronology [07 §2].
func TestApplyInputProducesSpecialAndComposedTokens(t *testing.T) {
	for _, key := range []input.Key{input.KeyPrior, input.KeyNext, input.KeyInsert, input.KeyPause, input.KeyF1, input.KeyF12} {
		in := input.NewState()
		sample := sampledInput{}
		sample.keys[key] = true
		applyInput(in, sample)
		got := in.DrainTokens()
		if len(got) != 1 || got[0] != (input.Token{Kind: input.TokenEdit, Key: key}) {
			t.Fatalf("key %v tokens=%v", key, got)
		}
		if !in.ShortcutTokenMode {
			t.Fatal("production input did not select token mode")
		}
	}
	in := input.NewState()
	sample := sampledInput{modifiers: input.Modifiers{Ctrl: true}}
	sample.keys[input.KeyA], sample.keys[input.KeyCtrl] = true, true
	applyInput(in, sample)
	sample.modifiers.Ctrl = false
	sample.keys[input.KeyA], sample.keys[input.KeyCtrl] = false, false
	applyInput(in, sample)
	got := in.DrainTokens()
	if len(got) != 1 || got[0] != (input.Token{Kind: input.TokenEdit, Key: input.KeyA, Ctrl: true}) {
		t.Fatalf("composed history=%v", got)
	}
}

func TestApplyInputAltUsesSystemCharacterIdentity(t *testing.T) {
	in := input.NewState()
	sample := sampledInput{modifiers: input.Modifiers{Alt: true, Shift: true}, characters: []rune{'!'}}
	sample.keys[input.Key1], sample.keys[input.KeyAlt], sample.keys[input.KeyShift] = true, true, true
	applyInput(in, sample)
	got := in.DrainTokens()
	if len(got) != 1 || got[0] != (input.Token{Kind: input.TokenText, Rune: '1'}) {
		t.Fatalf("Alt digit=%v", got)
	}
}

func TestCtrlPunctuationKeepsPlainTokenIdentity(t *testing.T) {
	for _, tc := range []struct {
		key  input.Key
		text rune
	}{
		{input.KeyEqual, '='}, {input.KeyMinus, '-'},
		{input.KeyComma, ','}, {input.KeyPeriod, '.'}, {input.KeyBackquote, '`'},
	} {
		token, ok := translatedKeyToken(tc.key, input.Modifiers{Ctrl: true, Shift: true})
		if !ok || token != (input.Token{Kind: input.TokenText, Rune: tc.text}) {
			t.Fatalf("Ctrl punctuation %v = %+v, want plain %q [07 §2]", tc.key, token, tc.text)
		}
	}
}
