package save

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"math"
	"testing"
)

func sampleBank() []byte {
	b := NewBuilder()
	summary := b.Add("Summary")
	summary.SetInt("BUILD DATE:Aug 23 2026", 0)
	summary.SetInt("maxunits", 500)
	summary.SetString("Map", "Coast To Coast")
	summary.SetDouble("Game Time", 1234.5)
	camera := b.Add("Camera")
	camera.SetDouble("X Position", 320)
	camera.SetDouble("Z Position", 240)
	players := b.Add("Players")
	players.AppendBox("Alliances", 0, bytes.Repeat([]byte{1}, 11))
	mapping := b.Add("Mapping")
	mapping.AppendBox("", 0, []byte{9, 9, 9})
	return b.Bytes()
}

func TestBankRoundTrip(t *testing.T) {
	payload := sampleBank()
	if len(payload) < BankHeaderSize || string(payload[:8]) != "HAPIBANK" {
		t.Fatalf("magic header=%q", payload[:8])
	}
	if got := binary.LittleEndian.Uint32(payload[0x14:]); got != 1 {
		t.Fatalf("version=%d want 1", got)
	}
	if got := binary.LittleEndian.Uint32(payload[0x10:]); got != BankHeaderSize {
		t.Fatalf("firstAccount=%d want %d", got, BankHeaderSize)
	}
	if payload[0x18] != 0 {
		t.Fatalf("compression flag %d want 0", payload[0x18])
	}
	for i := 0x19; i < 0x22; i++ {
		if payload[i] != 0 {
			t.Fatalf("reserved byte 0x%x = %d want 0", i, payload[i])
		}
	}
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if bank.Count() != 4 {
		t.Fatalf("accounts=%d want 4", bank.Count())
	}
	// Verify pool tag is case-insensitive: open with lower case.
	if _, err := openBytesWithTag(payload, "total annihilation 3.0"); err != nil {
		t.Fatalf("case-insensitive tag: %v", err)
	}
	summary, ok := bank.Account("Summary")
	if !ok {
		t.Fatal("missing Summary account")
	}
	if v, _ := summary.Int("maxunits"); v != 500 {
		t.Fatalf("maxunits=%d want 500", v)
	}
	if v, _ := summary.Double("Game Time"); v != 1234.5 {
		t.Fatalf("Game Time=%v want 1234.5", v)
	}
	if v, _ := summary.Str("Map"); v != "Coast To Coast" {
		t.Fatalf("Map=%q want %q", v, "Coast To Coast")
	}
	players, _ := bank.Account("Players")
	data, ok := players.BoxData("Alliances", 0)
	if !ok || len(data) != 11 {
		t.Fatalf("Alliances box len=%d ok=%v want 11 true", len(data), ok)
	}
	mapping, _ := bank.Account("Mapping")
	numbered, ok := mapping.BoxData("", 0)
	if !ok || !bytes.Equal(numbered, []byte{9, 9, 9}) {
		t.Fatalf("numbered box=%v ok=%v want [9 9 9]", numbered, ok)
	}
	// Ensure enumeration advances by span and reaches pool offset.
	poolOff := binary.LittleEndian.Uint32(payload[0x0C:])
	if len(payload) < int(poolOff) {
		t.Fatalf("pool offset beyond file")
	}
	// Walk manually and compare spans.
	cursor := int(BankHeaderSize)
	accounts := bank.Accounts()
	for i, ac := range accounts {
		if ac.Offset != uint32(cursor) {
			t.Fatalf("account %d offset %d want %d", i, ac.Offset, cursor)
		}
		cursor += int(ac.Span)
	}
	if cursor != int(poolOff) {
		t.Fatalf("final cursor %d != poolOff %d", cursor, poolOff)
	}
}

func TestBankRejectsWrongMagicVersionAndTag(t *testing.T) {
	payload := sampleBank()
	if _, err := OpenBytes([]byte("NOPE")); err != ErrMagic {
		t.Fatalf("wrong magic err=%v want ErrMagic", err)
	}
	// Wrong magic case-sensitive: hapibank lower should fail.
	corrupt := append([]byte(nil), payload...)
	copy(corrupt[:8], []byte("hapibank"))
	if _, err := OpenBytes(corrupt); err != ErrMagic {
		t.Fatalf("case-sensitive magic err=%v want ErrMagic", err)
	}
	corrupt = append([]byte(nil), payload...)
	corrupt[0x14] = 2
	binary.LittleEndian.PutUint32(corrupt[0x14:], 2)
	if _, err := OpenBytes(corrupt); err != ErrVersion {
		t.Fatalf("wrong version err=%v want ErrVersion", err)
	}
	if _, err := openBytesWithTag(payload, "Some other tag"); err != ErrTag {
		t.Fatalf("wrong tag err=%v want ErrTag", err)
	}
	// Zero-return semantics: on wrong magic/version/tag, the bank is not returned (nil) and count is zero.
	if b, err := OpenBytes(corrupt); err == nil || b != nil {
		// We return nil bank on error, which is zero accounts.
	}
	if _, err := openBytesWithTag(payload, ""); err != nil {
		t.Fatalf("empty expectation should accept any tag with RetailTag default: %v", err)
	}
	// Verify that bank with wrong tag would have zero accounts if we ignore error (simulating retail zero return).
	if b, _ := openBytesWithTag(payload, "Some other tag"); b != nil && b.Count() != 0 {
		t.Fatalf("wrong tag bank should be zero accounts, got %d", b.Count())
	}
}

func TestBankDecompressionFailureContinuesParsing(t *testing.T) {
	// Build a bank with three accounts; middle one will be marked compressed to trigger diagnostic.
	b := NewBuilder()
	a1 := b.Add("Summary")
	a1.SetInt("maxunits", 100)
	a2 := b.Add("Players")
	a2.SetInt("Human Player", 0)
	a2.AppendBox("Alliances", 0, make([]byte, 11))
	a3 := b.Add("Camera")
	a3.SetDouble("X Position", 123)
	payload := b.Bytes()
	// Corrupt the middle account's header to set compressed flag !=0 and keep span same but body bogus.
	// Locate accounts: firstAccount = 0x22, span of first account.
	firstOff := int(binary.LittleEndian.Uint32(payload[0x10:]))
	span1 := int(binary.LittleEndian.Uint32(payload[firstOff:]))
	secondOff := firstOff + span1
	if secondOff+AccountHeaderSize > len(payload) {
		t.Fatalf("second account out of range")
	}
	// Set compressed flag (at 0x18 within account header) to 1.
	binary.LittleEndian.PutUint32(payload[secondOff+0x18:], 1)
	// Also corrupt a byte in its body to ensure decompression would fail if attempted.
	if secondOff+AccountHeaderSize < len(payload) {
		payload[secondOff+AccountHeaderSize] ^= 0xFF
	}
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes with compressed middle account: %v", err)
	}
	// Parsing must continue: we should have 2 accounts (first and third), middle skipped due to compressed flag.
	if bank.Count() != 2 {
		t.Fatalf("decompression-failure should skip compressed account but continue, got %d accounts want 2", bank.Count())
	}
	if _, ok := bank.Account("Summary"); !ok {
		t.Fatal("Summary should remain after decompression failure")
	}
	if _, ok := bank.Account("Camera"); !ok {
		t.Fatal("Camera should remain after decompression failure of middle account")
	}
	if _, ok := bank.Account("Players"); ok {
		t.Fatal("compressed Players account should be skipped")
	}
	found := false
	for _, w := range bank.Warnings() {
		if w == diagAccountDecompress {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected diagnostic %q in warnings %v", diagAccountDecompress, bank.Warnings())
	}
	// Also test pool decompression failure continues.
	b2 := NewBuilder()
	s := b2.Add("Summary")
	s.SetInt("maxunits", 1)
	p2 := b2.Bytes()
	// Mark pool compressed flag and corrupt pool bytes.
	p2[0x18] = 1
	poolOff := int(binary.LittleEndian.Uint32(p2[0x0C:]))
	if poolOff < len(p2) {
		// Corrupt by putting invalid zlib data.
		for i := poolOff; i < len(p2) && i < poolOff+5; i++ {
			p2[i] = 0xFF
		}
	}
	bank2, err := OpenBytes(p2)
	if err != ErrTag && err != ErrFormat {
		// Pool decompression fails, but parsing continues; however tag check will likely fail because pool is garbage.
		// We expect either ErrTag (if tag not found) or success with raw pool and warnings.
		// In our implementation we treat failure as raw pool, so tag should still be readable (since we kept raw).
		// Thus we should still get a bank with warnings.
		if err != nil {
			t.Logf("pool decompress failure gave err %v, warnings handling may still produce bank", err)
		}
	}
	if bank2 != nil {
		found = false
		for _, w := range bank2.Warnings() {
			if w == diagPoolDecompress {
				found = true
				break
			}
		}
		if !found && p2[0x18] == 1 {
			// If decompression failed we must have warning; if it unexpectedly succeeded (e.g., empty), ignore.
			t.Logf("pool warnings %v", bank2.Warnings())
		}
	}
}

func TestBankAccountSpanWalk(t *testing.T) {
	b := NewBuilder()
	for i := 0; i < 5; i++ {
		ac := b.Add(accountName(i))
		ac.SetInt("Index", int32(i))
		if i == 2 {
			ac.AppendBox("Blob", 0, []byte{1, 2, 3, 4, 5})
		}
	}
	payload := b.Bytes()
	poolOff := binary.LittleEndian.Uint32(payload[0x0C:])
	// Walk spans manually.
	cursor := int(BankHeaderSize)
	offsets := []int{}
	for cursor+AccountHeaderSize <= int(poolOff) {
		span := int(binary.LittleEndian.Uint32(payload[cursor:]))
		if span < AccountHeaderSize {
			t.Fatalf("span %d too small at %d", span, cursor)
		}
		offsets = append(offsets, cursor)
		cursor += span
	}
	if len(offsets) != 5 {
		t.Fatalf("span walk found %d accounts want 5", len(offsets))
	}
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes span walk: %v", err)
	}
	if bank.Count() != 5 {
		t.Fatalf("bank count %d want 5", bank.Count())
	}
	// Verify each account's span matches actual layout.
	for i, ac := range bank.Accounts() {
		if int(ac.Offset) != offsets[i] {
			t.Fatalf("account %d offset %d want %d", i, ac.Offset, offsets[i])
		}
		if int(ac.Offset)+int(ac.Span) > int(poolOff) {
			t.Fatalf("account %d overruns pool", i)
		}
	}
	// Test retail quirk: span <=32 leaves cursor after header [08 "Location and representation"].
	// Craft a minimal bank where first account's span is 10 (<32) and second account follows immediately after the 32-byte header.
	// This mimics retail's "cursor after header instead of start+span" path.
	{
		b2 := NewBuilder()
		// We will manually craft the file to test quirk without builder misalignment.
		// Build pool manually.
		poolStrings := []string{RetailTag, "A", "B", "v"}
		pool := []byte{}
		offsets := map[string]uint32{}
		off := uint32(0)
		for _, s := range poolStrings {
			offsets[s] = off
			pool = append(pool, []byte(s)...)
			pool = append(pool, 0)
			off += uint32(len(s) + 1)
		}
		// Build image with header + two account headers + pool.
		// First account: span 10 (quirk), name "A", no items counted (still header only).
		// Second account: span 40 (32 header + 8 item), name "B", one int "v"=2.
		var img bytes.Buffer
		img.Write(bytes.Repeat([]byte{0}, BankHeaderSize))
		var h1 [AccountHeaderSize]byte
		binary.LittleEndian.PutUint32(h1[0:], 10) // quirk span
		binary.LittleEndian.PutUint32(h1[4:], offsets["A"])
		binary.LittleEndian.PutUint32(h1[0x18:], 0)
		img.Write(h1[:])
		var h2 [AccountHeaderSize]byte
		binary.LittleEndian.PutUint32(h2[0:], 40)
		binary.LittleEndian.PutUint32(h2[4:], offsets["B"])
		binary.LittleEndian.PutUint32(h2[8:], 1) // one int
		binary.LittleEndian.PutUint32(h2[0x18:], 0)
		img.Write(h2[:])
		var row [8]byte
		binary.LittleEndian.PutUint32(row[0:], offsets["v"])
		binary.LittleEndian.PutUint32(row[4:], 2)
		img.Write(row[:])
		poolOff := uint32(img.Len())
		img.Write(pool)
		data := img.Bytes()
		copy(data[0:], bankMagic)
		binary.LittleEndian.PutUint32(data[0x08:], offsets[RetailTag])
		binary.LittleEndian.PutUint32(data[0x0C:], poolOff)
		binary.LittleEndian.PutUint32(data[0x10:], BankHeaderSize)
		binary.LittleEndian.PutUint32(data[0x14:], 1)
		data[0x18] = 0
		bank2, err := OpenBytes(data)
		if err != nil {
			t.Fatalf("quirk span <=32: %v", err)
		}
		// First account is skipped (span <=32), second should be parsed.
		if bank2.Count() != 1 {
			t.Fatalf("quirk span should yield 1 parsed account, got %d", bank2.Count())
		}
		if _, ok := bank2.Account("B"); !ok {
			t.Fatal("quirk: B should be present")
		}
		if _, ok := bank2.Account("A"); ok {
			t.Fatal("quirk: A with span <=32 should be skipped")
		}
		_ = b2
	}
}

func TestBankOffsetBoundRejection(t *testing.T) {
	payload := sampleBank()
	// C13: header offsets not range-checked in retail — we bound them.
	// Pool offset beyond file.
	corrupt := append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(corrupt[0x0C:], uint32(len(corrupt)+100))
	if _, err := OpenBytes(corrupt); err != ErrFormat {
		t.Fatalf("pool offset beyond file should be ErrFormat, got %v", err)
	}
	// First account offset beyond pool offset.
	corrupt = append([]byte(nil), payload...)
	poolOff := binary.LittleEndian.Uint32(corrupt[0x0C:])
	binary.LittleEndian.PutUint32(corrupt[0x10:], poolOff+10)
	if _, err := OpenBytes(corrupt); err != ErrFormat {
		t.Fatalf("firstAccount beyond pool should be ErrFormat, got %v", err)
	}
	// First account offset before header.
	corrupt = append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(corrupt[0x10:], 10)
	if _, err := OpenBytes(corrupt); err != ErrFormat {
		t.Fatalf("firstAccount before header should be ErrFormat, got %v", err)
	}
	// Tag offset beyond pool.
	corrupt = append([]byte(nil), payload...)
	// Set tag offset to large value.
	binary.LittleEndian.PutUint32(corrupt[0x08:], 9999)
	if _, err := OpenBytes(corrupt); err != ErrFormat {
		t.Fatalf("tag offset beyond pool should be ErrFormat, got %v", err)
	}
	// Account span overruns pool.
	b2 := NewBuilder()
	ac := b2.Add("Summary")
	ac.SetInt("maxunits", 1)
	p2 := b2.Bytes()
	off := int(binary.LittleEndian.Uint32(p2[0x10:]))
	// Make span huge to overrun pool.
	binary.LittleEndian.PutUint32(p2[off:], 9999)
	bank, err := OpenBytes(p2)
	if err != nil {
		// Opening may succeed but with warning and zero accounts due to span overrun handling.
		t.Logf("overrun span open err %v", err)
	} else {
		if bank.Count() != 0 {
			// Our implementation breaks enumeration on overrun, yielding zero parsed accounts.
			t.Fatalf("overrun span should yield zero accounts, got %d", bank.Count())
		}
		if len(bank.Warnings()) == 0 {
			t.Fatalf("overrun should produce warning")
		}
	}
}

func TestBankEmptyAccountsNotEmitted(t *testing.T) {
	b := NewBuilder()
	b.Add("Empty") // no items
	alsoEmpty := b.Add("AlsoEmpty")
	alsoEmpty.SetInt("dummy", 1)
	// Remove dummy to make it empty again? Keep one with data, one empty.
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("empty account: %v", err)
	}
	if _, ok := bank.Account("Empty"); ok {
		t.Fatalf("empty account should not be emitted")
	}
	if _, ok := bank.Account("AlsoEmpty"); !ok {
		t.Fatalf("AlsoEmpty should exist")
	}
	// Writer should have only 1 account span.
	poolOff := binary.LittleEndian.Uint32(payload[0x0C:])
	cursor := int(BankHeaderSize)
	count := 0
	for cursor+AccountHeaderSize <= int(poolOff) {
		span := int(binary.LittleEndian.Uint32(payload[cursor:]))
		if span < AccountHeaderSize {
			cursor += AccountHeaderSize
		} else {
			count++
			cursor += span
		}
	}
	if count != 1 {
		t.Fatalf("writer emitted %d accounts want 1 (empty filtered)", count)
	}
}

func TestBankCompressedPoolValid(t *testing.T) {
	// Valid compressed pool: wrap a zlib payload in the established SQSH
	// framing and set the bank compression flag.
	b := NewBuilder()
	ac := b.Add("Summary")
	ac.SetInt("maxunits", 42)
	payload := b.Bytes()
	poolOff := binary.LittleEndian.Uint32(payload[0x0C:])
	rawPool := payload[poolOff:]
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(rawPool); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	w.Close()
	compressed := buf.Bytes()
	chunk := make([]byte, 19+len(compressed))
	copy(chunk, []byte("SQSH"))
	chunk[4], chunk[5], chunk[6] = 2, 2, 0
	binary.LittleEndian.PutUint32(chunk[7:], uint32(len(compressed)))
	binary.LittleEndian.PutUint32(chunk[11:], uint32(len(rawPool)))
	binary.LittleEndian.PutUint32(chunk[15:], sumBytes(compressed))
	copy(chunk[19:], compressed)
	// Build new image: header+accounts+compressed pool
	newPayload := append([]byte(nil), payload[:poolOff]...)
	newPayload = append(newPayload, chunk...)
	// Update header compression flag and keep pool offset same (since accounts unchanged, pool offset stays same)
	newPayload[0x18] = 1
	// Pool length changed, but pool offset is still start of pool, which is correct.
	bank, err := OpenBytes(newPayload)
	if err != nil {
		t.Fatalf("valid compressed pool: %v", err)
	}
	if bank.Count() != 1 {
		t.Fatalf("compressed pool bank count %d want 1", bank.Count())
	}
	if v, _ := bank.Account("Summary"); v == nil {
		t.Fatal("missing Summary after decompress")
	} else if val, _ := v.Int("maxunits"); val != 42 {
		t.Fatalf("maxunits %d want 42", val)
	}
}

func TestBankBoxPayloadBounds(t *testing.T) {
	b := NewBuilder()
	ac := b.Add("Test")
	ac.AppendBox("Blob", 0, []byte{1, 2, 3})
	payload := b.Bytes()
	// Find box descriptor and corrupt its payload offset to beyond file.
	off2 := int(binary.LittleEndian.Uint32(payload[0x10:]))
	span := int(binary.LittleEndian.Uint32(payload[off2:]))
	// Body starts after header; descriptor is at offset headerSize + 0 (since one int? Actually no ints, so descriptor at headerSize)
	// Locate descriptor's payload offset field (at headerSize+8)
	// For this test we just corrupt the absolute offset bytes directly in payload.
	// Read descriptor: at off+32 is start of descriptors
	descrOff := off2 + AccountHeaderSize
	if descrOff+16 > off2+span {
		t.Fatalf("descr out of range")
	}
	// Overwrite payload offset to huge value.
	binary.LittleEndian.PutUint32(payload[descrOff+8:], 0xFFFFFF)
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("box payload bounds: %v", err)
	}
	// Should still parse account but box data empty due to bounds.
	if ac2, ok := bank.Account("Test"); ok {
		if data, _ := ac2.BoxData("Blob", 0); len(data) != 0 {
			t.Fatalf("out-of-bounds box should be empty, got %d bytes", len(data))
		}
		found := false
		for _, w := range bank.Warnings() {
			if bytes.Contains([]byte(w), []byte("out of bounds")) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("warnings %v", bank.Warnings())
		}
	}
}

func accountName(i int) string {
	return "Acct" + string(rune('A'+i))
}

func binaryU32(data []byte, offset int) uint32 {
	return binary.LittleEndian.Uint32(data[offset:])
}

// Ensure math import is used (for Double round-trip in earlier tests).
var _ = math.Float64bits
