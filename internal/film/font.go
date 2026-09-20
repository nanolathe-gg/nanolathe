package film

// A monoline stroke face. Every glyph is a set of polylines on the cap box:
// x grows right from the pen, y grows DOWN from the cap line, so y = 0 is the
// cap top and y = 1 the baseline. Descenders run past 1. One unit is the cap
// height, so a glyph scales to any capture resolution by multiplication alone.
//
// The face is upper case only. Lower-case input is folded before lookup, which
// is what a title card wants and keeps the table to one alphabet.

type point struct{ X, Y float64 }

type glyph struct {
	advance float64 // pen movement in cap heights, before tracking
	strokes [][]point
}

const (
	defaultAdvance = 0.68
	narrowAdvance  = 0.32
)

func p(x, y float64) point { return point{X: x, Y: y} }

// wide is the common case: a 0.6-wide drawing on a 0.68 advance.
func wide(strokes ...[]point) glyph { return glyph{advance: defaultAdvance, strokes: strokes} }

func narrow(strokes ...[]point) glyph { return glyph{advance: narrowAdvance, strokes: strokes} }

// oval is the shared O/0/Q bowl, as an eight-point closed ring.
func oval() []point {
	return []point{p(0.30, 0.00), p(0.08, 0.17), p(0.01, 0.50), p(0.08, 0.83),
		p(0.30, 1.00), p(0.52, 0.83), p(0.59, 0.50), p(0.52, 0.17), p(0.30, 0.00)}
}

var face = map[rune]glyph{
	'A': wide([]point{p(0.00, 1.00), p(0.30, 0.00), p(0.60, 1.00)}, []point{p(0.11, 0.63), p(0.49, 0.63)}),
	'B': wide([]point{p(0.02, 0.00), p(0.02, 1.00)},
		[]point{p(0.02, 0.00), p(0.40, 0.00), p(0.55, 0.14), p(0.55, 0.36), p(0.40, 0.48), p(0.02, 0.48)},
		[]point{p(0.02, 0.48), p(0.44, 0.48), p(0.59, 0.62), p(0.59, 0.86), p(0.44, 1.00), p(0.02, 1.00)}),
	'C': wide([]point{p(0.58, 0.20), p(0.44, 0.02), p(0.20, 0.00), p(0.04, 0.22), p(0.01, 0.50),
		p(0.04, 0.78), p(0.20, 1.00), p(0.44, 0.98), p(0.58, 0.80)}),
	'D': wide([]point{p(0.02, 0.00), p(0.02, 1.00)},
		[]point{p(0.02, 0.00), p(0.33, 0.00), p(0.56, 0.22), p(0.59, 0.50), p(0.56, 0.78), p(0.33, 1.00), p(0.02, 1.00)}),
	'E': wide([]point{p(0.56, 0.00), p(0.02, 0.00), p(0.02, 1.00), p(0.56, 1.00)}, []point{p(0.02, 0.49), p(0.44, 0.49)}),
	'F': wide([]point{p(0.56, 0.00), p(0.02, 0.00), p(0.02, 1.00)}, []point{p(0.02, 0.49), p(0.44, 0.49)}),
	'G': wide([]point{p(0.58, 0.20), p(0.44, 0.02), p(0.20, 0.00), p(0.04, 0.22), p(0.01, 0.50),
		p(0.04, 0.78), p(0.20, 1.00), p(0.44, 0.98), p(0.58, 0.78), p(0.58, 0.55), p(0.34, 0.55)}),
	'H': wide([]point{p(0.02, 0.00), p(0.02, 1.00)}, []point{p(0.58, 0.00), p(0.58, 1.00)}, []point{p(0.02, 0.49), p(0.58, 0.49)}),
	'I': narrow([]point{p(0.16, 0.00), p(0.16, 1.00)}),
	'J': wide([]point{p(0.52, 0.00), p(0.52, 0.78), p(0.38, 0.99), p(0.16, 0.99), p(0.02, 0.80)}),
	'K': wide([]point{p(0.02, 0.00), p(0.02, 1.00)}, []point{p(0.56, 0.00), p(0.06, 0.54)}, []point{p(0.22, 0.38), p(0.59, 1.00)}),
	'L': wide([]point{p(0.02, 0.00), p(0.02, 1.00), p(0.54, 1.00)}),
	'M': wide([]point{p(0.00, 1.00), p(0.00, 0.00), p(0.30, 0.56), p(0.60, 0.00), p(0.60, 1.00)}),
	'N': wide([]point{p(0.02, 1.00), p(0.02, 0.00), p(0.58, 1.00), p(0.58, 0.00)}),
	'O': wide(oval()),
	'P': wide([]point{p(0.02, 1.00), p(0.02, 0.00)},
		[]point{p(0.02, 0.00), p(0.42, 0.00), p(0.58, 0.16), p(0.58, 0.38), p(0.42, 0.53), p(0.02, 0.53)}),
	'Q': wide(oval(), []point{p(0.36, 0.72), p(0.62, 1.06)}),
	'R': wide([]point{p(0.02, 1.00), p(0.02, 0.00)},
		[]point{p(0.02, 0.00), p(0.42, 0.00), p(0.58, 0.16), p(0.58, 0.36), p(0.42, 0.51), p(0.02, 0.51)},
		[]point{p(0.28, 0.51), p(0.59, 1.00)}),
	'S': wide([]point{p(0.57, 0.16), p(0.42, 0.01), p(0.17, 0.01), p(0.03, 0.17), p(0.05, 0.36),
		p(0.22, 0.45), p(0.42, 0.53), p(0.57, 0.66), p(0.55, 0.87), p(0.38, 0.99), p(0.14, 0.98), p(0.02, 0.84)}),
	'T': wide([]point{p(0.00, 0.01), p(0.60, 0.01)}, []point{p(0.30, 0.01), p(0.30, 1.00)}),
	'U': wide([]point{p(0.02, 0.00), p(0.02, 0.74), p(0.18, 0.98), p(0.42, 0.98), p(0.58, 0.74), p(0.58, 0.00)}),
	'V': wide([]point{p(0.00, 0.00), p(0.30, 1.00), p(0.60, 0.00)}),
	'W': wide([]point{p(0.00, 0.00), p(0.13, 1.00), p(0.30, 0.42), p(0.47, 1.00), p(0.60, 0.00)}),
	'X': wide([]point{p(0.01, 0.00), p(0.59, 1.00)}, []point{p(0.59, 0.00), p(0.01, 1.00)}),
	'Y': wide([]point{p(0.01, 0.00), p(0.30, 0.52), p(0.59, 0.00)}, []point{p(0.30, 0.52), p(0.30, 1.00)}),
	'Z': wide([]point{p(0.02, 0.01), p(0.58, 0.01), p(0.02, 0.99), p(0.58, 0.99)}),

	'0': wide(oval(), []point{p(0.46, 0.22), p(0.14, 0.78)}),
	'1': wide([]point{p(0.10, 0.18), p(0.30, 0.01), p(0.30, 1.00)}, []point{p(0.08, 1.00), p(0.52, 1.00)}),
	'2': wide([]point{p(0.04, 0.20), p(0.20, 0.01), p(0.42, 0.01), p(0.57, 0.20), p(0.48, 0.44), p(0.02, 1.00), p(0.58, 1.00)}),
	'3': wide([]point{p(0.04, 0.16), p(0.20, 0.01), p(0.44, 0.03), p(0.54, 0.24), p(0.30, 0.47)},
		[]point{p(0.30, 0.47), p(0.56, 0.64), p(0.48, 0.93), p(0.22, 0.99), p(0.03, 0.87)}),
	'4': wide([]point{p(0.44, 1.00), p(0.44, 0.01), p(0.01, 0.72), p(0.59, 0.72)}),
	'5': wide([]point{p(0.55, 0.01), p(0.12, 0.01), p(0.07, 0.42), p(0.32, 0.36), p(0.55, 0.52), p(0.52, 0.86), p(0.26, 0.99), p(0.03, 0.89)}),
	'6': wide([]point{p(0.50, 0.04), p(0.22, 0.05), p(0.05, 0.32), p(0.02, 0.72), p(0.16, 0.97), p(0.40, 0.98), p(0.56, 0.80), p(0.46, 0.56), p(0.16, 0.52), p(0.03, 0.66)}),
	'7': wide([]point{p(0.02, 0.01), p(0.58, 0.01), p(0.24, 1.00)}),
	'8': wide([]point{p(0.30, 0.47), p(0.10, 0.36), p(0.09, 0.14), p(0.30, 0.01), p(0.51, 0.14), p(0.50, 0.36), p(0.30, 0.47),
		p(0.07, 0.62), p(0.06, 0.86), p(0.30, 0.99), p(0.54, 0.86), p(0.53, 0.62), p(0.30, 0.47)}),
	'9': wide([]point{p(0.10, 0.96), p(0.38, 0.95), p(0.55, 0.68), p(0.58, 0.28), p(0.44, 0.03), p(0.20, 0.02), p(0.04, 0.20), p(0.14, 0.44), p(0.44, 0.48), p(0.57, 0.34)}),

	' ':  {advance: 0.42},
	'.':  narrow([]point{p(0.14, 0.97), p(0.16, 0.97)}),
	',':  narrow([]point{p(0.18, 0.94), p(0.09, 1.14)}),
	':':  narrow([]point{p(0.15, 0.36), p(0.17, 0.36)}, []point{p(0.15, 0.97), p(0.17, 0.97)}),
	';':  narrow([]point{p(0.17, 0.36), p(0.19, 0.36)}, []point{p(0.18, 0.94), p(0.09, 1.14)}),
	'-':  glyph{advance: 0.52, strokes: [][]point{{p(0.06, 0.54), p(0.42, 0.54)}}},
	'\'': narrow([]point{p(0.16, 0.02), p(0.13, 0.26)}),
	'"':  glyph{advance: 0.44, strokes: [][]point{{p(0.11, 0.02), p(0.08, 0.26)}, {p(0.29, 0.02), p(0.26, 0.26)}}},
	'!':  narrow([]point{p(0.16, 0.01), p(0.16, 0.66)}, []point{p(0.16, 0.97), p(0.18, 0.97)}),
	'?': wide([]point{p(0.06, 0.19), p(0.22, 0.01), p(0.44, 0.03), p(0.55, 0.22), p(0.44, 0.42), p(0.30, 0.52), p(0.30, 0.68)},
		[]point{p(0.30, 0.97), p(0.32, 0.97)}),
	'/': glyph{advance: 0.54, strokes: [][]point{{p(0.02, 1.02), p(0.46, -0.02)}}},
	'(': glyph{advance: 0.36, strokes: [][]point{{p(0.26, -0.02), p(0.08, 0.28), p(0.08, 0.74), p(0.26, 1.04)}}},
	')': glyph{advance: 0.36, strokes: [][]point{{p(0.08, -0.02), p(0.26, 0.28), p(0.26, 0.74), p(0.08, 1.04)}}},
	'&': wide([]point{p(0.59, 1.00), p(0.16, 0.30), p(0.16, 0.13), p(0.30, 0.01), p(0.44, 0.13), p(0.44, 0.28),
		p(0.04, 0.62), p(0.04, 0.85), p(0.22, 1.00), p(0.44, 0.90), p(0.52, 0.66)}),
	'+': glyph{advance: 0.60, strokes: [][]point{{p(0.06, 0.52), p(0.50, 0.52)}, {p(0.28, 0.30), p(0.28, 0.74)}}},
	'=': glyph{advance: 0.60, strokes: [][]point{{p(0.06, 0.40), p(0.50, 0.40)}, {p(0.06, 0.64), p(0.50, 0.64)}}},
	'_': glyph{advance: 0.60, strokes: [][]point{{p(0.00, 1.06), p(0.54, 1.06)}}},
	'%': wide([]point{p(0.02, 1.00), p(0.52, 0.00)},
		[]point{p(0.02, 0.20), p(0.13, 0.06), p(0.24, 0.20), p(0.13, 0.34), p(0.02, 0.20)},
		[]point{p(0.30, 0.82), p(0.41, 0.68), p(0.52, 0.82), p(0.41, 0.96), p(0.30, 0.82)}),
	'*': glyph{advance: 0.48, strokes: [][]point{{p(0.20, 0.06), p(0.20, 0.42)}, {p(0.05, 0.15), p(0.35, 0.33)}, {p(0.35, 0.15), p(0.05, 0.33)}}},
}

// lookupGlyph folds case and reports the drawable glyph. An unmapped rune
// advances as a space rather than vanishing, so a title never silently loses
// its spacing to one unsupported character.
func lookupGlyph(r rune) (glyph, bool) {
	if r >= 'a' && r <= 'z' {
		r -= 'a' - 'A'
	}
	g, ok := face[r]
	if !ok {
		return face[' '], false
	}
	return g, true
}
