package palette

// The derived-table builders share the sum-sorted nearest-color lookup with
// gray/blue generation [03 §4.3.4]. Stored authored tables still take precedence.
func buildAlphaTable(t *Tables) {
	sums, perm := sortPaletteBySum(t)
	for a := range t.Base {
		for b := range t.Base {
			if a == b {
				t.Alpha[a*256+b] = byte(a)
				continue
			}
			x, y := t.Base[a], t.Base[b]
			t.Alpha[a*256+b] = nearestBySum(t, sums, perm,
				(int(x[0])+int(y[0]))/2, (int(x[1])+int(y[1]))/2, (int(x[2])+int(y[2]))/2)
		}
	}
}

func buildLightTable(t *Tables) {
	sums, perm := sortPaletteBySum(t)
	for row := 0; row < 32; row++ {
		// Separate rounded multiplication and subtraction; do not fuse them
		// or replace the stored negative constant with an exact ratio [03 §4.3.4].
		product := float64(float64(row) * -0.03333333333333333)
		factor := 1.0 - product
		for i, color := range t.Base {
			t.Light[row*256+i] = nearestBySum(t, sums, perm,
				min(int(float64(color[0])*factor), 255),
				min(int(float64(color[1])*factor), 255),
				min(int(float64(color[2])*factor), 255))
		}
	}
}

func buildShadeTable(t *Tables) {
	sums, perm := sortPaletteBySum(t)
	factor := float64(0)
	for row := range t.Shade {
		for i, color := range t.Base {
			// Channel products are finite and below 544. The unsigned-word
			// comparison therefore agrees with this clamp [03 §4.3.4].
			t.Shade[row][i] = nearestBySum(t, sums, perm,
				min(int(float64(color[0])*factor), 255),
				min(int(float64(color[1])*factor), 255),
				min(int(float64(color[2])*factor), 255))
		}
		// The stored factor is advanced per row, not recomputed as row*step.
		factor -= -0.06875
	}
}
