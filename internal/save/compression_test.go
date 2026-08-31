package save

import (
	"encoding/binary"
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
