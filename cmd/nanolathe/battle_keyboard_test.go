package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Character identity and Ctrl composition are fixed when the token enters the
// ring, while group recall's Shift/Alt queries remain live [07 R-CAM-01 §14].
func TestBattleShortcutTokenIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		token       input.Token
		live        input.Modifiers
		key         input.Key
		shift, ctrl bool
	}{
		{"lowercase n after Shift down", input.Token{Kind: input.TokenText, Rune: 'n'}, input.Modifiers{Shift: true, Ctrl: true}, input.KeyN, false, false},
		{"uppercase N after Shift up", input.Token{Kind: input.TokenText, Rune: 'N'}, input.Modifiers{}, input.KeyNone, false, false},
		{"uppercase T after Shift up", input.Token{Kind: input.TokenText, Rune: 'T'}, input.Modifiers{}, input.KeyT, true, false},
		{"lowercase t after Shift down", input.Token{Kind: input.TokenText, Rune: 't'}, input.Modifiers{Shift: true}, input.KeyT, false, false},
		{"label literal", input.Token{Kind: input.TokenText, Rune: '!'}, input.Modifiers{}, input.KeyBackquote, false, false},
		{"shifted comma has no case", input.Token{Kind: input.TokenText, Rune: '<'}, input.Modifiers{Shift: true}, input.KeyNone, true, false},
		{"composed Ctrl after release", input.Token{Kind: input.TokenEdit, Key: input.KeyA, Ctrl: true}, input.Modifiers{}, input.KeyA, false, true},
		{"digit holds live modifiers", input.Token{Kind: input.TokenText, Rune: '1'}, input.Modifiers{Shift: true, Alt: true}, input.Key1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input.StateFromSample(input.Sample{Modifiers: tc.live, ShortcutToken: tc.token, ShortcutTokenMode: true})
			kbd := battleShortcutKeyboard(in)
			for key := input.Key(1); key < input.KeyCount; key++ {
				if kbd.KeyDown(key) != (key == tc.key) {
					t.Fatalf("edge %v=%v, want key %v", key, kbd.KeyDown(key), tc.key)
				}
			}
			if kbd.HasShift() != tc.shift || kbd.KeyHeld(input.KeyCtrl) != tc.ctrl || kbd.KeyHeld(input.KeyAlt) != tc.live.Alt {
				t.Fatal("shortcut modifiers changed identity")
			}
			if in.Kbd.HasShift() != tc.live.Shift || in.Kbd.KeyHeld(input.KeyCtrl) != tc.live.Ctrl {
				t.Fatal("live held state mutated")
			}
		})
	}
}

// The residual table consumes one event and retains the ordered tail. A GUI
// claim owns its pass rather than admitting the next token too [07 §2].
func TestBattleResidualKeepsTokenTailAndPointer(t *testing.T) {
	in := input.NewState()
	in.ShortcutTokenMode = true
	in.Kbd.SetKey(input.KeyEqual, true)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	for _, r := range "=-" {
		in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
	}
	first := battleTokenInput(in, false)
	if first.ShortcutToken.Rune != '=' || in.PendingTokens() != 1 {
		t.Fatal("first token or retained tail lost")
	}
	if !first.Mouse.Pressed(input.MouseButtonLeft) || !in.Kbd.KeyDown(input.KeyEqual) {
		t.Fatal("pointer or live key edges mutated")
	}
	claimed := battleTokenInput(in, true)
	if claimed.ShortcutToken.Kind != input.TokenNone || in.PendingTokens() != 1 {
		t.Fatal("GUI claim consumed tail")
	}
	second := battleTokenInput(in, false)
	if second.ShortcutToken.Rune != '-' || in.PendingTokens() != 0 {
		t.Fatal("second token was lost")
	}
	empty := battleTokenInput(in, false)
	if battleShortcutKeyboard(empty).KeyDown(input.KeyEqual) {
		t.Fatal("production fell back to physical edge")
	}
	copy := input.StateFromSample(input.SampleFromState(second, 0, 640, 480))
	if !copy.ShortcutTokenMode || copy.ShortcutToken != second.ShortcutToken {
		t.Fatal("controller sample lost event identity")
	}
}
