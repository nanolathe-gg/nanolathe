package audiobackend

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeMP3InvalidInput(t *testing.T) {
	for _, encoded := range [][]byte{nil, []byte("invalid music")} {
		if reader, err := decodeMP3(bytes.NewReader(encoded), 44100); err == nil {
			reader.Close()
			t.Fatal("invalid MP3 accepted")
		}
	}
	if _, err := decodeMP3(bytes.NewReader(nil), 0); err == nil {
		t.Fatal("invalid output rate accepted")
	}
}

// The installed soundtrack is optional and never copied into test fixtures.
// This locks the host conversion contract, not a retail codec implementation.
func TestDecodeInstalledMP3(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := filepath.Glob(filepath.Join(home, "TotalAnnihilation", "music", "*.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) == 0 {
		t.Skip("installed MP3 soundtrack absent")
	}
	var lengths [2]int64
	for i, rate := range []int{22050, 44100} {
		file, err := os.Open(tracks[0])
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		reader, err := decodeMP3(file, rate)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		// Deliberately unaligned reads exercise the frame-to-byte adapter.
		var prefix [44100*8 + 3]byte
		n, err := io.ReadFull(reader, prefix[:])
		if err != nil {
			t.Fatal(err)
		}
		nonzero := false
		for j := 0; j+4 <= n; j += 4 {
			value := math.Float32frombits(binary.LittleEndian.Uint32(prefix[j:]))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				t.Fatal("nonfinite PCM")
			}
			nonzero = nonzero || value != 0
		}
		if !nonzero {
			t.Fatal("decoded soundtrack is silent")
		}
		tail, err := io.Copy(io.Discard, reader)
		if err != nil {
			t.Fatal(err)
		}
		lengths[i] = int64(n) + tail
		if lengths[i]%8 != 0 {
			t.Fatal("partial stereo frame")
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal("repeated close:", err)
		}
		if _, err := reader.Read(prefix[:]); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("read after close: %v", err)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			t.Fatal("decoder closed caller's source:", err)
		}
	}
	// Conversion preserves duration; a rounding difference of one frame is
	// allowed when the source duration is not integral at the lower rate.
	if delta := lengths[1] - 2*lengths[0]; delta < -8 || delta > 8 {
		t.Fatalf("output rate did not preserve duration: byte lengths %v", lengths)
	}
}
