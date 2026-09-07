package save

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestReadSummaryFileKeepsRetailRawPoolFallback(t *testing.T) {
	b := NewBuilder()
	WriteSummary(b, Summary{Description: "raw fallback", IsBattle: true, Gametype: 2})
	data := b.Bytes()
	// A nonzero pool-compression byte with a raw pool is the established
	// decompression-failure fallback: full Open retains those raw bytes.
	data[0x18] = 1
	if _, err := OpenBytes(data); err != nil {
		t.Fatalf("full reader lost raw-pool fallback: %v", err)
	}
	path := filepath.Join(t.TempDir(), "fallback.SAV")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadSummaryFile(path)
	if err != nil || !ok || got.Description != "raw fallback" {
		t.Fatalf("filtered fallback = (%+v,%v,%v)", got, ok, err)
	}
}

func TestReadSummaryFileMatchesFullReaderForTruncatedCompressedScalars(t *testing.T) {
	b := NewBuilder()
	summary := b.Add(SummaryAccount)
	summary.SetInt("Gametype", 2)
	raw := b.Bytes()
	poolOffset := int(binary.LittleEndian.Uint32(raw[0x0c:]))
	logical := raw[BankHeaderSize+AccountHeaderSize : poolOffset]
	if len(logical) != 8 {
		t.Fatalf("one scalar Summary body length = %d, want 8", len(logical))
	}
	packed := encodeRetailSQSH(logical)
	data := make([]byte, BankHeaderSize+AccountHeaderSize+len(packed)+len(raw[poolOffset:]))
	copy(data[:BankHeaderSize], raw[:BankHeaderSize])
	copy(data[BankHeaderSize:], raw[BankHeaderSize:BankHeaderSize+AccountHeaderSize])
	account := data[BankHeaderSize : BankHeaderSize+AccountHeaderSize]
	binary.LittleEndian.PutUint32(account, uint32(AccountHeaderSize+len(packed)))
	binary.LittleEndian.PutUint32(account[8:], 2) // second scalar descriptor is absent
	binary.LittleEndian.PutUint32(account[0x18:], 1)
	copy(data[BankHeaderSize+AccountHeaderSize:], packed)
	newPool := BankHeaderSize + AccountHeaderSize + len(packed)
	copy(data[newPool:], raw[poolOffset:])
	binary.LittleEndian.PutUint32(data[0x0c:], uint32(newPool))

	full, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("full reader: %v", err)
	}
	want, ok := ReadSummary(full)
	if !ok {
		t.Fatal("full reader lost truncated Summary")
	}
	path := filepath.Join(t.TempDir(), "truncated-scalars.SAV")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadSummaryFile(path)
	if err != nil || !ok {
		t.Fatalf("filtered reader = (%+v,%v,%v)", got, ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered Summary = %+v, want full Summary %+v", got, want)
	}
}

func TestReadSummaryFileRejectsPoolOverHostBudget(t *testing.T) {
	b := NewBuilder()
	WriteSummary(b, Summary{Description: "budget", IsBattle: true, Gametype: 2})
	data := b.Bytes()
	poolOffset := binary.LittleEndian.Uint32(data[0x0c:])
	data = append(data[:poolOffset], make([]byte, 19)...)
	copy(data[poolOffset:], []byte("SQSH"))
	data[0x18] = 1
	binary.LittleEndian.PutUint32(data[poolOffset+11:], maxSummaryPoolBytes+1)
	path := filepath.Join(t.TempDir(), "budget.SAV")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSummaryFile(path); !errors.Is(err, ErrFormat) {
		t.Fatalf("over-budget pool error = %v, want ErrFormat", err)
	}
}

func TestReadSummaryFileRejectsDecodedSummaryOverHostBudget(t *testing.T) {
	b := NewBuilder()
	WriteSummary(b, Summary{Description: "budget", IsBattle: true, Gametype: 2, RadarImage: bytes.Repeat([]byte("radar"), 1<<18)})
	data := b.Bytes()
	bank, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	summary, ok := bank.Account(SummaryAccount)
	if !ok || !summary.Compressed {
		t.Fatal("fixture did not produce a compressed Summary body")
	}
	body := int(summary.Offset) + AccountHeaderSize
	binary.LittleEndian.PutUint32(data[body+11:], maxSummaryDecodedBytes+1)
	path := filepath.Join(t.TempDir(), "summary-budget.SAV")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSummaryFile(path); !errors.Is(err, ErrFormat) {
		t.Fatalf("over-budget Summary error = %v, want ErrFormat", err)
	}
}

func TestDecodeSummaryPrefixValidatesCompressedTail(t *testing.T) {
	raw := append([]byte("scalar-prefix"), bytes.Repeat([]byte("radar"), 1<<12)...)
	var packed bytes.Buffer
	w := zlib.NewWriter(&packed)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 19+packed.Len())
	copy(chunk, "SQSH")
	chunk[5] = 2
	binary.LittleEndian.PutUint32(chunk[7:], uint32(packed.Len()))
	binary.LittleEndian.PutUint32(chunk[11:], uint32(len(raw)))
	copy(chunk[19:], packed.Bytes())
	binary.LittleEndian.PutUint32(chunk[15:], sumBytes(chunk[19:]))
	if _, err := decodeSummaryPrefix(chunk, len("scalar-prefix")); err != nil {
		t.Fatalf("valid compressed Summary prefix: %v", err)
	}
	// The scalar bytes precede this zlib trailer. A prefix-only read would
	// accept it; the list reader consumes the stream and rejects the bad tail.
	chunk[len(chunk)-1] ^= 0xff
	binary.LittleEndian.PutUint32(chunk[15:], sumBytes(chunk[19:]))
	if _, err := decodeSummaryPrefix(chunk, len("scalar-prefix")); err == nil {
		t.Fatal("accepted compressed Summary with a malformed trailing stream")
	}
}

func TestDecodeSummaryPrefixValidatesRetailLZTail(t *testing.T) {
	raw := append([]byte("scalar-prefix"), bytes.Repeat([]byte("radar"), 1<<12)...)
	chunk := encodeRetailSQSH(raw)
	if _, err := decodeSummaryPrefix(chunk, len("scalar-prefix")); err != nil {
		t.Fatalf("valid retail LZ Summary prefix: %v", err)
	}
	// Alter the terminator's match-position high bits and retain a valid payload
	// checksum: the stream is malformed only after the prefix.
	term := retailLZTerminator(t, chunk[19:])
	chunk[19+term+1] = 0x10
	binary.LittleEndian.PutUint32(chunk[15:], sumBytes(chunk[19:]))
	if _, err := decodeSummaryPrefix(chunk, len("scalar-prefix")); err == nil {
		t.Fatal("accepted retail LZ Summary with a malformed trailing stream")
	}
}

func retailLZTerminator(t *testing.T, payload []byte) int {
	t.Helper()
	for pos := 0; pos < len(payload); {
		tag := payload[pos]
		pos++
		for bit := 0; bit < 8; bit++ {
			if tag&(1<<bit) == 0 {
				pos++
				continue
			}
			if pos+2 > len(payload) {
				t.Fatal("retail LZ fixture has truncated match")
			}
			if binary.LittleEndian.Uint16(payload[pos:])>>4 == 0 {
				return pos
			}
			pos += 2
		}
	}
	t.Fatal("retail LZ fixture has no terminator")
	return 0
}

// TestReadSummaryFileSkipsUnrelatedCompressedBody makes the skipped account
// undecodable. A full Open therefore warns and drops it, while the list reader
// still obtains Summary without touching that stored payload [08 R-SAVE-02 §1].
func TestReadSummaryFileSkipsUnrelatedCompressedBody(t *testing.T) {
	b := NewBuilder()
	WriteSummary(b, Summary{Description: "listed", IsBattle: true, Gametype: 2})
	bulk := b.Add("Units")
	bulk.AppendBox("bulk", 0, bytes.Repeat([]byte("not summary"), 5000))
	data := b.Bytes()
	// Summary is first. Corrupt the following compressed Units body's SQSH
	// marker; its account header remains valid so the filtered walker can seek
	// past it while the full reader tries and fails to decode it.
	before, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	units := before.Accounts()[1]
	if !units.Compressed {
		t.Fatal("fixture did not compress its unrelated bulk account")
	}
	copy(data[units.Offset+AccountHeaderSize:], []byte("BAD!"))
	path := filepath.Join(t.TempDir(), "bulk.SAV")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	summary, ok, err := ReadSummaryFile(path)
	if err != nil || !ok {
		t.Fatalf("ReadSummaryFile = (%+v,%v,%v)", summary, ok, err)
	}
	if summary.Description != "listed" {
		t.Fatalf("description %q, want listed", summary.Description)
	}
	bank, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(bank.Warnings()) == 0 {
		t.Fatal("full reader did not visit the corrupted unrelated payload")
	}
}

func TestReadSummaryFileMatchesDuplicateSummaryAccountsWithCompressedPool(t *testing.T) {
	b := NewBuilder()
	first := b.Add(SummaryAccount)
	first.SetInt("Gametype", 1)
	first.SetString("Description", "first")
	first.SetString("Mission", "M1")
	second := b.Add(SummaryAccount)
	second.SetInt("Gametype", 2)
	second.SetInt("maxunits", 333)
	second.SetString("Description", "last")
	// Repeated but distinct pool entries make the name pool compress while
	// retaining the same offsets and last-writer-wins account merge.
	fill := b.Add("PoolFill")
	for i := 0; i < 256; i++ {
		fill.SetString("Field"+strconv.Itoa(i), strings.Repeat("repeated-pool-value-", 4)+strconv.Itoa(i))
	}
	data := b.Bytes()
	if data[0x18] == 0 {
		t.Fatal("fixture did not compress the string pool")
	}
	full, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	want, ok := ReadSummary(full)
	if !ok {
		t.Fatal("full reader found no Summary")
	}
	path := filepath.Join(t.TempDir(), "duplicate.SAV")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadSummaryFile(path)
	if err != nil || !ok {
		t.Fatalf("ReadSummaryFile = (%+v,%v,%v)", got, ok, err)
	}
	if got.Description != want.Description || got.Mission != want.Mission || got.Gametype != want.Gametype || got.MaxUnits != want.MaxUnits || got.HasMaxUnits != want.HasMaxUnits {
		t.Fatalf("filtered Summary = %+v, want merged full Summary %+v", got, want)
	}
}

func TestReadSummaryFileSkipsSummaryRadarPayload(t *testing.T) {
	b := NewBuilder()
	WriteSummary(b, Summary{Description: "listed", IsBattle: true, Gametype: 2, RadarImage: bytes.Repeat([]byte("radar"), 1<<18)})
	path := filepath.Join(t.TempDir(), "radar.SAV")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadSummaryFile(path)
	if err != nil || !ok || got.Description != "listed" {
		t.Fatalf("ReadSummaryFile = (%+v,%v,%v)", got, ok, err)
	}
	if len(got.RadarImage) != 0 {
		t.Fatalf("filtered Summary retained %d radar bytes", len(got.RadarImage))
	}
}

// BenchmarkReadSummaryFile keeps listing work bounded by the Summary account
// and name pool, rather than the size of compressed battle payloads.
func BenchmarkReadSummaryFile(b *testing.B) {
	for _, size := range []int{1024, 1 << 20} {
		b.Run("bulk="+strconv.Itoa(size), func(b *testing.B) {
			image := NewBuilder()
			WriteSummary(image, Summary{Description: "listed", IsBattle: true, Gametype: 2})
			image.Add("Units").AppendBox("bulk", 0, bytes.Repeat([]byte("x"), size))
			path := filepath.Join(b.TempDir(), "bulk.SAV")
			if err := os.WriteFile(path, image.Bytes(), 0o600); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok, err := ReadSummaryFile(path); err != nil || !ok {
					b.Fatalf("ReadSummaryFile: ok=%v err=%v", ok, err)
				}
			}
		})
	}
}
