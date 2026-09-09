package save

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
)

func TestRetailBankWriterLayoutAndOrder(t *testing.T) {
	b := NewBuilder()
	b.Add("empty")
	first := b.Add("first")
	first.SetInt("i", 7)
	first.SetDouble("d", 2.5)
	first.SetString("s", "value")
	first.AppendBox("named", 99, []byte{1, 2})
	first.AppendBox("ignored", 0, nil)
	first.AppendBox("", 4, []byte{3, 4, 5})
	second := b.Add("second")
	second.SetInt("j", -1)

	data := b.Bytes()
	if got := string(data[:8]); got != "HAPIBANK" {
		t.Fatalf("magic %q", got)
	}
	if got := binary.LittleEndian.Uint32(data[0x10:]); got != BankHeaderSize {
		t.Fatalf("first account offset %d", got)
	}
	poolOffset := binary.LittleEndian.Uint32(data[0x0c:])
	if poolOffset >= uint32(len(data)) {
		t.Fatalf("pool offset %d outside %d-byte image", poolOffset, len(data))
	}
	if got := string(data[poolOffset : poolOffset+uint32(len(RetailTag))]); got != RetailTag {
		t.Fatalf("pool tag %q", got)
	}

	bank, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if got := bank.Count(); got != 2 {
		t.Fatalf("account count %d, want 2", got)
	}
	if got := bank.Accounts()[0].Name; got != "first" {
		t.Fatalf("first account %q", got)
	}
	if got, ok := bank.Accounts()[0].BoxData("named", 0); !ok || !bytes.Equal(got, []byte{1, 2}) {
		t.Fatalf("named box %v, present=%v", got, ok)
	}
	if got, ok := bank.Accounts()[0].BoxData("", 4); !ok || !bytes.Equal(got, []byte{3, 4, 5}) {
		t.Fatalf("numbered box %v, present=%v", got, ok)
	}
	if _, ok := bank.Accounts()[0].BoxData("ignored", 0); ok {
		t.Fatal("empty box was emitted")
	}

	firstOffset := int(binary.LittleEndian.Uint32(data[0x10:]))
	firstSpan := int(binary.LittleEndian.Uint32(data[firstOffset:]))
	if firstSpan != 32+8+12+8+16+16+2+3 {
		t.Fatalf("first span %d", firstSpan)
	}
	firstBody := data[firstOffset+32 : firstOffset+firstSpan]
	// Integer, double, string, then descriptors. The named descriptor points
	// at the first payload and the numbered descriptor at the second payload.
	box0 := firstBody[8+12+8:]
	if marker := int32(binary.LittleEndian.Uint32(box0)); marker < 0 {
		t.Fatalf("named marker %d", marker)
	}
	if number := binary.LittleEndian.Uint32(box0[4:]); number != 0 {
		t.Fatalf("named box number %d", number)
	}
	if got := binary.LittleEndian.Uint32(box0[8:]); got != uint32(firstOffset+32+8+12+8+32) {
		t.Fatalf("named payload offset %d", got)
	}
	box1 := box0[16:]
	if marker := int32(binary.LittleEndian.Uint32(box1)); marker != -1 {
		t.Fatalf("numbered marker %d", marker)
	}
	if number := binary.LittleEndian.Uint32(box1[4:]); number != 4 {
		t.Fatalf("numbered box number %d", number)
	}
}

func TestRetailBankWriterDuplicateAccountsAndPoolEncounterOrder(t *testing.T) {
	b := NewBuilder()
	one := b.Add("dup")
	one.SetInt("first", 1)
	two := b.Add("dup")
	two.SetInt("second", 2)
	data := b.Bytes()
	first := int(binary.LittleEndian.Uint32(data[0x10:]))
	span := int(binary.LittleEndian.Uint32(data[first:]))
	second := first + span
	if got := binary.LittleEndian.Uint32(data[second+4:]); got != uint32(len(RetailTag)+1) {
		t.Fatalf("duplicate account name offset %d, want first name offset %d", got, len(RetailTag)+1)
	}
	poolOffset := int(binary.LittleEndian.Uint32(data[0x0c:]))
	pool := data[poolOffset:]
	want := []byte(RetailTag + "\x00dup\x00first\x00second\x00")
	if !bytes.Equal(pool, want) {
		t.Fatalf("pool %q, want %q", pool, want)
	}
	bank, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if got := bank.Count(); got != 1 {
		t.Fatalf("merged account count %d, want 1", got)
	}
	merged, ok := bank.Account("dup")
	if !ok {
		t.Fatal("merged account missing")
	}
	if _, ok := merged.Int("second"); !ok {
		t.Fatal("merged account lost second item")
	}
}

func TestRetailBankWriterCompressionAndStrictShorter(t *testing.T) {
	b := NewBuilder()
	large := b.Add("large")
	large.AppendBox("blob", 0, bytes.Repeat([]byte("ABCD"), 5000))
	small := b.Add("small")
	small.SetInt("n", 1)
	data := b.Bytes()
	first := int(binary.LittleEndian.Uint32(data[0x10:]))
	firstSpan := int(binary.LittleEndian.Uint32(data[first:]))
	if flag := binary.LittleEndian.Uint32(data[first+0x18:]); flag != 1 {
		t.Fatalf("large body compression flag %d, want 1", flag)
	}
	second := first + firstSpan
	if flag := binary.LittleEndian.Uint32(data[second+0x18:]); flag != 0 {
		t.Fatalf("small body compression flag %d, want 0", flag)
	}
	if poolFlag := data[0x18]; poolFlag != 0 {
		t.Fatalf("pool compression flag %d for small pool, want 0", poolFlag)
	}
	bank, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes compressed bank: %v", err)
	}
	largeAccount, ok := bank.Account("large")
	if !ok {
		t.Fatal("compressed account missing")
	}
	got, ok := largeAccount.BoxData("blob", 0)
	if !ok || !bytes.Equal(got, bytes.Repeat([]byte("ABCD"), 5000)) {
		t.Fatalf("compressed blob round trip: len=%d present=%v", len(got), ok)
	}
	if bank.Accounts()[0].Span != uint32(firstSpan) {
		t.Fatalf("parsed stored span %d, want %d", bank.Accounts()[0].Span, firstSpan)
	}
}

func TestRetailBankWriterCompressesPool(t *testing.T) {
	b := NewBuilder()
	account := b.Add("pool")
	for i := 0; i < 300; i++ {
		account.SetString(fmt.Sprintf("key%03d", i), "repeated pool value")
	}
	data := b.Bytes()
	if data[0x18] != 1 {
		t.Fatal("compressible string pool was not packed")
	}
	bank, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes compressed pool: %v", err)
	}
	poolAccount, ok := bank.Account("pool")
	if !ok {
		t.Fatal("compressed pool account missing")
	}
	got, ok := poolAccount.Str("key299")
	if !ok || got != "repeated pool value" {
		t.Fatalf("compressed pool value %q, present=%v", got, ok)
	}
}

func TestRetailStoredChunkRequiresStrictShorter(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}
	stored, packed := retailStoredChunk(raw)
	if packed || !bytes.Equal(stored, raw) {
		t.Fatalf("short raw data stored as packed=%v bytes=%v", packed, stored)
	}
}

func TestRetailSQSHEncoderRoundTrip(t *testing.T) {
	plain := bytes.Repeat([]byte("0123456789abcdef"), 200)
	chunk := encodeRetailSQSH(plain)
	if len(chunk) >= len(plain) {
		t.Fatal("fixture should compress")
	}
	if chunk[4] != 2 || chunk[5] != 1 || chunk[6] != 0 {
		t.Fatalf("SQSH flags %d,%d,%d", chunk[4], chunk[5], chunk[6])
	}
	got, err := decodeSQSH(chunk)
	if err != nil {
		t.Fatalf("decode encoded chunk: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("encoded chunk did not round-trip")
	}
}

func TestRetailLZGoldenFixtures(t *testing.T) {
	mixed := make([]byte, 0, 10000)
	for i := 0; i < 2500; i++ {
		mixed = append(mixed, byte(i), byte(i>>3), 'M', 'X')
	}
	boundary := make([]byte, 4096+17)
	for i := range boundary[:4096] {
		boundary[i] = byte(i*73 + i/31)
	}
	copy(boundary[4096:], boundary[:17])
	// payloadSHA values were captured from the baseline encoder. They lock the
	// greedy token stream, including its tag grouping and terminator placement.
	fixtures := []struct {
		name       string
		plain      []byte
		payloadSHA string
	}{
		{"repetitive", bytes.Repeat([]byte("AB"), 6000), "065f7b92be964d011c1a2e9913d33b28d1784e011147995e95a855de6a9dd279"},
		{"mixed", mixed, "c89406551d69aabb1ee98d502033cd5f4c3f412237f77dac25edb8c27e953173"},
		{"window-boundary", boundary, "6792156c53eb7c375a79cfacda4a326b6ec862e12c8164656bb674b14f143adc"},
		{"partial-tag", []byte("partial tag"), "82c6f5ea31500063fea13700619ab681a7884e0245bd6720f40caf8630a91b22"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			payload := encodeRetailLZ(fixture.plain)
			if got := fmt.Sprintf("%x", sha256.Sum256(payload)); got != fixture.payloadSHA {
				t.Fatalf("LZ payload SHA-256 %s, want %s", got, fixture.payloadSHA)
			}
			chunk := encodeRetailSQSH(fixture.plain)
			got, err := decodeSQSH(chunk)
			if err != nil {
				t.Fatalf("decodeSQSH: %v", err)
			}
			if !bytes.Equal(got, fixture.plain) {
				t.Fatal("SQSH fixture did not round-trip")
			}
		})
	}
}

func BenchmarkEncodeRetailLZRepetitive(b *testing.B) {
	plain := bytes.Repeat([]byte("AB"), 32768/2)
	b.ReportAllocs()
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = encodeRetailLZ(plain)
	}
}
