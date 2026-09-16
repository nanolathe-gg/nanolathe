package client

import "testing"

// [03 R-COMP-01 §2] Ties step both axes after clipping, independent of the
// supplied direction for wholly admitted segments. Pixels are physical bytes.
func TestIndexedLineRetailRaster(t *testing.T) {
	for _, tc := range []struct {
		name           string
		x0, y0, x1, y1 int32
		want           [][2]int
	}{
		{"shallow tie", 0, 0, 2, 1, [][2]int{{0, 0}, {1, 1}, {2, 1}}},
		{"reversed shallow tie", 2, 1, 0, 0, [][2]int{{0, 0}, {1, 1}, {2, 1}}},
		{"steep tie", 0, 0, 1, 2, [][2]int{{0, 0}, {1, 1}, {1, 2}}},
		{"descending tie", 0, 2, 2, 1, [][2]int{{0, 2}, {1, 1}, {2, 1}}},
		{"left clip restarts error", -1, 0, 3, 2, [][2]int{{0, 0}, {1, 1}, {2, 1}, {3, 2}}},
		{"left then top keeps slope", -2, -2, 3, 2, [][2]int{{1, 0}, {2, 1}, {3, 2}}},
		{"right clip truncates", 0, 0, 6, 3, [][2]int{{0, 0}, {1, 1}, {2, 1}, {3, 2}, {4, 2}}},
		{"bottom clip truncates", 0, 0, 3, 6, [][2]int{{0, 0}, {1, 1}, {1, 2}, {2, 3}, {2, 4}}},
		{"vertical", 1, 6, 1, -2, [][2]int{{1, 0}, {1, 1}, {1, 2}, {1, 3}, {1, 4}}},
		{"horizontal", 6, 1, -2, 1, [][2]int{{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 1}}},
		{"single pixel", 2, 2, 2, 2, [][2]int{{2, 2}}},
		{"outside", -3, -3, -1, -1, nil},
		{"no horizontal extent", -1, 0, -1, 3, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{width: 5, height: 5, indexed: make([]byte, 25)}
			c.drawIndexedLine(tc.x0, tc.y0, tc.x1, tc.y1, 91)
			want := make([]byte, 25)
			for _, p := range tc.want {
				want[p[1]*5+p[0]] = 91
			}
			for i, b := range c.indexed {
				if b != want[i] {
					t.Fatalf("pixel (%d,%d) = %d, want %d; raster %v", i%5, i/5, b, want[i], c.indexed)
				}
			}
		})
	}
}
