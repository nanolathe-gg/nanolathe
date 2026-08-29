// Package author writes the small binary fixtures the probe kit ships.
//
// Every layout here follows research/formats byte for byte — tnt.md, gaf.md,
// 3do.md, cob.md and pal.md — and nothing else: the writers emit exactly the
// fields those documents describe, with the "unknown / always zero" words
// written as zero. The package is deliberately standard-library only and is
// used by the `go run`-able generators under probes/<slug>/gen/.
//
// Nothing here is a parser or a simulation rule; it only serialises authored
// data. The probes' data files are committed next to their generators so a
// human can copy them into a retail install without building anything.
package author

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile creates the parent directory and writes data.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Must panics on error; the generators are one-shot tools.
func Must(err error) {
	if err != nil {
		panic(err)
	}
}

// buf is a little-endian byte builder with absolute-offset patching.
type buf struct{ bytes.Buffer }

func (b *buf) u8(v uint8)   { b.WriteByte(v) }
func (b *buf) u16(v uint16) { binary.Write(&b.Buffer, binary.LittleEndian, v) }
func (b *buf) i16(v int16)  { binary.Write(&b.Buffer, binary.LittleEndian, v) }
func (b *buf) u32(v uint32) { binary.Write(&b.Buffer, binary.LittleEndian, v) }
func (b *buf) i32(v int32)  { binary.Write(&b.Buffer, binary.LittleEndian, v) }
func (b *buf) off() uint32  { return uint32(b.Len()) }

// patchU32 overwrites the u32 at absolute offset at.
func (b *buf) patchU32(at uint32, v uint32) {
	binary.LittleEndian.PutUint32(b.Bytes()[at:at+4], v)
}

func (b *buf) cstr(s string) {
	b.WriteString(s)
	b.WriteByte(0)
}

// pad writes zero bytes until the length is a multiple of n.
func (b *buf) pad(n int) {
	for b.Len()%n != 0 {
		b.WriteByte(0)
	}
}

func check(cond bool, format string, args ...any) {
	if !cond {
		panic(fmt.Sprintf(format, args...))
	}
}
