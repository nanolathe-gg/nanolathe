package zrb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/color"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// These small streams are authored here from [fmt zrb]; no retail bytes are
// embedded. Chain-shaped trees make each symbol's branch choices explicit.
type fixtureBits struct {
	data  []byte
	count int
}

func (w *fixtureBits) put(v uint32, n int) {
	for i := 0; i < n; i++ {
		if w.count%8 == 0 {
			w.data = append(w.data, 0)
		}
		w.data[w.count/8] |= byte((v>>i)&1) << uint(w.count%8)
		w.count++
	}
}
func fixtureByteTree(w *fixtureBits, values []byte) {
	w.put(1, 1)
	for i, v := range values {
		if i < len(values)-1 {
			w.put(1, 1)
		}
		w.put(0, 1)
		w.put(uint32(v), 8)
	}
	w.put(0, 1)
}
func fixtureCode(w *fixtureBits, index, count int) {
	for i := 0; i < index; i++ {
		w.put(1, 1)
	}
	if index < count-1 {
		w.put(0, 1)
	}
}
func fixtureWordTree(w *fixtureBits, values []uint16) {
	w.put(1, 1)
	var lo, hi []byte
	for _, v := range values {
		if !slices.Contains(lo, byte(v)) {
			lo = append(lo, byte(v))
		}
		if !slices.Contains(hi, byte(v>>8)) {
			hi = append(hi, byte(v>>8))
		}
	}
	fixtureByteTree(w, lo)
	fixtureByteTree(w, hi)
	for _, v := range []uint32{65533, 65534, 65535} {
		w.put(v, 16)
	}
	for i, v := range values {
		if i < len(values)-1 {
			w.put(1, 1)
		}
		w.put(0, 1)
		fixtureCode(w, slices.Index(lo, byte(v)), len(lo))
		fixtureCode(w, slices.Index(hi, byte(v>>8)), len(hi))
	}
	w.put(0, 1)
}
func fixtureMovie(flags uint32, trees []byte, packets [][]byte, masks []byte, audioFlags uint32) []byte {
	h := make([]byte, 104)
	copy(h, "SMK2")
	binary.LittleEndian.PutUint32(h[4:], 4)
	binary.LittleEndian.PutUint32(h[8:], 4)
	binary.LittleEndian.PutUint32(h[12:], uint32(len(packets))-flags&1)
	binary.LittleEndian.PutUint32(h[16:], uint32(100))
	binary.LittleEndian.PutUint32(h[20:], flags)
	binary.LittleEndian.PutUint32(h[52:], uint32(len(trees)))
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint32(h[56+i*4:], 64)
	}
	binary.LittleEndian.PutUint32(h[72:], audioFlags)
	for _, p := range packets {
		n := (len(p) + 3) &^ 3
		h = binary.LittleEndian.AppendUint32(h, uint32(n)|1)
	}
	h = append(h, masks...)
	h = append(h, trees...)
	for _, p := range packets {
		h = append(h, p...)
		h = append(h, make([]byte, (-len(p))&3)...)
	}
	return h
}
func solidTrees() []byte {
	w := fixtureBits{}
	for _, v := range []uint16{0, 0, 0, 0x0703} {
		fixtureWordTree(&w, []uint16{v})
	}
	return w.data
}

func TestBitOrderAcrossBytes(t *testing.T) {
	b := bitReader{data: []byte{0xa7, 0x51, 0xc2}}
	// First five bits 00111, next six 001101, next seven 1001010.
	for i, want := range []uint32{7, 13, 74} {
		if got := b.read([]int{5, 6, 7}[i]); got != want {
			t.Fatalf("read %d = %d, want %d", i, got, want)
		}
	}
	if b.read(7) != 0 || !errors.Is(b.err, io.ErrUnexpectedEOF) {
		t.Fatal("truncated read did not fail")
	}
}

func TestWordCacheResolvesBeforeShiftAndKeepsRepeatedNewest(t *testing.T) {
	w := fixtureBits{}
	fixtureWordTree(&w, []uint16{0x1234, 0xabcd, 65533, 65534, 65535})
	b := bitReader{data: w.data}
	tree, err := readWordTree(&b)
	if err != nil {
		t.Fatal(err)
	}
	stream := fixtureBits{}
	// Literal A, literal B, newest B again, second A, third A, second B.
	for _, i := range []int{0, 1, 2, 3, 4, 3} {
		fixtureCode(&stream, i, 5)
	}
	b = bitReader{data: stream.data}
	for i, want := range []uint16{0x1234, 0xabcd, 0xabcd, 0x1234, 0x1234, 0xabcd} {
		if got := tree.decode(&b); got != want {
			t.Fatalf("word %d = %04x, want %04x", i, got, want)
		}
	}
	if b.err != nil {
		t.Fatal(b.err)
	}
}

func TestPaletteRGBExpansionAndCopyFromPriorPalette(t *testing.T) {
	var p [256]color.RGBA
	for i := range p {
		p[i] = color.RGBA{byte(i), 0, 0, 255}
	}
	// Replace entry 0, then copy old entries 0..2 onto new entries 1..3.
	err := updatePalette(&p, []byte{63, 16, 32, 0x42, 0, 0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	if p[0] != (color.RGBA{255, 65, 130, 255}) {
		t.Fatalf("literal = %v", p[0])
	}
	for i := 1; i < 4; i++ {
		if p[i].R != byte(i-1) {
			t.Fatalf("overlapping copy entry %d = %v", i, p[i])
		}
	}
}

func TestDPCM16BitCarryAndStereoBaseOrder(t *testing.T) {
	w := fixtureBits{}
	w.put(1, 1)
	w.put(1, 1)
	w.put(1, 1)
	// Left delta +2, right delta -1: one constant tree per delta byte.
	for _, v := range []byte{2, 0, 255, 255} {
		fixtureByteTree(&w, []byte{v})
	}
	w.put(0x80, 8)
	w.put(0, 8) // right starts -32768, high byte first
	w.put(0, 8)
	w.put(255, 8) // left starts 255
	data := binary.LittleEndian.AppendUint32(nil, 12)
	data = append(data, w.data...)
	pcm, err := decodeAudio(data, 0xf0005622, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{255, -32768, 257, 32767, 259, 32766}
	for i, v := range want {
		if got := int16(binary.LittleEndian.Uint16(pcm[i*2:])); got != v {
			t.Fatalf("sample %d = %d, want %d", i, got, v)
		}
	}
}

func TestDPCM8BitWrapAndUnsignedNormalization(t *testing.T) {
	w := fixtureBits{}
	w.put(1, 1)
	w.put(0, 1)
	w.put(0, 1)
	fixtureByteTree(&w, []byte{2})
	w.put(255, 8)
	data := binary.LittleEndian.AppendUint32(nil, 3)
	data = append(data, w.data...)
	pcm, err := decodeAudio(data, 0xc0005622, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int16{32512, -32512, -32000} {
		if got := int16(binary.LittleEndian.Uint16(pcm[i*2:])); got != want {
			t.Fatalf("sample %d = %d, want %d", i, got, want)
		}
	}
}

func TestPlayOnceAndIndependentAudioPass(t *testing.T) {
	audio := []byte{8, 0, 0, 0, 0, 128, 255, 64} // raw unsigned-eight-bit stereo
	movie := fixtureMovie(3, solidTrees(), [][]byte{audio, {0, 0, 0, 0}}, []byte{2, 0}, 0x50005622)
	d, err := New(movie)
	if err != nil {
		t.Fatal(err)
	}
	if d.FrameCount != 1 || d.DisplayHeight != 8 || !d.Interlaced {
		t.Fatalf("ring/scaling metadata = %+v", d)
	}
	all, err := d.DecodeAudio(0)
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	if f.Index != 0 || !bytes.Equal(all, f.Audio[0]) {
		t.Fatal("audio-only pass advanced or changed video state")
	}
	if !bytes.Equal(f.Pixels, bytes.Repeat([]byte{7}, 16)) {
		t.Fatalf("solid block = %v", f.Pixels)
	}
	if _, err = d.Next(); err != io.EOF {
		t.Fatalf("ring packet played: %v", err)
	}
}

func TestVideoPairOrderMapBitsAndFrameCacheReset(t *testing.T) {
	// One map word sets just the first and last pixel. Full pairs alternate
	// to distinguish right-pair-first from left-pair-first.
	w := fixtureBits{}
	fixtureWordTree(&w, []uint16{0x8001})
	fixtureWordTree(&w, []uint16{0x0904})
	fixtureWordTree(&w, []uint16{0x0201, 0x0403})
	fixtureWordTree(&w, []uint16{0, 1, 2, 65533})
	mono := fixtureBits{}
	fixtureCode(&mono, 0, 4)
	full := fixtureBits{}
	fixtureCode(&full, 1, 4)
	for row := 0; row < 4; row++ {
		fixtureCode(&full, 0, 2)
		fixtureCode(&full, 1, 2)
	}
	skip := fixtureBits{}
	fixtureCode(&skip, 2, 4)
	reset := fixtureBits{}
	fixtureCode(&reset, 3, 4) // newest cache resets to type zero
	d, err := New(fixtureMovie(0, w.data, [][]byte{mono.data, full.data, skip.data, reset.data}, []byte{0, 0, 0, 0}, 0))
	if err != nil {
		t.Fatal(err)
	}
	for frame := 0; frame < 4; frame++ {
		f, err := d.Next()
		if err != nil {
			t.Fatal(err)
		}
		if frame == 1 || frame == 2 {
			for row := 0; row < 4; row++ {
				if !bytes.Equal(f.Pixels[row*4:row*4+4], []byte{3, 4, 1, 2}) {
					t.Fatalf("frame %d row %d = %v", frame, row, f.Pixels)
				}
			}
		} else {
			for i, got := range f.Pixels {
				want := byte(4)
				if i == 0 || i == 15 {
					want = 9
				}
				if got != want {
					t.Fatalf("frame %d pixel %d = %d, want %d", frame, i, got, want)
				}
			}
		}
	}
}

func TestSignedFrameInterval(t *testing.T) {
	for _, tc := range []struct {
		raw  int32
		want time.Duration
	}{{-3333, 33330 * time.Microsecond}, {25, 25 * time.Millisecond}} {
		movie := fixtureMovie(0, solidTrees(), [][]byte{{0, 0, 0, 0}}, []byte{0}, 0)
		binary.LittleEndian.PutUint32(movie[16:], uint32(tc.raw))
		d, err := New(movie)
		if err != nil {
			t.Fatal(err)
		}
		if d.FrameDuration != tc.want {
			t.Fatalf("interval %d = %s, want %s", tc.raw, d.FrameDuration, tc.want)
		}
	}
}

func TestTruncationAndUnsupportedCodecs(t *testing.T) {
	movie := fixtureMovie(0, solidTrees(), [][]byte{{0, 0, 0, 0}}, []byte{0}, 0)
	for end := 0; end < len(movie); end++ {
		if _, err := New(movie[:end]); err == nil {
			t.Fatalf("accepted prefix length %d", end)
		}
	}
	bad := bytes.Clone(movie)
	binary.LittleEndian.PutUint32(bad[16:], 0)
	if _, err := New(bad); err == nil {
		t.Fatal("accepted zero frame interval")
	}
	bad = bytes.Clone(movie)
	copy(bad, "SMK4")
	if _, err := New(bad); err == nil {
		t.Fatal("accepted SMK4")
	}
	bad = bytes.Clone(movie)
	binary.LittleEndian.PutUint32(bad[72:], 0xc4005622)
	if _, err := New(bad); err == nil {
		t.Fatal("accepted perceptual audio codec")
	}
}

func FuzzDecoder(f *testing.F) {
	f.Add(fixtureMovie(0, solidTrees(), [][]byte{{0, 0, 0, 0}}, []byte{0}, 0))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := New(b)
		if err != nil {
			return
		}
		for i := 0; i < min(d.FrameCount, 4); i++ {
			if _, err := d.Next(); err != nil {
				return
			}
		}
	})
}

func TestDurationOverflowRejected(t *testing.T) {
	movie := fixtureMovie(0, solidTrees(), make([][]byte, 5000), make([]byte, 5000), 0)
	binary.LittleEndian.PutUint32(movie[16:], 1<<31-1)
	if _, err := New(movie); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("duration overflow result = %v", err)
	}
}
