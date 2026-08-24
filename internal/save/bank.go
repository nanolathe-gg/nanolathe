// Package save implements the retail HAPIBANK bank container used by battle
// saves [08 "Location and representation"].
//
// Byte layout is the contract here (I13 exception): the 34-byte header and
// 32-byte account headers are defined by exact file offsets, not by Go
// struct layout. All integers are little-endian.
//
// C13 divergence: header offsets and the tag offset are not range-checked
// before use in retail. We bound them (mirroring PLAN_01 archive hardening;
// sanctioned exception to I11) and reject out-of-range banks with ErrFormat.
package save

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

const (
	BankHeaderSize    = 0x22 // 34 bytes [08 "Location and representation"]
	AccountHeaderSize = 32   // 32 bytes [08 "Location and representation"]
	bankVersion       = uint32(1)
	RetailTag         = "Total Annihilation 3.0"
)

var bankMagic = []byte("HAPIBANK")

var (
	ErrMagic   = errors.New("save: not a HAPIBANK container")
	ErrVersion = errors.New("save: unsupported bank version")
	ErrTag     = errors.New("save: unexpected bank tag")
	ErrFormat  = errors.New("save: malformed bank")
)

const (
	diagPoolDecompress    = "HapiBank::OpenBank::Decompression failed"
	diagAccountDecompress = "HapiBank::LoadAccount::Decompression failed"
)

// TODO(question): compressed bank framing is not established beyond the
// flag and the diagnostic strings [08 "Location and representation"].
// The pool/account body flag is 0 raw / 1 compressed and the reader is said
// to use "the same decompressor the archive reader uses" [08 "Location and representation"],
// but the on-disk framing (zlib stream vs SQSH chunk table+header, chunk
// size, checksums) is not specified. We treat flag==1 as opaque: attempt a
// single zlib inflate and on failure emit the verbatim diagnostic and
// continue parsing with the raw bytes.

type IntItem struct {
	Name  string
	Value int32
}

type DoubleItem struct {
	Name  string
	Value float64
}

type StringItem struct {
	Name, Value string
}

type Box struct {
	Name   string
	Number int32
	Data   []byte
}

type Account struct {
	Name    string
	Ints    []IntItem
	Doubles []DoubleItem
	Strings []StringItem
	Boxes   []*Box
	// raw container fields for seam with boxes.go
	Span       uint32
	NameOffset uint32
	Offset     uint32
	Compressed bool
	Body       []byte
}

func (a *Account) SetInt(name string, value int32) {
	for i := range a.Ints {
		if a.Ints[i].Name == name {
			a.Ints[i].Value = value
			return
		}
	}
	a.Ints = append(a.Ints, IntItem{Name: name, Value: value})
}

func (a *Account) SetDouble(name string, value float64) {
	for i := range a.Doubles {
		if a.Doubles[i].Name == name {
			a.Doubles[i].Value = value
			return
		}
	}
	a.Doubles = append(a.Doubles, DoubleItem{Name: name, Value: value})
}

func (a *Account) SetString(name, value string) {
	for i := range a.Strings {
		if a.Strings[i].Name == name {
			a.Strings[i].Value = value
			return
		}
	}
	a.Strings = append(a.Strings, StringItem{Name: name, Value: value})
}

func (a *Account) AppendBox(name string, number int32, data []byte) {
	for _, box := range a.Boxes {
		if box.Name == name && box.Number == number {
			box.Data = append(box.Data, data...)
			return
		}
	}
	a.Boxes = append(a.Boxes, &Box{Name: name, Number: number, Data: append([]byte(nil), data...)})
}

func (a *Account) Int(name string) (int32, bool) {
	for _, item := range a.Ints {
		if item.Name == name {
			return item.Value, true
		}
	}
	return 0, false
}

func (a *Account) Double(name string) (float64, bool) {
	for _, item := range a.Doubles {
		if item.Name == name {
			return item.Value, true
		}
	}
	return 0, false
}

func (a *Account) Str(name string) (string, bool) {
	for _, item := range a.Strings {
		if item.Name == name {
			return item.Value, true
		}
	}
	return "", false
}

func (a *Account) BoxData(name string, number int32) ([]byte, bool) {
	found := false
	var out []byte
	for _, box := range a.Boxes {
		if box.Name == name && box.Number == number {
			out = append(out, box.Data...)
			found = true
		}
	}
	return out, found
}

// Builder assembles a bank from accounts plus its shared string pool.
type Builder struct {
	pool     map[string]uint32
	order    []string
	Accounts []*Account
	tag      string
}

func NewBuilder(tag string) *Builder {
	if tag == "" {
		tag = RetailTag
	}
	b := &Builder{pool: make(map[string]uint32), tag: tag}
	b.PoolOffset(b.tag)
	return b
}

func (b *Builder) PoolOffset(value string) uint32 {
	if offset, ok := b.pool[value]; ok {
		return offset
	}
	offset := uint32(0)
	for _, existing := range b.order {
		offset += uint32(len(existing)) + 1
	}
	b.pool[value] = offset
	b.order = append(b.order, value)
	return offset
}

func (b *Builder) Add(name string) *Account {
	account := &Account{Name: name}
	b.Accounts = append(b.Accounts, account)
	return account
}

// Bytes lays out header, accounts, and pool. Account bodies are uncompressed;
// box payload offsets are absolute within this uncompressed image
// [08 "Location and representation"].
func (b *Builder) Bytes() []byte {
	var out bytes.Buffer
	type laidBox struct {
		box    *Box
		offset uint32
	}
	// Filter empty accounts: empty accounts are not emitted [08 "Location and representation"].
	filtered := make([]*Account, 0, len(b.Accounts))
	for _, ac := range b.Accounts {
		if len(ac.Ints) == 0 && len(ac.Doubles) == 0 && len(ac.Strings) == 0 && len(ac.Boxes) == 0 {
			continue
		}
		filtered = append(filtered, ac)
	}
	accountSpans := make([]uint32, len(filtered))
	accountNameOffsets := make([]uint32, len(filtered))
	accountBoxes := make([][]laidBox, len(filtered))
	accountCursor := uint32(BankHeaderSize)
	for ai, account := range filtered {
		accountNameOffsets[ai] = b.PoolOffset(account.Name)
		itemRows := 0
		itemRows += len(account.Ints) * 8
		itemRows += len(account.Doubles) * 12
		itemRows += len(account.Strings) * 8
		descriptors := len(account.Boxes) * 16
		payloadBase := accountCursor + AccountHeaderSize + uint32(itemRows+descriptors)
		cursor := payloadBase
		laidBoxes := make([]laidBox, len(account.Boxes))
		for bi, box := range account.Boxes {
			laidBoxes[bi].box = box
			laidBoxes[bi].offset = cursor
			cursor += uint32(len(box.Data))
		}
		accountBoxes[ai] = laidBoxes
		accountSpans[ai] = uint32(AccountHeaderSize) + uint32(itemRows+descriptors) + (cursor - payloadBase)
		accountCursor += accountSpans[ai]
	}
	for ai, account := range filtered {
		var head [AccountHeaderSize]byte
		binary.LittleEndian.PutUint32(head[0:], accountSpans[ai])
		binary.LittleEndian.PutUint32(head[4:], accountNameOffsets[ai])
		binary.LittleEndian.PutUint32(head[8:], uint32(len(account.Ints)))
		binary.LittleEndian.PutUint32(head[0x0C:], uint32(len(account.Doubles)))
		binary.LittleEndian.PutUint32(head[0x10:], uint32(len(account.Strings)))
		binary.LittleEndian.PutUint32(head[0x14:], uint32(len(account.Boxes)))
		binary.LittleEndian.PutUint32(head[0x18:], 0) // raw body [08 "Location and representation"]
		// head[0x1C:0x20] reserved zero
		out.Write(head[:])
		for _, item := range account.Ints {
			var row [8]byte
			binary.LittleEndian.PutUint32(row[0:], b.PoolOffset(item.Name))
			binary.LittleEndian.PutUint32(row[4:], uint32(item.Value))
			out.Write(row[:])
		}
		for _, item := range account.Doubles {
			var row [12]byte
			binary.LittleEndian.PutUint32(row[0:], b.PoolOffset(item.Name))
			binary.LittleEndian.PutUint64(row[4:], math.Float64bits(item.Value))
			out.Write(row[:])
		}
		for _, item := range account.Strings {
			var row [8]byte
			binary.LittleEndian.PutUint32(row[0:], b.PoolOffset(item.Name))
			binary.LittleEndian.PutUint32(row[4:], b.PoolOffset(item.Value))
			out.Write(row[:])
		}
		for _, laid := range accountBoxes[ai] {
			var row [16]byte
			if laid.box.Name == "" {
				binary.LittleEndian.PutUint32(row[0:], math.MaxUint32) // -1 marker [08 "Location and representation"]
				binary.LittleEndian.PutUint32(row[4:], uint32(laid.box.Number))
			} else {
				binary.LittleEndian.PutUint32(row[0:], b.PoolOffset(laid.box.Name))
				binary.LittleEndian.PutUint32(row[4:], 0)
			}
			binary.LittleEndian.PutUint32(row[8:], laid.offset)
			binary.LittleEndian.PutUint32(row[12:], uint32(len(laid.box.Data)))
			out.Write(row[:])
		}
		for _, laid := range accountBoxes[ai] {
			out.Write(laid.box.Data)
		}
	}
	poolFileOffset := uint32(out.Len()) + BankHeaderSize
	var pool bytes.Buffer
	for _, value := range b.order {
		pool.WriteString(value)
		pool.WriteByte(0)
	}
	poolBytes := pool.Bytes()
	tagOffset := uint32(0)
	if len(poolBytes) > 0 {
		idx := bytes.Index(poolBytes, []byte(b.tag))
		if idx >= 0 {
			tagOffset = uint32(idx)
		}
	}
	out.Write(poolBytes)
	image := make([]byte, 0, BankHeaderSize+out.Len())
	image = append(image, bytes.Repeat([]byte{0}, BankHeaderSize)...)
	image = append(image, out.Bytes()...)
	return finishHeader(image, tagOffset, poolFileOffset)
}

func finishHeader(image []byte, tagOffset, poolFileOffset uint32) []byte {
	// [08 "Location and representation"] 34-byte header: magic, tag offset, pool offset,
	// first account 0x22, version 1, compression flag 0, reserved zero.
	copy(image[0:], bankMagic)
	binary.LittleEndian.PutUint32(image[0x08:], tagOffset)
	binary.LittleEndian.PutUint32(image[0x0C:], poolFileOffset)
	binary.LittleEndian.PutUint32(image[0x10:], BankHeaderSize) // first account offset 0x22
	binary.LittleEndian.PutUint32(image[0x14:], bankVersion)
	image[0x18] = 0
	// image[0x19:0x22] stay reserved zero
	return image
}

// Bank is an opened bank ready for account access.
type Bank struct {
	Tag            string
	Pool           []byte
	data           []byte
	poolFileOffset uint32
	firstAccount   uint32
	accounts       []*Account
	warnings       []string
}

// Warnings returns decompression diagnostics emitted during open.
func (b *Bank) Warnings() []string { return append([]string(nil), b.warnings...) }

// Accounts exposes the parsed account list in file order [08 "Location and representation"].
func (b *Bank) Accounts() []*Account { return b.accounts }

// Account finds one account by exact name.
func (b *Bank) Account(name string) (*Account, bool) {
	for _, account := range b.accounts {
		if account.Name == name {
			return account, true
		}
	}
	return nil, false
}

// AccountAt returns the account at index.
func (b *Bank) AccountAt(i int) (*Account, bool) {
	if i < 0 || i >= len(b.accounts) {
		return nil, false
	}
	return b.accounts[i], true
}

// Count returns number of accounts.
func (b *Bank) Count() int { return len(b.accounts) }

// Enumerate calls fn for each account in file order; return false to stop.
func (b *Bank) Enumerate(fn func(*Account) bool) {
	for _, ac := range b.accounts {
		if !fn(ac) {
			return
		}
	}
}

// PoolString resolves a logical pool offset to a string (NUL terminated).
func (b *Bank) PoolString(offset uint32) string { return readPoolString(b.Pool, offset) }

// Open reads a bank from a file path. Wrong magic/version/tag closes the
// file and returns zero (nil bank + error) per [08 "Location and representation"] C12.
func Open(path string) (*Bank, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return OpenBytes(data, RetailTag)
}

// OpenBytes validates the container and parses every account body. The
// expected tag is compared case-insensitively after the pool load, mirroring
// retail order (magic -> version -> pool -> tag) [08 "Location and representation"] C11.
//
// C13: header offsets are bound-checked; retail does not check them. We
// reject out-of-range offsets with ErrFormat and document this divergence.
func OpenBytes(data []byte, expectedTag string) (*Bank, error) {
	if len(data) < BankHeaderSize || !bytes.Equal(data[:8], bankMagic) {
		return nil, ErrMagic
	}
	version := binary.LittleEndian.Uint32(data[0x14:])
	if version != bankVersion {
		return nil, ErrVersion
	}
	// C13: bound header offsets (retail: not range-checked).
	poolFileOffset := binary.LittleEndian.Uint32(data[0x0C:])
	firstAccount := binary.LittleEndian.Uint32(data[0x10:])
	tagOffset := binary.LittleEndian.Uint32(data[0x08:])
	compressionFlag := data[0x18]
	// Reserved 9 bytes ignored per [08 "Location and representation"].

	if poolFileOffset < BankHeaderSize || int(poolFileOffset) > len(data) {
		return nil, ErrFormat
	}
	if firstAccount < BankHeaderSize || firstAccount > poolFileOffset {
		return nil, ErrFormat
	}
	// Allow poolFileOffset == len(data) meaning pool may be empty (should not happen with valid tag).

	var pool []byte
	var warnings []string
	poolRaw := data[poolFileOffset:]
	if compressionFlag != 0 {
		// TODO(question): see package comment on unknown framing.
		dec, err := tryDecompress(poolRaw)
		if err != nil {
			warnings = append(warnings, diagPoolDecompress)
			// Emit verbatim diagnostic and continue with raw pool per C12.
			// Diagnostic is collected in warnings; caller may log it.
			pool = poolRaw
		} else {
			pool = dec
		}
	} else {
		pool = poolRaw
	}
	if int(tagOffset) >= len(pool) {
		return nil, ErrFormat
	}
	tag := readPoolString(pool, tagOffset)
	// C11: tag compared after pool load, case-insensitive.
	checkTag := expectedTag
	if checkTag == "" {
		checkTag = RetailTag
	}
	if !strings.EqualFold(tag, checkTag) {
		return nil, ErrTag
	}
	bank := &Bank{
		Tag:            tag,
		Pool:           pool,
		data:           data,
		poolFileOffset: poolFileOffset,
		firstAccount:   firstAccount,
		warnings:       warnings,
	}
	// C11: enumerate accounts from firstAccount until cursor reaches pool offset,
	// advancing by each account's total stored span.
	cursor := int(firstAccount)
	poolEnd := int(poolFileOffset)
	// Safety bound to avoid infinite loop on malformed spans.
	for iter := 0; iter < 4096 && cursor+AccountHeaderSize <= poolEnd; iter++ {
		if cursor+AccountHeaderSize > len(data) {
			break
		}
		head := data[cursor : cursor+AccountHeaderSize]
		span := binary.LittleEndian.Uint32(head[0:])
		nameOffset := binary.LittleEndian.Uint32(head[4:])
		intCount := int32(binary.LittleEndian.Uint32(head[8:]))
		doubleCount := int32(binary.LittleEndian.Uint32(head[0x0C:]))
		stringCount := int32(binary.LittleEndian.Uint32(head[0x10:]))
		boxCount := int32(binary.LittleEndian.Uint32(head[0x14:]))
		compressed := binary.LittleEndian.Uint32(head[0x18:])
		// Reserved head[0x1C:0x20] ignored.

		// C13: bound span. Retail would advance cursor by span even if it
		// overruns the pool boundary; we stop enumeration rather than
		// reading beyond the pool.
		if span == 0 {
			warnings = append(warnings, fmt.Sprintf("account at %d has zero span", cursor))
			break
		}
		if span < AccountHeaderSize {
			// Retail quirk: span <=32 leaves cursor after header instead of
			// at start+span on the unfiltered path [08 "Location and representation"].
			// We mimic that for compatibility and continue.
			// C12: empty accounts not emitted – but we still advance.
			cursor += AccountHeaderSize
			continue
		}
		if cursor+int(span) > poolEnd || cursor+int(span) > len(data) {
			// C13: bound rejection (retail would overrun).
			warnings = append(warnings, fmt.Sprintf("account span at %d overruns pool", cursor))
			break
		}
		accountOffset := uint32(cursor)
		// Handle compressed body: diagnostic and continue parsing [08 "Location and representation"] C12.
		if compressed != 0 {
			warnings = append(warnings, diagAccountDecompress)
			// For opaque pass-through, skip parsing this account's body but
			// advance by span per enumeration rule, so later accounts remain reachable.
			// TODO(question): if framing were known we would decompress body here.
			cursor += int(span)
			bank.warnings = warnings
			continue
		}
		body := data[cursor+AccountHeaderSize : cursor+int(span)]
		account := &Account{
			Name:       readPoolString(pool, nameOffset),
			Span:       span,
			NameOffset: nameOffset,
			Offset:     accountOffset,
			Compressed: false,
			Body:       append([]byte(nil), body...),
		}
		// Negative counts skip loops rather than reject [08 "Location and representation"].
		if intCount < 0 {
			intCount = 0
		}
		if doubleCount < 0 {
			doubleCount = 0
		}
		if stringCount < 0 {
			stringCount = 0
		}
		if boxCount < 0 {
			boxCount = 0
		}
		read := 0
		for i := int32(0); i < intCount && read+8 <= len(body); i++ {
			nameOff := binary.LittleEndian.Uint32(body[read:])
			val := int32(binary.LittleEndian.Uint32(body[read+4:]))
			name := readPoolString(pool, nameOff)
			account.Ints = append(account.Ints, IntItem{Name: name, Value: val})
			read += 8
		}
		for i := int32(0); i < doubleCount && read+12 <= len(body); i++ {
			nameOff := binary.LittleEndian.Uint32(body[read:])
			bits := binary.LittleEndian.Uint64(body[read+4:])
			name := readPoolString(pool, nameOff)
			account.Doubles = append(account.Doubles, DoubleItem{Name: name, Value: math.Float64frombits(bits)})
			read += 12
		}
		for i := int32(0); i < stringCount && read+8 <= len(body); i++ {
			nameOff := binary.LittleEndian.Uint32(body[read:])
			valOff := binary.LittleEndian.Uint32(body[read+4:])
			name := readPoolString(pool, nameOff)
			value := readPoolString(pool, valOff)
			account.Strings = append(account.Strings, StringItem{Name: name, Value: value})
			read += 8
		}
		// Box descriptors: 16 bytes each [08 "Location and representation"].
		for i := int32(0); i < boxCount && read+16 <= len(body); i++ {
			marker := int32(binary.LittleEndian.Uint32(body[read:]))
			number := int32(binary.LittleEndian.Uint32(body[read+4:]))
			payloadOffset := binary.LittleEndian.Uint32(body[read+8:])
			length := binary.LittleEndian.Uint32(body[read+12:])
			box := &Box{Number: number}
			if marker >= 0 {
				box.Name = readPoolString(pool, uint32(marker))
			} else if marker == -1 {
				box.Name = ""
			} else {
				// Negative marker other than -1: treat as numbered with marker as name offset? Retail not validated.
				box.Name = ""
				box.Number = marker
			}
			// Payload offsets are absolute pre-compression image; for raw banks they are absolute file offsets.
			// We bound them against the full file (C13) and copy the slice.
			// Retail does not validate before copy; we do.
			start := int(payloadOffset)
			end := start + int(length)
			if start >= 0 && end >= start && start < len(data) && end <= len(data) {
				// Ensure payload lies within the account's span area (extra bound).
				if start >= cursor && end <= cursor+int(span) {
					box.Data = append([]byte(nil), data[start:end]...)
				} else {
					// Allow payload outside strict span for compatibility with
					// writers that may place payloads elsewhere? We still bound to file.
					box.Data = append([]byte(nil), data[start:end]...)
				}
			} else {
				// Out of bounds: leave empty but continue.
				box.Data = nil
				warnings = append(warnings, fmt.Sprintf("box payload out of bounds %d+%d", start, length))
			}
			account.Boxes = append(account.Boxes, box)
			read += 16
		}
		// Binary payload bytes follow descriptors; but our Boxes already copied via absolute offsets.
		// For raw banks, data after descriptors is the payload bytes themselves, and descriptor offsets point into that region.
		// The above copy already extracts them; no extra handling needed.

		bank.accounts = append(bank.accounts, account)
		bank.warnings = warnings
		cursor += int(span)
		if cursor == poolEnd {
			break
		}
		if cursor > poolEnd {
			break
		}
	}
	bank.warnings = warnings
	return bank, nil
}

func readPoolString(pool []byte, offset uint32) string {
	if int(offset) >= len(pool) {
		return ""
	}
	end := offset
	for end < uint32(len(pool)) && pool[end] != 0 {
		end++
	}
	return string(pool[offset:end])
}

func tryDecompress(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return src, nil
	}
	// Attempt zlib inflate (standard). This matches the archive zlib path [fmt hpi].
	r, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		return nil, err
	}
	if err := r.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// NormalizeSAV reproduces the retail naming rule: strip the last dot and
// everything after it from the assembled path (not path-component aware), then append .SAV
// [08 "File naming and write policy"].
func NormalizeSAV(name string) string {
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[:dot]
	}
	return name + ".SAV"
}

// WriteFile implements the retail write policy: open directly for
// truncate-write via w+b (existing file truncates at successful open);
// after open, every write and close result is ignored and the caller sees
// success [08 "File naming and write policy"][P1-13 §3].
func WriteFile(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, _ = file.Write(payload)
	_ = file.Close()
	return nil
}

// Dump renders the audit listing shape: account count, per-account name, each
// box's name or number and byte count, and each item's name with its value.
func (b *Bank) Dump() string {
	var out strings.Builder
	fmt.Fprintf(&out, "accounts=%d\n", len(b.accounts))
	for _, account := range b.accounts {
		fmt.Fprintf(&out, "[%s]\n", account.Name)
		for _, item := range account.Ints {
			fmt.Fprintf(&out, "  %s = %d\n", item.Name, item.Value)
		}
		for _, item := range account.Doubles {
			fmt.Fprintf(&out, "  %s = %v\n", item.Name, item.Value)
		}
		for _, item := range account.Strings {
			fmt.Fprintf(&out, "  %s = \"%s\"\n", item.Name, item.Value)
		}
		for _, box := range account.Boxes {
			label := box.Name
			if label == "" {
				label = fmt.Sprintf("#%d", box.Number)
			}
			fmt.Fprintf(&out, "  box %s (%d bytes)\n", label, len(box.Data))
		}
	}
	return out.String()
}

// OpenFile is an alias for Open to provide Bank open/enumerate/account-read seams.
func OpenFile(path string) (*Bank, error) { return Open(path) }
