package audiobackend

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

func stereoFrames(values ...float32) []byte {
	data := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return data
}

func TestPanReaderSeekRestoresTransformedStream(t *testing.T) {
	data := stereoFrames(1, 0.5, 0.25, -0.5)
	r := newPanReader(data, 0.5)
	want, err := io.ReadAll(newPanReader(data, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 11)
	if _, err := io.ReadFull(r, first); err != nil {
		t.Fatal(err)
	}
	if position, err := r.Seek(0, io.SeekCurrent); err != nil || position != int64(len(first)) {
		t.Fatalf("partial current position = %d, %v", position, err)
	}
	if position, err := r.Seek(-3, io.SeekCurrent); err != nil || position != 8 {
		t.Fatalf("partial current rewind = %d, %v", position, err)
	}
	current, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(current, want[8:]) {
		t.Fatal("current-relative seek did not preserve transformed-frame bytes")
	}
	if position, err := r.Seek(0, io.SeekStart); err != nil || position != 0 {
		t.Fatalf("rewind = %d, %v", position, err)
	}
	whole, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, whole[:len(first)]) {
		t.Fatal("seek to start did not restore the transformed byte stream")
	}
	if position, err := r.Seek(3, io.SeekStart); err != nil || position != 3 {
		t.Fatalf("unaligned seek = %d, %v", position, err)
	}
	part, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(part, whole[3:]) {
		t.Fatal("unaligned seek did not preserve transformed-frame bytes")
	}
	if position, err := r.Seek(-8, io.SeekEnd); err != nil || position != int64(len(whole)-8) {
		t.Fatalf("end-relative seek = %d, %v", position, err)
	}
	last, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(last, whole[len(whole)-8:]) {
		t.Fatal("end-relative seek did not preserve transformed-frame bytes")
	}
}

func TestPanReaderSetPanAppliesAfterRewind(t *testing.T) {
	r := newPanReader(stereoFrames(1, 1), 0)
	before := make([]byte, 8)
	if _, err := io.ReadFull(r, before); err != nil {
		t.Fatal(err)
	}
	r.SetPan(1)
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	after := make([]byte, 8)
	if _, err := io.ReadFull(r, after); err != nil {
		t.Fatal(err)
	}
	left := math.Float32frombits(binary.LittleEndian.Uint32(after))
	right := math.Float32frombits(binary.LittleEndian.Uint32(after[4:]))
	if left != 0 || right != 1 {
		t.Fatalf("pan after rewind = (%g, %g), want (0, 1)", left, right)
	}
}
