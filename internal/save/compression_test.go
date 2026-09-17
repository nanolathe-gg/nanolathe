package save

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestDecodeSQSHLiterals(t *testing.T) {
	plain := []byte("sqsh literals")
	payload := make([]byte, 2+len(plain)+3)
	payload[0] = 0 // first eight literal items
	copy(payload[1:9], plain[:8])
	payload[9] = 0x20 // five literals, then a match terminator
	copy(payload[10:], plain[8:])
	// zero-position match terminator and the normal trailing pad byte
	term := len(plain) + 2
	payload[term] = 0
	payload[term+1] = 0
	payload[term+2] = 0
	chunk := make([]byte, 19+len(payload))
	copy(chunk, []byte("SQSH"))
	chunk[4], chunk[5], chunk[6] = 2, 1, 0
	binary.LittleEndian.PutUint32(chunk[7:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(chunk[11:], uint32(len(plain)))
	binary.LittleEndian.PutUint32(chunk[15:], sumBytes(payload))
	copy(chunk[19:], payload)
	got, err := decodeSQSH(chunk)
	if err != nil {
		t.Fatalf("decodeSQSH: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("decoded %q, want %q", got, plain)
	}
}

func TestDecodeSQSHRejectsBoundsAndChecksum(t *testing.T) {
	chunk := make([]byte, 19)
	copy(chunk, []byte("SQSH"))
	chunk[5] = 1
	binary.LittleEndian.PutUint32(chunk[7:], 1)
	if _, err := decodeSQSH(chunk); err == nil {
		t.Fatal("accepted payload beyond framing")
	}
	chunk = append(chunk, 0)
	binary.LittleEndian.PutUint32(chunk[11:], 1)
	if _, err := decodeSQSH(chunk); err == nil {
		t.Fatal("accepted checksum mismatch")
	}
}

// A save is an untrusted file. A chunk header that claims an output no
// payload of its size could ever encode must be rejected from the header,
// before the decoder reserves or reads anything [fmt hpi].
func TestDecodeSQSHRejectsOversizedDeclaredOutput(t *testing.T) {
	newChunk := func(method byte, payload []byte, declared uint32) []byte {
		chunk := make([]byte, 19+len(payload))
		copy(chunk, []byte("SQSH"))
		chunk[4], chunk[5], chunk[6] = 2, method, 0
		binary.LittleEndian.PutUint32(chunk[7:], uint32(len(payload)))
		binary.LittleEndian.PutUint32(chunk[11:], declared)
		binary.LittleEndian.PutUint32(chunk[15:], sumBytes(payload))
		copy(chunk[19:], payload)
		return chunk
	}
	// A truncated LZ77 body claiming the full uint32 range: the old reader
	// reserved four gibibytes here before the truncation was ever noticed.
	for _, method := range []byte{1, 2} {
		chunk := newChunk(method, []byte{0x00, 0x41, 0x00, 0x00}, 0xFFFFFFFF)
		out, err := decodeSQSH(chunk)
		if err == nil {
			t.Fatalf("method %d: accepted a 4 GiB claim from a 4-byte payload (%d bytes out)", method, len(out))
		}
		if !errors.Is(err, ErrFormat) {
			t.Fatalf("method %d: err = %v, want ErrFormat", method, err)
		}
	}
	// The ceilings are properties of the encodings, so a claim at the limit is
	// still admitted to the decoder and fails only on the real content.
	if maxSQSHLZOutput(4) < 136 || maxDeflateOutput(4) < 4*1032 {
		t.Fatalf("ceiling below one group: lz %d deflate %d", maxSQSHLZOutput(4), maxDeflateOutput(4))
	}
	chunk := newChunk(1, []byte{0x00, 0x41, 0x00, 0x00}, uint32(maxSQSHLZOutput(4)))
	if _, err := decodeSQSH(chunk); err == nil || strings.Contains(err.Error(), "exceeds what") {
		t.Fatalf("a claim at the ceiling should reach the decoder, got %v", err)
	}
}

// A zlib member that expands past its declared size stops at the declared
// length plus one byte instead of being read to exhaustion.
func TestDecodeSQSHZlibStopsAtDeclaredSize(t *testing.T) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(make([]byte, 1<<20)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	payload := buf.Bytes()
	chunk := make([]byte, 19+len(payload))
	copy(chunk, []byte("SQSH"))
	chunk[4], chunk[5], chunk[6] = 2, 2, 0
	binary.LittleEndian.PutUint32(chunk[7:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(chunk[11:], 16) // claims far less than it holds
	binary.LittleEndian.PutUint32(chunk[15:], sumBytes(payload))
	copy(chunk[19:], payload)
	if _, err := decodeSQSH(chunk); !errors.Is(err, ErrFormat) {
		t.Fatalf("err = %v, want ErrFormat", err)
	}
}
