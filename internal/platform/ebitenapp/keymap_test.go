package ebitenapp

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Keypad Enter is the same 0x0D character as Return in retail [07 §2][07 §5
// "Chat"]; Ebitengine reports it as a distinct key, so both drive KeyEnter.
// No other portable key gains an alias.
func TestKeypadEnterDrivesEnter(t *testing.T) {
	keys, n := ebitenKeys(input.KeyEnter)
	if n != 2 || keys[0] != ebiten.KeyEnter || keys[1] != ebiten.KeyNumpadEnter {
		t.Fatalf("Enter keys = %v", keys[:n])
	}
	for key := input.Key(1); key < input.KeyCount; key++ {
		keys, n := ebitenKeys(key)
		for _, ek := range keys[:n] {
			if ek == ebiten.KeyNumpadEnter && key != input.KeyEnter {
				t.Fatalf("keypad Enter also drives key %d", key)
			}
		}
		if key != input.KeyEnter && n > 1 {
			t.Fatalf("key %d has %d physical keys", key, n)
		}
	}
}
