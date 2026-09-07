package content

import "testing"

func TestCanonicalKeyUsesTDFTrimAndASCIIFold(t *testing.T) {
	if got := CanonicalKey(" \t\r\nMiXeD\n\r\t "); got != "mixed" {
		t.Fatalf("CanonicalKey ASCII = %q, want mixed", got)
	}
	if got := CanonicalKey("\vMiXeD\f"); got != "\vmixed\f" {
		t.Fatalf("CanonicalKey nonsemantic whitespace = %q, want vertical/form-feed retained", got)
	}
	if a, b := CanonicalKey("\xc0"), CanonicalKey("\xe0"); a == b {
		t.Fatalf("high-byte keys collapsed: %q and %q", a, b)
	}
}
