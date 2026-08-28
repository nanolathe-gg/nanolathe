package input

import "testing"

func TestStateFromSamplePreservesExplicitEdges(t *testing.T) {
	tests := []struct {
		name        string
		sample      Sample
		wantDown    bool
		wantHeld    bool
		wantMouse   bool
		wantRelease bool
	}{
		{name: "idle", sample: Sample{}},
		{name: "held without edge", sample: Sample{HeldKeys: []Key{KeyEscape}, Buttons: MouseButtons{Left: true}}, wantHeld: true, wantMouse: true},
		{name: "press", sample: Sample{PressedKeys: []Key{KeyEscape}, HeldKeys: []Key{KeyEscape}, Buttons: MouseButtons{Left: true}, PressedButtons: [4]bool{false, true}}, wantDown: true, wantHeld: true, wantMouse: true},
		{name: "release", sample: Sample{ReleasedButtons: [4]bool{false, true}}, wantRelease: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := StateFromSample(tt.sample)
			if got := in.Kbd.KeyDown(KeyEscape); got != tt.wantDown {
				t.Fatalf("escape KeyDown=%v want %v", got, tt.wantDown)
			}
			if got := in.Kbd.KeyHeld(KeyEscape); got != tt.wantHeld {
				t.Fatalf("escape KeyHeld=%v want %v", got, tt.wantHeld)
			}
			if got := in.Mouse.Held(MouseButtonLeft); got != tt.wantMouse {
				t.Fatalf("left Held=%v want %v", got, tt.wantMouse)
			}
			if got := in.Mouse.Pressed(MouseButtonLeft); got != (tt.name == "press") {
				t.Fatalf("left Pressed=%v want %v", got, tt.name == "press")
			}
			if got := in.Mouse.Released(MouseButtonLeft); got != tt.wantRelease {
				t.Fatalf("left Released=%v want %v", got, tt.wantRelease)
			}
		})
	}
}
