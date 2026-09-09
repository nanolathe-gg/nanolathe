package client

import "testing"

func TestDrawTextClippedKeepsLayoutInsidePrivateSurface(t *testing.T) {
	f := testFont()
	frame := make([]uint8, 8*4)
	// The A pen starts left of the private surface. Its right-hand pixels still
	// land at their ordinary coordinates; the rest remain untouched.
	drawTextClipped(frame, 8, 4, f, "A", 1, 2, 0, 7, 2, 1, 2, 2, nil)
	if frame[1+1*8] != 0 || frame[3+1*8] != 7 || frame[2+2*8] != 7 || frame[3+2*8] != 7 {
		t.Fatalf("private-surface clip wrote %v", frame)
	}
}
