package client

import "testing"

// Admission ignores the baseline, requires one-pixel slack at right/bottom,
// and rejects the whole string on any edge [03 R-FONT-01 §3].
func TestDrawTextWholeStringAdmission(t *testing.T) {
	for _, tc := range []struct {
		name           string
		x, y, maxWidth int
		text           string
		want           bool
	}{
		{"inside", 2, 2, 0, "A", true},
		{"inclusive one-past bounds", 3, 3, 0, "A", true},
		{"left", 1, 2, 0, "A", false},
		{"top", 2, 1, 0, "A", false},
		{"right last pixel", 4, 2, 0, "A", false},
		{"bottom last pixel", 2, 4, 0, "A", false},
		{"truncate first", 2, 2, 3, "AB", true},
		{"whole run", 2, 2, 0, "ABB", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := make([]byte, 8*8)
			drawTextClipped(frame, 8, 8, testFont(), tc.text, tc.x, tc.y, tc.maxWidth, 7, 2, 2, 5, 4)
			writes := 0
			for _, p := range frame {
				if p != 0 {
					writes++
				}
			}
			if (writes != 0) != tc.want {
				t.Fatalf("writes=%d, admitted=%v", writes, tc.want)
			}
			if tc.want && frame[tc.x+(tc.y-1)*8] != 7 {
				t.Fatal("admitted baseline overrun above private clip was lost")
			}
		})
	}
}

func TestDrawTextBaselineOverrunBoundsHostStorage(t *testing.T) {
	f := testFont()
	frame := make([]byte, 8*4)
	drawTextClipped(frame, 8, 4, f, "A", 1, 0, 0, 7, 0, 0, 8, 4)
	// The top glyph row lies outside storage; the second retains its original
	// row rather than shifting the glyph down as a whole [03 R-FONT-01 §3][03 R-FONT-01 §4].
	if frame[1] != 0 || frame[2] != 7 || frame[3] != 7 {
		t.Fatalf("baseline crop=%v", frame)
	}
}
