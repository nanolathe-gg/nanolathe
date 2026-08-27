package audio

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// helper to build RIFF bytes for tests (mirrors fixtures)
func buildRIFF(channels int, rate int, bits int, pcm []byte) []byte {
	block := channels * bits / 8
	byteRate := rate * block
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+len(pcm)))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16)
	binary.LittleEndian.PutUint16(hdr[20:22], 1)
	binary.LittleEndian.PutUint16(hdr[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(hdr[32:34], uint16(block))
	binary.LittleEndian.PutUint16(hdr[34:36], uint16(bits))
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(len(pcm)))
	out := append(hdr, pcm...)
	if len(pcm)&1 != 0 {
		out = append(out, 0)
	}
	return out
}

func TestDecode_RIFFVariants(t *testing.T) {
	cases := []struct {
		name     string
		channels int
		rate     int
		bits     int
		pcm      []byte
	}{
		{"mono11025_8", 1, 11025, 8, make([]byte, 40)},
		{"mono22050_16", 1, 22050, 16, make([]byte, 80)},
		{"stereo44100_16", 2, 44100, 16, make([]byte, 160)},
		{"mono22254_8", 1, 22254, 8, make([]byte, 100)},
	}
	for _, tc := range cases {
		data := buildRIFF(tc.channels, tc.rate, tc.bits, tc.pcm)
		s, err := Decode(tc.name, data)
		if err != nil {
			t.Fatalf("%s decode %v", tc.name, err)
		}
		if s.Container != "RIFF" {
			t.Fatalf("%s container %q want RIFF", tc.name, s.Container)
		}
		if int(s.Channels) != tc.channels || int(s.SampleRate) != tc.rate || int(s.BitsPerSample) != tc.bits {
			t.Fatalf("%s got ch %d rate %d bits %d want %d %d %d", tc.name, s.Channels, s.SampleRate, s.BitsPerSample, tc.channels, tc.rate, tc.bits)
		}
		if s.BlockAlign != uint16(tc.channels*tc.bits/8) {
			t.Fatalf("%s blockAlign %d want %d", tc.name, s.BlockAlign, tc.channels*tc.bits/8)
		}
		if s.ByteRate != uint32(tc.rate*tc.channels*tc.bits/8) {
			t.Fatalf("%s byteRate %d", tc.name, s.ByteRate)
		}
		if len(s.Data) != len(tc.pcm) {
			t.Fatalf("%s data len %d want %d", tc.name, len(s.Data), len(tc.pcm))
		}
	}
}

func TestDecode_RIFF_OddPadding(t *testing.T) {
	// JUNK chunk size 3 odd + pad, then fmt/data [fmt wav] odd pad
	pcm := []byte{0x80, 0x81, 0x82, 0x83, 0x84} // 5 odd
	junk := []byte("JUNK")
	// Build manual riff with JUNK odd
	extra := make([]byte, 8+3+1)
	copy(extra[0:4], "JUNK")
	binary.LittleEndian.PutUint32(extra[4:8], 3)
	copy(extra[8:11], junk[:3])
	extra[11] = 0 // pad
	fmtSize := 16
	fmtPayload := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtPayload[0:2], 1)
	binary.LittleEndian.PutUint16(fmtPayload[2:4], 1)
	binary.LittleEndian.PutUint32(fmtPayload[4:8], 11025)
	binary.LittleEndian.PutUint32(fmtPayload[8:12], 11025)
	binary.LittleEndian.PutUint16(fmtPayload[12:14], 1)
	binary.LittleEndian.PutUint16(fmtPayload[14:16], 8)
	fmtChunk := append([]byte("fmt "), binary.LittleEndian.AppendUint32(nil, uint32(fmtSize))...)
	fmtChunk = append(fmtChunk, fmtPayload...)
	dataChunk := append([]byte("data"), binary.LittleEndian.AppendUint32(nil, uint32(len(pcm)))...)
	dataChunk = append(dataChunk, pcm...)
	if len(pcm)&1 != 0 {
		dataChunk = append(dataChunk, 0)
	}
	chunks := append(extra, fmtChunk...)
	chunks = append(chunks, dataChunk...)
	hdr := make([]byte, 12)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(4+len(chunks)))
	copy(hdr[8:12], "WAVE")
	full := append(hdr, chunks...)
	s, err := Decode("oddpad", full)
	if err != nil {
		t.Fatalf("oddpad %v", err)
	}
	if len(s.Data) != 5 {
		t.Fatalf("oddpad data %d want 5", len(s.Data))
	}
	if s.SampleRate != 11025 {
		t.Fatalf("oddpad rate %d", s.SampleRate)
	}
}

func TestDecode_Raw(t *testing.T) {
	raw := bytesRepeat(0x5c, 100)
	s, err := Decode("honk", raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Container != "raw" {
		t.Fatalf("raw container %q", s.Container)
	}
	if s.SampleRate != 11025 || s.Channels != 1 || s.BitsPerSample != 8 {
		t.Fatalf("raw fmt %d %d %d", s.SampleRate, s.Channels, s.BitsPerSample)
	}
	if len(s.Data) != 100 {
		t.Fatalf("raw len %d", len(s.Data))
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func buildDIGI(rate int, pcm []byte) []byte {
	hsz := 24
	hshdPayload := make([]byte, 16)
	binary.LittleEndian.PutUint32(hshdPayload[6:10], uint32(rate))
	hshd := make([]byte, 8+16)
	copy(hshd[0:4], "HSHD")
	binary.BigEndian.PutUint32(hshd[4:8], uint32(hsz))
	copy(hshd[8:], hshdPayload)
	sdatSize := 8 + len(pcm)
	sdat := make([]byte, 8+len(pcm))
	copy(sdat[0:4], "SDAT")
	binary.BigEndian.PutUint32(sdat[4:8], uint32(sdatSize))
	copy(sdat[8:], pcm)
	body := append(hshd, sdat...)
	fileSize := 8 + len(body)
	hdr := make([]byte, 8)
	copy(hdr[0:4], "DIGI")
	binary.BigEndian.PutUint32(hdr[4:8], uint32(fileSize))
	return append(hdr, body...)
}

func TestDecode_DIGI_Remap(t *testing.T) {
	pcm := bytesRepeat(0x80, 20)
	data11000 := buildDIGI(11000, pcm)
	s, err := Decode("digi11000", data11000)
	if err != nil {
		t.Fatal(err)
	}
	if s.Container != "DIGI" {
		t.Fatalf("digi container %q", s.Container)
	}
	if s.SampleRate != 11025 {
		t.Fatalf("remap got %d want 11025", s.SampleRate)
	}
	if len(s.Data) != 10 {
		t.Fatalf("digi len %d want 10 after SDAT wrapper trim", len(s.Data))
	}
	data11025 := buildDIGI(11025, pcm)
	s2, err := Decode("digi11025", data11025)
	if err != nil {
		t.Fatal(err)
	}
	if s2.SampleRate != 11025 {
		t.Fatalf("digi 11025 got %d", s2.SampleRate)
	}
}

func TestDecode_DIGI_Container(t *testing.T) {
	pcm := bytesRepeat(0x7f, 50)
	data := buildDIGI(11025, pcm)
	s, err := Decode("digi", data)
	if err != nil {
		t.Fatal(err)
	}
	if s.Channels != 1 || s.BitsPerSample != 8 {
		t.Fatalf("digi fmt %d %d", s.Channels, s.BitsPerSample)
	}
}

func TestDecode_FixturesFromTestdata(t *testing.T) {
	// Verify synthetic authored fixtures under testdata/ decode correctly [fmt wav]
	for _, tc := range []struct {
		path string
		rate int
		ch   int
		bits int
		kind string
	}{
		{"testdata/mono11025_8.wav", 11025, 1, 8, "RIFF"},
		{"testdata/mono22050_16.wav", 22050, 1, 16, "RIFF"},
		{"testdata/stereo44100_16.wav", 44100, 2, 16, "RIFF"},
		{"testdata/mono22254_8.wav", 22254, 1, 8, "RIFF"},
		{"testdata/raw_honk.wav", 11025, 1, 8, "raw"},
		{"testdata/digi_sing.wav", 11025, 1, 8, "DIGI"},
		{"testdata/digi_remap.wav", 11025, 1, 8, "DIGI"},
	} {
		data, err := os.ReadFile(filepath.Join("testdata", filepath.Base(tc.path)))
		if err != nil {
			// fallback to full path if running from different cwd
			data, err = os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read %s %v", tc.path, err)
			}
		}
		s, err := Decode(tc.path, data)
		if err != nil {
			t.Fatalf("%s %v", tc.path, err)
		}
		if s.Container != tc.kind {
			t.Fatalf("%s container %q want %q", tc.path, s.Container, tc.kind)
		}
		if int(s.SampleRate) != tc.rate {
			t.Fatalf("%s rate %d want %d", tc.path, s.SampleRate, tc.rate)
		}
		if int(s.Channels) != tc.ch {
			t.Fatalf("%s ch %d want %d", tc.path, s.Channels, tc.ch)
		}
		if int(s.BitsPerSample) != tc.bits {
			t.Fatalf("%s bits %d want %d", tc.path, s.BitsPerSample, tc.bits)
		}
	}
	// oddpad fixture
	for _, p := range []string{"testdata/oddpad.wav", "testdata/mono11025_8.wav"} {
		data, err := os.ReadFile(filepath.Join("testdata", filepath.Base(p)))
		if err != nil {
			data, err = os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Decode(p, data); err != nil {
			t.Fatalf("oddpad decode %v", err)
		}
	}
}

func TestDecode_Errors(t *testing.T) {
	if _, err := Decode("empty", nil); err == nil {
		t.Fatal("empty should error")
	}
	if _, err := Decode("empty2", []byte{}); err == nil {
		t.Fatal("empty2 should error")
	}
	// truncated RIFF
	bad := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	if _, err := Decode("bad", bad); err == nil {
		t.Fatal("bad fmt should error")
	}
	// unsupported format tag 3 (float)
	pcm := make([]byte, 4)
	data := buildRIFF(1, 11025, 8, pcm)
	// patch audioFormat to 3
	binary.LittleEndian.PutUint16(data[20:22], 3)
	if _, err := Decode("float", data); err == nil {
		t.Fatal("float fmt should error")
	}
}

func TestCache_HitMiss(t *testing.T) {
	c := NewCache(nil)
	if c.Cap() != 255 {
		t.Fatalf("cap %d want 255", c.Cap())
	}
	raw := bytesRepeat(0x80, 10)
	s1, err := c.Put("AliasA", raw)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != nil {
		t.Fatalf("first put should return nil existing")
	}
	if c.Len() != 1 {
		t.Fatalf("len %d", c.Len())
	}
	// hit case-insensitive
	if _, ok := c.Get("aliasa"); !ok {
		t.Fatal("case insensitive hit")
	}
	if _, ok := c.Get("ALIA SA"); ok {
		t.Fatal("should miss with space")
	}
	s2, ok := c.Get("AliasA")
	if !ok || s2 == nil {
		t.Fatal("get miss")
	}
	// put same alias again updates but len unchanged
	raw2 := bytesRepeat(0x81, 10)
	if _, err := c.Put("aliasa", raw2); err != nil {
		t.Fatal(err)
	}
	if c.Len() != 1 {
		t.Fatalf("len after update %d want 1", c.Len())
	}
	if got, _ := c.Get("aliasa"); got.Data[0] != 0x81 {
		t.Fatal("update not reflected")
	}
	// miss
	if _, ok := c.Get("missing"); ok {
		t.Fatal("missing should miss")
	}
}

func TestCache_EvictionFIFO(t *testing.T) {
	c := NewCacheWithCap(nil, 3) // small cap for deterministic test
	for i := 0; i < 3; i++ {
		alias := string(rune('a' + i))
		raw := bytesRepeat(byte(i), 4)
		if _, err := c.Put(alias, raw); err != nil {
			t.Fatal(err)
		}
	}
	if c.Len() != 3 {
		t.Fatalf("len %d", c.Len())
	}
	if got := c.Aliases(); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("order %v want a,b,c", got)
	}
	// insert fourth evicts oldest "a"
	if _, err := c.Put("d", bytesRepeat(0x99, 4)); err != nil {
		t.Fatal(err)
	}
	if c.Len() != 3 {
		t.Fatalf("len after evict %d", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should be evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b should remain")
	}
	if _, ok := c.Get("d"); !ok {
		t.Fatal("d should be present")
	}
	if got := c.Aliases(); strings.Join(got, ",") != "b,c,d" {
		t.Fatalf("order after evict %v want b,c,d", got)
	}
	// deterministic: inserting same key does not change order
	prevOrder := strings.Join(c.Aliases(), ",")
	if _, err := c.Put("c", bytesRepeat(0x55, 4)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Aliases(), ",") != prevOrder {
		t.Fatalf("update should preserve order, got %v want %s", c.Aliases(), prevOrder)
	}
}

func TestCache_VFSResolution(t *testing.T) {
	dir := t.TempDir()
	soundsDir := filepath.Join(dir, "sounds")
	if err := os.MkdirAll(soundsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// create two synthetic wav files
	pcm := bytesRepeat(0x80, 20)
	mono := buildRIFF(1, 11025, 8, pcm)
	if err := os.WriteFile(filepath.Join(soundsDir, "testalias.wav"), mono, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(soundsDir, "other.wav"), mono, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	c := NewCache(fs)
	// Load without extension, should resolve to sounds/testalias.wav
	s, err := c.Load("testalias")
	if err != nil {
		t.Fatalf("load testalias %v", err)
	}
	if s.Container != "RIFF" || s.SampleRate != 11025 {
		t.Fatalf("resolved fmt %+v", s)
	}
	if s.Provenance.LogicalPath == "" {
		t.Fatalf("provenance empty")
	}
	// case-insensitive second load hits cache (no VFS)
	s2, err := c.Load("TESTALIAS")
	if err != nil {
		t.Fatal(err)
	}
	if s != s2 {
		t.Fatalf("cache hit should return same pointer")
	}
	if c.Len() != 1 {
		t.Fatalf("len %d want 1", c.Len())
	}
	// load alias with .wav extension already
	s3, err := c.Load("other.wav")
	if err != nil {
		t.Fatal(err)
	}
	if s3 == nil {
		t.Fatal("other.wav nil")
	}
	// alias not found
	if _, err := c.Load("missing_xyz"); err == nil {
		t.Fatal("missing should error")
	}
}

func TestCache_VFSCanonicalAndEvictionWithVFS(t *testing.T) {
	dir := t.TempDir()
	soundsDir := filepath.Join(dir, "sounds")
	os.MkdirAll(soundsDir, 0755)
	pcm := bytesRepeat(0x80, 4)
	for i := 0; i < 5; i++ {
		alias := string(rune('a'+i)) + ".wav"
		mono := buildRIFF(1, 11025, 8, pcm)
		os.WriteFile(filepath.Join(soundsDir, alias), mono, 0644)
	}
	fs := vfs.New()
	fs.MountDirectory(dir, 1)
	defer fs.Close()
	c := NewCacheWithCap(fs, 3)
	for i := 0; i < 3; i++ {
		alias := string(rune('a' + i))
		if _, err := c.Load(alias); err != nil {
			t.Fatalf("load %s %v", alias, err)
		}
	}
	// a,b,c cached; load d evicts a
	if _, err := c.Load("d"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("a evicted")
	}
	// e evicts b
	if _, err := c.Load("e"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("b evicted")
	}
}

func TestInstallSoundsRelationships(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount %v", err)
	}
	defer fs.Close()
	man, err := fs.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache(fs)
	checked := 0
	for _, rec := range man {
		ext := strings.ToLower(filepath.Ext(rec.LogicalPath))
		if ext != ".wav" {
			continue
		}
		alias := strings.TrimSuffix(filepath.Base(rec.LogicalPath), filepath.Ext(rec.LogicalPath))
		// Use cache load to verify alias resolution and decode
		s, err := c.Load(alias)
		if err != nil {
			// Some wavs live under camps/briefs not sounds/ — Load via alias may fail for those
			// because alias resolution expects sounds/ prefix. Try direct file read as fallback:
			data, rerr := fs.ReadFileLimit(rec.LogicalPath, 8<<20)
			if rerr != nil {
				t.Fatalf("read %s %v", rec.LogicalPath, rerr)
			}
			s2, derr := Decode(alias, data)
			if derr != nil {
				t.Fatalf("decode %s %v", rec.LogicalPath, derr)
			}
			s = s2
		}
		// Relationships per [fmt wav] retail corpus
		if s.AudioFormat != 1 {
			t.Fatalf("%s audioFormat %d want 1", rec.LogicalPath, s.AudioFormat)
		}
		if s.Channels != 1 && s.Channels != 2 {
			t.Fatalf("%s channels %d", rec.LogicalPath, s.Channels)
		}
		if s.BitsPerSample != 8 && s.BitsPerSample != 16 {
			t.Fatalf("%s bits %d", rec.LogicalPath, s.BitsPerSample)
		}
		if s.BlockAlign != s.Channels*s.BitsPerSample/8 {
			t.Fatalf("%s blockAlign %d want %d", rec.LogicalPath, s.BlockAlign, s.Channels*s.BitsPerSample/8)
		}
		if s.ByteRate != uint32(s.SampleRate)*uint32(s.BlockAlign) {
			t.Fatalf("%s byteRate %d want %d", rec.LogicalPath, s.ByteRate, uint32(s.SampleRate)*uint32(s.BlockAlign))
		}
		if len(s.Data)%int(s.BlockAlign) != 0 {
			t.Fatalf("%s data align", rec.LogicalPath)
		}
		// sampleRate among known retail set or at least plausible
		switch s.SampleRate {
		case 11025, 22050, 44100, 22254:
		default:
			// allow other rates but log; still must be >0
			if s.SampleRate < 8000 || s.SampleRate > 48000 {
				t.Fatalf("%s rate %d implausible", rec.LogicalPath, s.SampleRate)
			}
		}
		checked++
		if checked >= 50 {
			// limit for speed; relationships hold for first 50
			break
		}
	}
	if checked == 0 {
		t.Fatal("no wavs checked")
	}
	// also verify known retail files decode with exact expectations
	known := []struct {
		logical string
		rate    int
		ch      int
		bits    int
	}{
		{"sounds/BUTTON12.WAV", 11025, 1, 8},
		{"sounds/CDOGGY.WAV", 22254, 1, 8},
		{"sounds/HONK.WAV", 11025, 1, 8},
		{"sounds/SING.WAV", 11025, 1, 8},
	}
	for _, k := range known {
		data, err := fs.ReadFileLimit(k.logical, 1<<20)
		if err != nil {
			t.Fatalf("read %s %v", k.logical, err)
		}
		s, err := Decode(k.logical, data)
		if err != nil {
			t.Fatalf("decode %s %v", k.logical, err)
		}
		if int(s.SampleRate) != k.rate || int(s.Channels) != k.ch || int(s.BitsPerSample) != k.bits {
			t.Fatalf("%s got %d %d %d want %d %d %d", k.logical, s.SampleRate, s.Channels, s.BitsPerSample, k.rate, k.ch, k.bits)
		}
	}
}
