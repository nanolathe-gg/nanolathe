// Package numeric contains the authoritative integer numeric types used by the
// simulation. Renderer-facing conversions belong outside this package.
//
// Two rounding rules coexist here and they are not interchangeable (I3):
//
//   - Narrowing a floating-point value to an integer truncates toward zero,
//     because retail routes those through the compiler's __ftol helper, which
//     sets round-control to truncate for the store [01 §8].
//   - Shifting a fixed-point intermediate down floors, because retail does it
//     with an arithmetic shift. The two disagree on every negative value with a
//     nonzero fraction, which is the map's west and north edges [03 §2.1].
package numeric
