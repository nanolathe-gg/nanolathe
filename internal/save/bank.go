// The retail HAPIBANK bank container used by battle
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
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

const (
	BankHeaderSize    = 0x22 // 34 bytes [08 "Location and representation"]
	AccountHeaderSize = 32   // 32 bytes [08 "Location and representation"]
	bankVersion       = uint32(1)
	// RetailTag is the container tag every retail save carries, compared
	// case-insensitively after the pool loads [08 "Location and representation"].
	RetailTag = "Total Annihilation 3.0"
)

var bankMagic = []byte("HAPIBANK")

// The four container rejections, in the order retail applies them: magic,
// version, tag — and ErrFormat for a body this reader will not accept
// [08 "Location and representation"].
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

// IntItem is one 32-bit named item of an account [08 "Save-file organization"].
type IntItem struct {
	Name  string
	Value int32
}

// DoubleItem is one named double item of an account.
type DoubleItem struct {
	Name  string
	Value float64
}

// StringItem is one named string item of an account; the value lives in the
// bank's string pool.
type StringItem struct {
	Name, Value string
}

// Box is an account's opaque byte payload, addressed by name or by number.
type Box struct {
	Name   string
	Number int32
	Data   []byte
}

// Account is one named record of a bank: its scalar items and its boxes.
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

// mergeAccount folds a later account occurrence into the first occurrence.
// Retail treats duplicate accounts as one logical account: scalar items are
// last-writer-wins and repeated binary boxes append in descriptor order [08
// "Save-file organization"].
func mergeAccount(dst, src *Account) {
	for _, item := range src.Ints {
		dst.SetInt(item.Name, item.Value)
	}
	for _, item := range src.Doubles {
		dst.SetDouble(item.Name, item.Value)
	}
	for _, item := range src.Strings {
		dst.SetString(item.Name, item.Value)
	}
	for _, box := range src.Boxes {
		dst.AppendBox(box.Name, box.Number, box.Data)
	}
}

// SetInt writes a named 32-bit item, replacing any item of that name.
func (a *Account) SetInt(name string, value int32) {
	for i := range a.Ints {
		if a.Ints[i].Name == name {
			a.Ints[i].Value = value
			return
		}
	}
	a.Ints = append(a.Ints, IntItem{Name: name, Value: value})
}

// SetDouble writes a named double item, replacing any item of that name.
func (a *Account) SetDouble(name string, value float64) {
	for i := range a.Doubles {
		if a.Doubles[i].Name == name {
			a.Doubles[i].Value = value
			return
		}
	}
	a.Doubles = append(a.Doubles, DoubleItem{Name: name, Value: value})
}

// SetString writes a named string item, replacing any item of that name.
func (a *Account) SetString(name, value string) {
	for i := range a.Strings {
		if a.Strings[i].Name == name {
			a.Strings[i].Value = value
			return
		}
	}
	a.Strings = append(a.Strings, StringItem{Name: name, Value: value})
}

// AppendBox appends a box to the account in emission order.
func (a *Account) AppendBox(name string, number int32, data []byte) {
	for _, box := range a.Boxes {
		if box.Name == name && box.Number == number {
			box.Data = append(box.Data, data...)
			return
		}
	}
	a.Boxes = append(a.Boxes, &Box{Name: name, Number: number, Data: append([]byte(nil), data...)})
}

// Int reads a named 32-bit item, reporting whether the account carries one.
func (a *Account) Int(name string) (int32, bool) {
	for _, item := range a.Ints {
		if item.Name == name {
			return item.Value, true
		}
	}
	return 0, false
}

// Double reads a named double item, reporting whether the account carries one.
func (a *Account) Double(name string) (float64, bool) {
	for _, item := range a.Doubles {
		if item.Name == name {
			return item.Value, true
		}
	}
	return 0, false
}

// Str reads a named string item, reporting whether the account carries one.
func (a *Account) Str(name string) (string, bool) {
	for _, item := range a.Strings {
		if item.Name == name {
			return item.Value, true
		}
	}
	return "", false
}

// BoxData reads a box by name, reporting whether the account carries one.
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

// NewBuilder starts a bank carrying the retail tag. There is one tag a save
// bank can be written with, so it is not a parameter; a test that needs a
// foreign tag uses newBuilderWithTag.
func NewBuilder() *Builder { return newBuilderWithTag(RetailTag) }

func newBuilderWithTag(tag string) *Builder {
	if tag == "" {
		tag = RetailTag
	}
	b := &Builder{pool: make(map[string]uint32), tag: tag}
	b.PoolOffset(b.tag)
	return b
}

// PoolOffset interns a string in the bank's string pool and returns its byte
// offset, which is what the account items reference [08 "Location and
// representation"].
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

// Add appends a named account to the bank under construction, in emission
// order.
func (b *Builder) Add(name string) *Account {
	account := &Account{Name: name}
	b.Accounts = append(b.Accounts, account)
	return account
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

// Count returns number of accounts.
func (b *Bank) Count() int { return len(b.accounts) }

// Open reads a bank from a file path. Wrong magic/version/tag closes the
// file and returns zero (nil bank + error) per [08 "Location and representation"] C12.
func Open(path string) (*Bank, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return OpenBytes(data)
}

// OpenBytes validates the container and parses every account body. The
// expected tag is compared case-insensitively after the pool load, mirroring
// retail order (magic -> version -> pool -> tag) [08 "Location and representation"] C11.
//
// C13: header offsets are bound-checked; retail does not check them. We
// reject out-of-range offsets with ErrFormat and document this divergence.
// OpenBytes reads a retail HAPIBANK image. Bank images carry the retail tag;
// openBytesWithTag exists for the malformed-tag cases the parser must still
// reject cleanly.
func OpenBytes(data []byte) (*Bank, error) { return openBytesWithTag(data, RetailTag) }

func openBytesWithTag(data []byte, expectedTag string) (*Bank, error) {
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
		// Account bodies use the same SQSH chunk framing as archive files. The
		// account header stays uncompressed; only the body after it is framed
		// [fmt hpi] [08 R-ENTRY-02 §3].
		body := data[cursor+AccountHeaderSize : cursor+int(span)]
		if compressed != 0 {
			decoded, err := decodeSQSH(body)
			if err != nil {
				warnings = append(warnings, diagAccountDecompress)
				// Keep the retail-compatible skip-on-error behavior used by the
				// existing bank API; staging treats missing accounts as invalid.
				cursor += int(span)
				bank.warnings = warnings
				continue
			}
			body = decoded
			accountCompressed := true
			account := parseAccountBody(pool, body, span, accountOffset, nameOffset, intCount, doubleCount, stringCount, boxCount, data, accountCompressed, &warnings)
			if account != nil {
				if existing, ok := bank.Account(account.Name); ok {
					mergeAccount(existing, account)
				} else {
					bank.accounts = append(bank.accounts, account)
				}
			}
			cursor += int(span)
			bank.warnings = warnings
			continue
		}
		account := parseAccountBody(pool, body, span, accountOffset, nameOffset, intCount, doubleCount, stringCount, boxCount, data, false, &warnings)
		if account == nil {
			cursor += int(span)
			continue
		}
		if existing, ok := bank.Account(account.Name); ok {
			mergeAccount(existing, account)
		} else {
			bank.accounts = append(bank.accounts, account)
		}
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

// parseAccountBody decodes typed rows and boxes from an already detached
// logical body. For raw accounts payload offsets are absolute file offsets;
// compressed accounts retain those offsets from the pre-compression image and
// are translated to body-relative offsets before slicing.
func parseAccountBody(pool, body []byte, span, accountOffset, nameOffset uint32, intCount, doubleCount, stringCount, boxCount int32, fileData []byte, compressed bool, warnings *[]string) *Account {
	account := &Account{Name: readPoolString(pool, nameOffset), Span: span, NameOffset: nameOffset, Offset: accountOffset, Compressed: compressed, Body: append([]byte(nil), body...)}
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
		account.Ints = append(account.Ints, IntItem{Name: readPoolString(pool, nameOff), Value: val})
		read += 8
	}
	for i := int32(0); i < doubleCount && read+12 <= len(body); i++ {
		nameOff := binary.LittleEndian.Uint32(body[read:])
		bits := binary.LittleEndian.Uint64(body[read+4:])
		account.Doubles = append(account.Doubles, DoubleItem{Name: readPoolString(pool, nameOff), Value: math.Float64frombits(bits)})
		read += 12
	}
	for i := int32(0); i < stringCount && read+8 <= len(body); i++ {
		nameOff := binary.LittleEndian.Uint32(body[read:])
		valueOff := binary.LittleEndian.Uint32(body[read+4:])
		account.Strings = append(account.Strings, StringItem{Name: readPoolString(pool, nameOff), Value: readPoolString(pool, valueOff)})
		read += 8
	}
	for i := int32(0); i < boxCount && read+16 <= len(body); i++ {
		marker := int32(binary.LittleEndian.Uint32(body[read:]))
		number := int32(binary.LittleEndian.Uint32(body[read+4:]))
		payloadOffset := binary.LittleEndian.Uint32(body[read+8:])
		length := binary.LittleEndian.Uint32(body[read+12:])
		read += 16
		box := &Box{Number: number}
		if marker >= 0 {
			box.Name = readPoolString(pool, uint32(marker))
		} else if marker < -1 {
			box.Number = marker
		}
		start := -1
		if compressed {
			// Retail writes an absolute offset into the pre-compression image;
			// after decompression it is body-relative [08 R-ENTRY-02 §3].
			bodyBase := uint64(accountOffset) + AccountHeaderSize
			if uint64(payloadOffset) >= bodyBase {
				candidate := uint64(payloadOffset) - bodyBase
				if candidate <= uint64(len(body)) && candidate+uint64(length) <= uint64(len(body)) {
					start = int(candidate)
				}
			}
		} else if payloadOffset <= uint32(len(fileData)) && uint64(payloadOffset)+uint64(length) <= uint64(len(fileData)) {
			start = int(payloadOffset)
		}
		if start >= 0 {
			if compressed {
				box.Data = append([]byte(nil), body[start:start+int(length)]...)
			} else {
				box.Data = append([]byte(nil), fileData[start:start+int(length)]...)
			}
		} else if warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("box payload out of bounds %d+%d", payloadOffset, length))
		}
		account.Boxes = append(account.Boxes, box)
	}
	return account
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
	return decodeSQSH(src)
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
