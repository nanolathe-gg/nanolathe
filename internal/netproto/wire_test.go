package netproto

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

// read runs f over b and returns the reader's failure; a successful read must
// consume every byte.
func read(t *testing.T, b []byte, f func(*Reader)) error {
	t.Helper()
	r := NewReader(b, nil)
	f(r)
	if r.Err() == nil && r.Offset() != len(b) {
		t.Fatalf("%x: %d bytes left", b, len(b)-r.Offset())
	}
	return r.Err()
}

// uvarintBytes is a test-side varint, written independently of the writer.
func uvarintBytes(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

// Unsigned integers are the shortest base-128 varint within their named
// width; overflow and overlong encodings are refused (§7.4.1).
func TestUnsignedVarints(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		bits uint
		want uint64
		ok   bool
	}{
		{"zero", []byte{0}, 16, 0, true},
		{"127", []byte{0x7f}, 16, 127, true},
		{"128", []byte{0x80, 0x01}, 16, 128, true},
		{"u16 max", []byte{0xff, 0xff, 0x03}, 16, 65535, true},
		{"u16 overflow", []byte{0x80, 0x80, 0x04}, 16, 0, false},
		{"u32 max", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, 32, 1<<32 - 1, true},
		{"u32 overflow", []byte{0x80, 0x80, 0x80, 0x80, 0x10}, 32, 0, false},
		{"u64 max", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, 64, 1<<64 - 1, true},
		{"u64 overflow", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}, 64, 0, false},
		{"eleven bytes", []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, 64, 0, false},
		{"overlong zero", []byte{0x80, 0x00}, 16, 0, false},
		{"overlong one", []byte{0x81, 0x80, 0x00}, 32, 0, false},
		{"unterminated", []byte{0x80}, 32, 0, false},
		{"empty", nil, 32, 0, false},
	} {
		var got uint64
		err := read(t, c.b, func(r *Reader) {
			switch c.bits {
			case 16:
				got = uint64(r.U16())
			case 32:
				got = uint64(r.U32())
			default:
				got = r.U64()
			}
		})
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s: got %d, %v", c.name, got, err)
		}
		if c.ok {
			var w Writer
			w.U64(c.want)
			if !bytes.Equal(w.Bytes(), c.b) || UvarintLen(c.want) != len(c.b) {
				t.Errorf("%s: %d writes %x (length %d), want %x", c.name, c.want, w.Bytes(), UvarintLen(c.want), c.b)
			}
		}
	}
}

// Signed integers are the zigzag of the named width, never narrowed through
// Go int.
func TestSignedZigzag(t *testing.T) {
	for _, c := range []struct {
		b  []byte
		v  int32
		ok bool
	}{{[]byte{0}, 0, true}, {[]byte{1}, -1, true}, {[]byte{2}, 1, true}, {[]byte{0xfe, 0xff, 0xff, 0xff, 0x0f}, 2147483647, true}, {[]byte{0xff, 0xff, 0xff, 0xff, 0x0f}, -2147483648, true}, {[]byte{0x80, 0x80, 0x80, 0x80, 0x10}, 0, false}} {
		var got int32
		err := read(t, c.b, func(r *Reader) { got = r.S32() })
		if (err == nil) != c.ok || got != c.v {
			t.Errorf("s32 %x: got %d, %v", c.b, got, err)
		}
		if c.ok {
			var w Writer
			w.S32(c.v)
			if !bytes.Equal(w.Bytes(), c.b) {
				t.Errorf("s32 %d writes %x, want %x", c.v, w.Bytes(), c.b)
			}
		}
	}
	for _, v := range []int64{0, -1, 1, math.MaxInt32 + 1, math.MinInt32 - 1, math.MaxInt64, math.MinInt64} {
		var w Writer
		w.S64(v)
		var got int64
		if err := read(t, w.Bytes(), func(r *Reader) { got = r.S64() }); err != nil || got != v {
			t.Errorf("s64 %d: got %d, %v", v, got, err)
		}
	}
	// The extremes take the full ten bytes the size proof counts (§7.4.1).
	var w Writer
	w.S64(math.MinInt64)
	if w.Len() != 10 {
		t.Fatalf("MinInt64 takes %d bytes, want 10", w.Len())
	}
}

// A boolean is exactly 0 or 1.
func TestBooleans(t *testing.T) {
	for _, c := range []struct {
		b  byte
		ok bool
	}{{0, true}, {1, true}, {2, false}, {0xff, false}} {
		if err := read(t, []byte{c.b}, func(r *Reader) { r.Bool() }); (err == nil) != c.ok {
			t.Errorf("boolean %d: %v", c.b, err)
		}
	}
}

// Text is a u32 length within its bound, then valid UTF-8 without NUL; a key
// is a u16 length of 1..255 bytes without NUL, kept literally; an optional key
// also admits length 0. Lengths are checked before the bytes are taken.
func TestTextAndKeys(t *testing.T) {
	if err := read(t, append([]byte{17}, strings.Repeat("a", 17)...), func(r *Reader) { r.Text(16) }); err == nil {
		t.Error("a 17-byte text(16) was read")
	}
	if err := read(t, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, func(r *Reader) { r.Text(1 << 20) }); err == nil {
		t.Error("a text longer than the payload was read")
	}
	for _, bad := range []string{"a\x00b", "\xff", "\xc3"} {
		if err := read(t, append([]byte{byte(len(bad))}, bad...), func(r *Reader) { r.Text(16) }); err == nil {
			t.Errorf("text %q was read", bad)
		}
	}
	var got string
	if err := read(t, append([]byte{4}, "héé"[:4]...), func(r *Reader) { got = r.Text(4) }); err == nil {
		t.Errorf("a text cut inside a character was read as %q", got)
	}
	if err := read(t, append([]byte{5}, "héé"...), func(r *Reader) { got = r.Text(5) }); err != nil || got != "héé" {
		t.Errorf("text: %q, %v", got, err)
	}
	if err := read(t, []byte{0}, func(r *Reader) { r.Key() }); err == nil {
		t.Error("an empty key was read")
	}
	if err := read(t, []byte{0}, func(r *Reader) { got = r.OptionalKey() }); err != nil || got != "" {
		t.Errorf("an empty optional key: %q, %v", got, err)
	}
	if err := read(t, append(uvarintBytes(256), strings.Repeat("k", 256)...), func(r *Reader) { r.Key() }); err == nil {
		t.Error("a 256-byte key was read")
	}
	if err := read(t, append([]byte{3}, "a\x00b"...), func(r *Reader) { r.OptionalKey() }); err == nil {
		t.Error("a key with NUL was read")
	}
	if err := read(t, append([]byte{2}, "\xff\xfe"...), func(r *Reader) { got = r.Key() }); err != nil || got != "\xff\xfe" {
		t.Errorf("a key's high bytes: %q, %v", got, err)
	}
	var w Writer
	w.Key(strings.Repeat("k", MaxKeyBytes))
	w.Text("text")
	r := NewReader(w.Bytes(), nil)
	if r.Key() != strings.Repeat("k", MaxKeyBytes) || r.Text(4) != "text" || r.End() != nil {
		t.Fatalf("key and text round trip: %v", r.Err())
	}
}

// A digest is exactly 32 bytes, an id 16, an amount four little-endian bytes
// of binary32; online, an amount is finite and canonical, the encoder
// spelling negative zero as positive zero (§7.4.1).
func TestFixedWidthFields(t *testing.T) {
	var d [DigestBytes]byte
	var id [IDBytes]byte
	for i := range d {
		d[i] = byte(i + 1)
	}
	for i := range id {
		id[i] = byte(0xf0 - i)
	}
	var w Writer
	w.Digest(d)
	w.ID(id)
	w.Amount(math.Float32bits(-2.5))
	w.Raw([]byte{9, 8})
	if w.Len() != DigestBytes+IDBytes+AmountBytes+2 || !bytes.Equal(w.Bytes()[48:52], []byte{0, 0, 0x20, 0xc0}) {
		t.Fatalf("fixed-width layout %x", w.Bytes())
	}
	r := NewReader(w.Bytes(), nil)
	if r.Digest() != d || r.ID() != id || r.Amount() != math.Float32bits(-2.5) || !bytes.Equal(r.Raw(2), []byte{9, 8}) || r.End() != nil {
		t.Fatalf("fixed-width round trip: %v", r.Err())
	}
	for _, n := range []int{0, 31} {
		if err := read(t, make([]byte, n), func(r *Reader) { r.Digest() }); err == nil {
			t.Errorf("a %d-byte digest was read", n)
		}
	}
	if err := read(t, make([]byte, 3), func(r *Reader) { r.Amount() }); err == nil {
		t.Error("a three-byte amount was read")
	}
	if CanonicalAmount(0x80000000) != 0 || CanonicalAmount(math.Float32bits(-1)) != math.Float32bits(-1) || CanonicalAmount(0x7fc00001) != 0x7fc00001 {
		t.Fatal("canonical amounts")
	}
	for _, c := range []struct {
		bits uint32
		ok   bool
	}{
		{0, true}, {0x80000000, false}, {math.Float32bits(-3), true}, {math.Float32bits(math.MaxFloat32), true},
		{math.Float32bits(float32(math.Inf(1))), false}, {math.Float32bits(float32(math.Inf(-1))), false}, {0x7fc00000, false}, {0xffc00001, false},
		{1, true}, // the smallest subnormal is finite
	} {
		if OnlineAmount(c.bits) != c.ok {
			t.Errorf("OnlineAmount(%#x) = %v", c.bits, !c.ok)
		}
	}
}

// A count is checked against its bound and against the bytes that remain,
// before the caller allocates from it.
func TestCountsAreBoundedBeforeAllocation(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		f    func(*Reader) int
		ok   bool
	}{
		{"within", []byte{2, 0, 0, 0, 0}, func(r *Reader) int { return r.Count(4, 2) }, true},
		{"over the bound", []byte{5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, func(r *Reader) int { return r.Count(4, 2) }, false},
		{"over the bytes left", []byte{3, 0, 0, 0, 0, 0}, func(r *Reader) int { return r.Count(4, 2) }, false},
		{"u16 count", []byte{0xff, 0xff, 0x03}, func(r *Reader) int { return r.Count16(65535, 0) }, true},
		{"u16 count overflow", []byte{0x80, 0x80, 0x04}, func(r *Reader) int { return r.Count16(1<<20, 0) }, false},
		{"huge claimed count", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, func(r *Reader) int { return r.Count(math.MaxInt32, 1) }, false},
	} {
		r := NewReader(c.b, nil)
		n := c.f(r)
		if (r.Err() == nil) != c.ok {
			t.Errorf("%s: count %d, %v", c.name, n, r.Err())
		}
		if !c.ok && n != 0 {
			t.Errorf("%s: a refused count returned %d", c.name, n)
		}
	}
}

// A failure sticks: nothing after it advances, allocates or replaces it, and
// it is reported in the owning schema's shape with the failing byte's offset.
func TestFailuresStick(t *testing.T) {
	owner := errors.New("owner")
	reject := func(path, expected string) error { return errors.Join(owner, errors.New(path+": "+expected)) }
	r := NewReader([]byte{2, 0, 0}, reject)
	r.Bool()
	if r.Err() == nil || !errors.Is(r.Err(), owner) || !strings.Contains(r.Err().Error(), "payload byte 0: a boolean of exactly 0 or 1") {
		t.Fatalf("failure %v", r.Err())
	}
	first := r.Err()
	r.Abort(errors.New("later"))
	r.FailAt(2, "later")
	if r.U8() != 0 || r.Text(10) != "" || r.Key() != "" || r.Count(10, 1) != 0 || r.Raw(1) != nil || r.U64() != 0 || r.S64() != 0 ||
		r.Amount() != 0 || r.Offset() != 0 || r.Err() != first || r.End() != first {
		t.Fatal("a reader kept reading after a failure, or replaced its first failure")
	}
	r = NewReader([]byte{1, 7}, nil)
	r.U8()
	if err := r.End(); err == nil || !strings.Contains(err.Error(), "logical path payload byte 1, providers searched [netproto v1], expected no bytes after the last field") {
		t.Fatalf("trailing byte: %v", err)
	}
	r = NewReader(nil, nil)
	r.Abort(owner)
	if r.Err() != owner {
		t.Fatal("Abort did not record the caller's error")
	}
	r = NewReader([]byte{7}, nil)
	r.Fail("a different first byte")
	if !strings.Contains(r.Err().Error(), "payload byte 0") {
		t.Fatalf("Fail at the current offset: %v", r.Err())
	}
}

// writeAll writes one value of every primitive from a seed, and readAll reads
// them back in the same order.
func writeAll(w *Writer, seed []byte) {
	at := func(i int) byte {
		if len(seed) == 0 {
			return 0
		}
		return seed[i%len(seed)]
	}
	w.U8(at(0))
	w.Bool(at(1)&1 == 1)
	w.U16(uint16(at(2))<<8 | uint16(at(3)))
	w.U32(uint32(at(4)) << 24)
	w.U64(uint64(at(5)) << 56)
	w.S32(-int32(at(6)) << 20)
	w.S64(-int64(at(7)) << 50)
	w.Text(strings.Repeat("t", int(at(8)%8)))
	w.Key("k" + strings.Repeat("e", int(at(9)%8)))
	w.Amount(uint32(at(10)) << 24)
	var d [DigestBytes]byte
	d[0] = at(11)
	w.Digest(d)
}

func readAll(r *Reader) {
	r.U8()
	r.Bool()
	r.U16()
	r.U32()
	r.U64()
	r.S32()
	r.S64()
	r.Text(8)
	r.Key()
	r.Amount()
	r.Digest()
}

// FuzzReader holds the reader's property over arbitrary input: a sequence of
// primitives either fails or consumes a prefix that writes back to exactly
// the bytes it read, so no input decodes to a value with another encoding.
func FuzzReader(f *testing.F) {
	var w Writer
	writeAll(&w, []byte{1, 1, 0x12, 0x34, 5, 6, 7, 8, 5, 3, 0x3f, 9})
	f.Add(w.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0x80, 0x00})
	f.Fuzz(func(t *testing.T, payload []byte) {
		r := NewReader(payload, nil)
		u8, b, u16, u32, u64 := r.U8(), r.Bool(), r.U16(), r.U32(), r.U64()
		s32, s64, text, key, amount, digest := r.S32(), r.S64(), r.Text(8), r.Key(), r.Amount(), r.Digest()
		if r.Err() != nil {
			return
		}
		var again Writer
		again.U8(u8)
		again.Bool(b)
		again.U16(u16)
		again.U32(u32)
		again.U64(u64)
		again.S32(s32)
		again.S64(s64)
		again.Text(text)
		again.Key(key)
		again.Amount(amount)
		again.Digest(digest)
		if !bytes.Equal(again.Bytes(), payload[:r.Offset()]) {
			t.Fatalf("read %x, writes back %x", payload[:r.Offset()], again.Bytes())
		}
	})
}

// Mutating a valid sequence — truncation at every byte, flips under several
// masks, and bounded random edits — is either refused or reads values that
// write back to exactly the mutated bytes.
func TestReaderMutations(t *testing.T) {
	var w Writer
	writeAll(&w, []byte{1, 1, 0x12, 0x34, 5, 6, 7, 8, 5, 3, 0x3f, 9})
	valid := w.Bytes()
	check := func(p []byte) bool {
		t.Helper()
		r := NewReader(p, nil)
		readAll(r)
		if r.End() != nil {
			return false
		}
		r = NewReader(p, nil)
		u8, b, u16, u32, u64 := r.U8(), r.Bool(), r.U16(), r.U32(), r.U64()
		s32, s64, text, key, amount, digest := r.S32(), r.S64(), r.Text(8), r.Key(), r.Amount(), r.Digest()
		var again Writer
		again.U8(u8)
		again.Bool(b)
		again.U16(u16)
		again.U32(u32)
		again.U64(u64)
		again.S32(s32)
		again.S64(s64)
		again.Text(text)
		again.Key(key)
		again.Amount(amount)
		again.Digest(digest)
		if !bytes.Equal(again.Bytes(), p) {
			t.Fatalf("accepted %x writes back %x", p, again.Bytes())
		}
		return true
	}
	if !check(valid) {
		t.Fatal("the valid sequence was refused")
	}
	for n := 0; n < len(valid); n++ {
		if check(valid[:n]) {
			t.Fatalf("a %d-byte prefix was read whole", n)
		}
	}
	accepted := 0
	for i := range valid {
		for _, mask := range []byte{0x01, 0x02, 0x04, 0x40, 0x80, 0xff} {
			m := append([]byte(nil), valid...)
			m[i] ^= mask
			if check(m) {
				accepted++
			}
		}
	}
	if accepted == 0 {
		t.Fatal("no flip was accepted: the sweep cannot tell a strict reader from a broken one")
	}
	state := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	for i := 0; i < 20000; i++ {
		p := append([]byte(nil), valid...)
		for k := 1 + next(4); k > 0 && len(p) > 0; k-- {
			at := next(len(p))
			switch next(4) {
			case 0:
				p[at] = byte(next(256))
			case 1:
				p = append(p[:at], p[at+1:]...)
			case 2:
				p = append(p[:at], append([]byte{byte(next(256))}, p[at:]...)...)
			case 3:
				p = p[:at]
			}
		}
		check(p)
	}
}

// The protocol constants are contracts (§7.4.1): a change is a new schema
// version, never an edit. Command schema version 2 added payloads and kept
// every version-1 primitive and limit.
func TestProtocolConstants(t *testing.T) {
	if CommandSchemaVersion != 4 {
		t.Fatalf("command schema version %d, want 4", CommandSchemaVersion)
	}
	if MaxCommandBytes != 4194304 || MaxKeyBytes != 255 || DigestBytes != 32 || IDBytes != 16 || AmountBytes != 4 {
		t.Fatal("a version-1 primitive's limit moved")
	}
}
