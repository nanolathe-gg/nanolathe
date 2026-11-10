// Package netproto is the multiplayer protocol's leaf: the version-1 wire
// primitives every Nanolathe payload is written in
// (docs/DESIGN_MULTIPLAYER.md §7.4.1, §8.6), a bounded reader that refuses
// malformed input before it allocates from it, and the identity a seat
// reports before it may ready (§8.2). It imports only the standard library,
// so the relay and replay packages of §14 can carry payloads and identities
// without reaching the session, the content or any simulation package. These
// are Nanolathe protocol contracts, not retail findings.
//
// Each payload's schema belongs to the package that owns its type — the
// command and configuration codecs to internal/session, the build manifest
// to internal/version — and each writes its fields in these primitives.
// Envelopes, live relay messages and transport are later milestones' (§12,
// §16 M6/M7).
package netproto

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

// CommandSchemaVersion is the command schema version this build speaks. It is
// negotiated outside every payload (§7.4.1): no payload carries it, and a
// seat reports it as its Identity's Protocol. Version 2 adds the sharing and
// alliance payloads; every version-1 payload keeps its bytes and meaning
// under it. Version 3 adds temporary economy activity controls. Version 4
// adds the single-player replay record of the `+ShootAll` toggle. All
// versions are written in this package's version-1 primitives.
const CommandSchemaVersion uint16 = 4

// Protocol limits of the version-1 primitives (§7.4.1), unchanged by command
// schema version 2. They are Nanolathe protocol limits, not retail constants.
const (
	// MaxCommandBytes is the largest command payload, checked before parsing.
	MaxCommandBytes = 4 << 20
	// MaxKeyBytes is the `key` primitive's byte ceiling.
	MaxKeyBytes = 255
	// DigestBytes and IDBytes are the fixed widths of `digest` and `id`.
	DigestBytes = 32
	IDBytes     = 16
	// AmountBytes is the width of `amount`, one IEEE binary32.
	AmountBytes = 4
)

// negativeZeroAmount is the binary32 encoding of negative zero, the one
// finite amount whose canonical spelling is another bit pattern.
const negativeZeroAmount uint32 = 0x80000000

// CanonicalAmount is the online spelling of an `amount`'s bits: negative zero
// becomes positive zero and every other pattern is kept (§7.4.1). The
// single-player replay context keeps all 32 bits instead.
func CanonicalAmount(bits uint32) uint32 {
	if bits == negativeZeroAmount {
		return 0
	}
	return bits
}

// OnlineAmount reports whether an `amount`'s bits are in the online domain:
// finite and canonical, so negative zero is refused (§7.4.1). It reads the
// encoding alone; no floating arithmetic is involved.
func OnlineAmount(bits uint32) bool {
	return bits&0x7f800000 != 0x7f800000 && bits != negativeZeroAmount
}

// UvarintLen is the length of v's shortest unsigned varint.
func UvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// Writer appends version-1 primitives. It writes the values its caller has
// already validated against the owning schema; it bounds nothing itself. The
// zero value is an empty writer.
type Writer struct{ b []byte }

// Bytes returns the encoding written so far. The slice aliases the writer's
// storage.
func (w *Writer) Bytes() []byte { return w.b }

// Len is the number of bytes written so far.
func (w *Writer) Len() int { return len(w.b) }

// U8 writes one byte.
func (w *Writer) U8(v uint8) { w.b = append(w.b, v) }

// Bool writes a boolean as exactly 0 or 1.
func (w *Writer) Bool(v bool) {
	if v {
		w.b = append(w.b, 1)
	} else {
		w.b = append(w.b, 0)
	}
}

// U16, U32 and U64 write the shortest unsigned base-128 varint.
func (w *Writer) U16(v uint16) { w.uvarint(uint64(v)) }
func (w *Writer) U32(v uint32) { w.uvarint(uint64(v)) }
func (w *Writer) U64(v uint64) { w.uvarint(v) }

func (w *Writer) uvarint(v uint64) {
	for v >= 0x80 {
		w.b = append(w.b, byte(v)|0x80)
		v >>= 7
	}
	w.b = append(w.b, byte(v))
}

// S32 and S64 write the zigzag form of the named signed width, then its
// shortest varint. Neither narrows through Go int.
func (w *Writer) S32(v int32) { w.uvarint(uint64(uint32(v<<1) ^ uint32(v>>31))) }
func (w *Writer) S64(v int64) { w.uvarint(uint64(v<<1) ^ uint64(v>>63)) }

// Text writes `text(N)`: a u32 byte length and the bytes.
func (w *Writer) Text(s string) {
	w.uvarint(uint64(len(s)))
	w.b = append(w.b, s...)
}

// Key writes `key`: a u16 byte length and the bytes. Its byte layout is
// Text's; the two differ in the domain a reader accepts.
func (w *Writer) Key(s string) {
	w.uvarint(uint64(len(s)))
	w.b = append(w.b, s...)
}

// Digest writes a `digest`: exactly 32 raw bytes.
func (w *Writer) Digest(d [DigestBytes]byte) { w.b = append(w.b, d[:]...) }

// ID writes an `id`: exactly 16 raw bytes.
func (w *Writer) ID(id [IDBytes]byte) { w.b = append(w.b, id[:]...) }

// Amount writes an `amount`: the four little-endian bytes of the binary32
// bits. The caller chooses the bits — CanonicalAmount online, all 32 bits in
// single-player replay.
func (w *Writer) Amount(bits uint32) { w.b = binary.LittleEndian.AppendUint32(w.b, bits) }

// Raw writes bytes with no framing; the schema frames them.
func (w *Writer) Raw(b []byte) { w.b = append(w.b, b...) }

// RejectFunc builds the owning schema's diagnostic for a refusal at path,
// so each codec keeps its own error shape.
type RejectFunc func(path, expected string) error

// Reader reads version-1 primitives from one payload. The first failure
// sticks: every later read returns the zero value and advances nothing, so a
// decode reports exactly one error and builds nothing from a malformed tail.
// Every length and count is checked against its bound and against the bytes
// that remain before anything is taken or allocated from it.
type Reader struct {
	b      []byte
	off    int
	err    error
	reject RejectFunc
}

// NewReader reads payload, reporting refusals through reject. A nil reject
// uses this package's own diagnostic shape.
func NewReader(payload []byte, reject RejectFunc) *Reader {
	if reject == nil {
		reject = wireError
	}
	return &Reader{b: payload, reject: reject}
}

func wireError(path, expected string) error {
	return fmt.Errorf("nanolathe: wire payload rejected: logical path %s, providers searched [netproto v1], expected %s", path, expected)
}

// Err is the first failure, or nil.
func (r *Reader) Err() error { return r.err }

// Offset is the next byte to read.
func (r *Reader) Offset() int { return r.off }

// Fail records a refusal at the current offset, unless one is recorded.
func (r *Reader) Fail(expected string) { r.FailAt(r.off, expected) }

// FailAt records a refusal at a given byte, for a field the caller has read
// and then found outside its schema's domain.
func (r *Reader) FailAt(offset int, expected string) {
	if r.err == nil {
		r.err = r.reject(fmt.Sprintf("payload byte %d", offset), expected)
	}
}

// Abort records an error the caller built, unless one is recorded.
func (r *Reader) Abort(err error) {
	if r.err == nil {
		r.err = err
	}
}

// End refuses any byte after the last field and returns the first failure.
func (r *Reader) End() error {
	if r.err == nil && r.off != len(r.b) {
		r.Fail("no bytes after the last field")
	}
	return r.err
}

// U8 reads one byte.
func (r *Reader) U8() uint8 {
	if r.err != nil {
		return 0
	}
	if r.off >= len(r.b) {
		r.Fail("another byte")
		return 0
	}
	v := r.b[r.off]
	r.off++
	return v
}

// Bool reads a boolean of exactly 0 or 1.
func (r *Reader) Bool() bool {
	v := r.U8()
	if v > 1 {
		r.off--
		r.Fail("a boolean of exactly 0 or 1")
		return false
	}
	return v == 1
}

// U16, U32 and U64 read the shortest varint of a value within the width.
func (r *Reader) U16() uint16 { return uint16(r.uvarint(16)) }
func (r *Reader) U32() uint32 { return uint32(r.uvarint(32)) }
func (r *Reader) U64() uint64 { return r.uvarint(64) }

// S32 and S64 read the zigzag form of the named signed width.
func (r *Reader) S32() int32 {
	u := uint32(r.uvarint(32))
	return int32(u>>1) ^ -int32(u&1)
}

func (r *Reader) S64() int64 {
	u := r.uvarint(64)
	return int64(u>>1) ^ -int64(u&1)
}

// uvarint reads the shortest varint of a value that fits bits, refusing an
// overlong or overflowing encoding with the offset of its first byte.
func (r *Reader) uvarint(bits uint) uint64 {
	if r.err != nil {
		return 0
	}
	start := r.off
	var v uint64
	for i := 0; ; i++ {
		if r.off >= len(r.b) {
			r.Fail("a complete varint")
			return 0
		}
		c := r.b[r.off]
		r.off++
		if i == 9 && c > 1 {
			r.off = start
			r.Fail("a varint within 64 bits")
			return 0
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			if i > 0 && c == 0 {
				r.off = start
				r.Fail("the shortest varint")
				return 0
			}
			if bits < 64 && v>>bits != 0 {
				r.off = start
				r.Fail(fmt.Sprintf("a value within %d bits", bits))
				return 0
			}
			return v
		}
	}
}

// Raw takes n bytes after checking that they are present. The slice aliases
// the payload.
func (r *Reader) Raw(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.b)-r.off {
		r.Fail(fmt.Sprintf("%d more bytes", n))
		return nil
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out
}

// Text reads `text(max)`: a u32 byte length of at most max, then that many
// bytes of valid UTF-8 without NUL.
func (r *Reader) Text(max int) string {
	n := r.uvarint(32)
	if r.err != nil {
		return ""
	}
	if n > uint64(max) {
		r.Fail(fmt.Sprintf("a text of at most %d bytes", max))
		return ""
	}
	start := r.off
	raw := r.Raw(int(n))
	if r.err == nil && (!utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0) {
		r.FailAt(start, "valid UTF-8 without NUL")
		return ""
	}
	return string(raw)
}

// Key reads `key`: a u16 byte length of 1..255, then that many bytes with
// no NUL. Bytes above ASCII are kept literally; a key need not be UTF-8. The
// owning schema checks that the bytes are the canonical content key.
func (r *Reader) Key() string { return r.key(1) }

// OptionalKey reads an explicitly optional key, which also admits length 0.
func (r *Reader) OptionalKey() string { return r.key(0) }

func (r *Reader) key(min uint64) string {
	n := r.uvarint(16)
	if r.err != nil {
		return ""
	}
	if n < min || n > MaxKeyBytes {
		r.Fail(fmt.Sprintf("a key of %d..%d bytes", min, MaxKeyBytes))
		return ""
	}
	start := r.off
	raw := r.Raw(int(n))
	if r.err == nil && bytes.IndexByte(raw, 0) >= 0 {
		r.FailAt(start, "a key without NUL")
		return ""
	}
	return string(raw)
}

// Digest reads a `digest`: exactly 32 bytes.
func (r *Reader) Digest() (d [DigestBytes]byte) {
	copy(d[:], r.Raw(DigestBytes))
	return d
}

// ID reads an `id`: exactly 16 bytes.
func (r *Reader) ID() (id [IDBytes]byte) {
	copy(id[:], r.Raw(IDBytes))
	return id
}

// Amount reads an `amount`'s four little-endian bytes as binary32 bits. The
// owning schema applies its context's domain (OnlineAmount).
func (r *Reader) Amount() uint32 {
	raw := r.Raw(AmountBytes)
	if raw == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(raw)
}

// Count reads a collection's u32 count and checks it against max and against
// the bytes that remain at minRecord bytes a record, before the caller
// allocates anything from it.
func (r *Reader) Count(max, minRecord int) int { return r.count(32, max, minRecord) }

// Count16 is Count for a collection whose count is a u16.
func (r *Reader) Count16(max, minRecord int) int { return r.count(16, max, minRecord) }

func (r *Reader) count(bits uint, max, minRecord int) int {
	n := r.uvarint(bits)
	if r.err != nil {
		return 0
	}
	if n > uint64(max) {
		r.Fail(fmt.Sprintf("a count of at most %d", max))
		return 0
	}
	if n*uint64(minRecord) > uint64(len(r.b)-r.off) {
		r.Fail(fmt.Sprintf("%d records of at least %d bytes", n, minRecord))
		return 0
	}
	return int(n)
}
