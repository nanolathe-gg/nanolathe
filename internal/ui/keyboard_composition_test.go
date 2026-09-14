package ui

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Ctrl tokens occupy their own authored quickkey domain [07 §2].
func TestComposedQuickKeysDoNotAliasUnmodifiedKeys(t *testing.T) {
	for _, tc := range []struct {
		key             input.Key
		composed, plain byte
	}{
		{input.KeyA, 0xaa, 'A'},
		{input.KeyZ, 0xc3, 'Z'},
		{input.Key0, 0xc4, '0'},
		{input.Key9, 0xcd, '9'},
		{input.KeyF1, 0xce, 0xe2},
		{input.KeyF12, 0xd9, 0xed},
	} {
		token := input.Token{Kind: input.TokenEdit, Key: tc.key, Ctrl: true}
		if !matrixQuickKeyMatchesToken(token, tc.composed) || matrixQuickKeyMatchesToken(token, tc.plain) {
			t.Fatalf("key %v did not retain its composed quickkey identity", tc.key)
		}
		if matrixKey(token) != input.KeyNone {
			t.Fatalf("composed key %v entered the unmodified navigation matrix", tc.key)
		}
	}
}
