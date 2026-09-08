package audiobackend

import (
	"bytes"
	"io"
	"testing"
)

func TestPanReaderLoopRepeatsCanonicalPCMWithoutEOF(t *testing.T) {
	data := stereoFrames(1, 0.5)
	r := newPanReader(data, 0)
	r.SetLoop(true)
	got := make([]byte, len(data)*3)
	if _, err := io.ReadFull(r, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append(append(append([]byte{}, data...), data...), data...)) {
		t.Fatal("looping reader did not repeat canonical PCM")
	}
	r.SetLoop(false)
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("one-shot reader EOF = %v, want EOF", err)
	}
}
