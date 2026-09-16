// The ASCII folding and trimming primitives the authored formats share.

package formats

// FoldASCII lowercases only the A..Z bytes and returns the value unchanged
// when it holds none. Bytes at or above 0x80 are left literal because the
// active code page retail compared them under is untraced [02 R-CAT-01 §3].
// Callers keep their own citation for the grammar they fold for.
// TODO(question): trace retail's active code-page comparison for bytes >= 0x80.
func FoldASCII(value string) string {
	for i := 0; i < len(value); i++ {
		if value[i] >= 'A' && value[i] <= 'Z' {
			out := []byte(value)
			for j := i; j < len(out); j++ {
				if out[j] >= 'A' && out[j] <= 'Z' {
					out[j] += 'a' - 'A'
				}
			}
			return string(out)
		}
	}
	return value
}

// FoldASCIIByte is the single-byte form of FoldASCII: A..Z fold, every other
// byte compares only with itself.
func FoldASCIIByte(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

// TrimASCIIFunc removes the leading and trailing bytes isSpace accepts
// [02 R-CAT-01 §3]. Only the scan is shared: each grammar supplies its own
// whitespace predicate because the retail parsers do not agree on the set.
func TrimASCIIFunc(value string, isSpace func(byte) bool) string {
	start, end := 0, len(value)
	for start < end && isSpace(value[start]) {
		start++
	}
	for end > start && isSpace(value[end-1]) {
		end--
	}
	return value[start:end]
}

// findByFoldedName returns the first entry whose name matches under ASCII
// letter folding, preserving authored table order. The archive index and its
// metadata view hold different entry types over the same comparison
// [02 "Animation archive (GAF)"][fmt gaf].
func findByFoldedName[T any](entries []T, name string, nameOf func(*T) string) (*T, bool) {
	for i := range entries {
		if asciiEqualFolded(nameOf(&entries[i]), name) {
			return &entries[i], true
		}
	}
	return nil, false
}

func asciiEqualFolded(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if FoldASCIIByte(a[i]) != FoldASCIIByte(b[i]) {
			return false
		}
	}
	return true
}
