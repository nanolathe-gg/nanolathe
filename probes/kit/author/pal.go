package author

// ALP writer — research/formats/pal.md: 65,536 raw bytes, no header,
// result_index = ALP[a*256 + b] for the ordered operand pair (a, b).

// ALPBytes builds a blend table from f(a, b).
func ALPBytes(f func(a, b byte) byte) []byte {
	t := make([]byte, 65536)
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			t[a*256+b] = f(byte(a), byte(b))
		}
	}
	return t
}
