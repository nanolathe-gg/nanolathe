package numeric

// Integer is the signed integer set the shared arithmetic helpers accept.
// Unsigned types are excluded on purpose: the helpers below encode sign
// handling, and an unsigned caller with wrap semantics wants its own form.
type Integer interface {
	~int | ~int32 | ~int64
}

// FloorDiv returns floor(a/b) with sign correction: Go's `/` truncates toward
// zero, while retail's arithmetic shift with sign correction floors, so a
// negative dividend with a remainder rounds one further down [03 §2.1] [I3].
// Division by zero panics exactly as `/` does.
func FloorDiv[T Integer](a, b T) T {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Abs returns the magnitude of v. The most negative value of the type wraps
// to itself, as it does with `-v`.
func Abs[T Integer](v T) T {
	if v < 0 {
		return -v
	}
	return v
}

// ISqrt64 is the floor of the square root of a non-negative value; a negative
// or zero radicand yields zero. Retail forms these magnitudes on the
// double-precision stack and truncates toward zero; for an exact integer
// radicand the two agree, and the integer form keeps authoritative state out
// of floating point [I2].
func ISqrt64(v int64) int64 {
	if v <= 0 {
		return 0
	}
	// Refine one base-four digit per step. The first digit is lowered to the
	// radicand's magnitude so that root+digit never overflows for v >= 2^62.
	digit := int64(1) << 62
	for digit > v {
		digit >>= 2
	}
	root := int64(0)
	for digit != 0 {
		if v >= root+digit {
			v -= root + digit
			root = (root >> 1) + digit
		} else {
			root >>= 1
		}
		digit >>= 2
	}
	return root
}
