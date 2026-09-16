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
	// RetailTag is the container tag every retail save carries, compared
	// case-insensitively after the pool loads [08 "Location and representation"].
	RetailTag = "Total Annihilation 3.0"
	// Summary listing is an untrusted-file boundary. These are host-resource
	// caps, not retail format limits: a list only needs its name pool and scalar
	// Summary rows, never an attacker-sized map snapshot.
	maxSummaryPoolBytes    = 8 << 20
	maxSummaryStoredBytes  = 8 << 20
	maxSummaryScalarBytes  = 1 << 20
	maxSummaryDecodedBytes = 8 << 20
)

var bankMagic = []byte("HAPIBANK")

// The four container rejections, in the order retail applies them: magic,
// version, tag — and ErrFormat for a body this reader will not accept
// [08 "Location and representation"].
var (
	ErrMagic         = errors.New("save: not a HAPIBANK container")
	ErrVersion       = errors.New("save: unsupported bank version")
	ErrTag           = errors.New("save: unexpected bank tag")
	ErrFormat        = errors.New("save: malformed bank")
	errSummaryBudget = errors.New("save: summary resource budget exceeded")
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

// Open reads a bank from a file path. Wrong magic/version/tag closes the
// file and returns zero (nil bank + error) per [08 "Location and representation"] C12.
func Open(path string) (*Bank, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return OpenBytes(data)
}

// ReadSummaryFile reads only the Summary account needed by the save list. It
// validates the same container header, name pool, account spans, tag, and
// duplicate-account lookup rules as Open, but seeks past every unrelated
// account body. For Summary it retains only the scalar prefix; boxes such as
// the radar image are never accumulated. A compressed Summary stream is
// still consumed to validate its complete framing. The explicit host caps
// above reject attacker-declared pool, stored-body, decoded-body, and
// scalar-prefix sizes rather than treating them as retail behavior [08
// R-SAVE-02 §1].
func ReadSummaryFile(path string) (Summary, bool, error) {
	return readSummaryFile(path, false)
}

// ReadSummaryFileWithBoxes is the same filtered read with the Summary account's
// binary boxes retained, which is what the summary panel needs for the one
// **selected** file: the panel "reads only the `Summary` account of the
// selected file" and shows its `Radar Image` box there [08 R-SAVE-02 §3]. The
// slot list keeps using ReadSummaryFile, so enumerating a directory never
// carries one preview raster per file.
func ReadSummaryFileWithBoxes(path string) (Summary, bool, error) {
	return readSummaryFile(path, true)
}

func readSummaryFile(path string, withBoxes bool) (Summary, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return Summary{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Summary{}, false, err
	}
	length := info.Size()
	if length < BankHeaderSize {
		return Summary{}, false, ErrMagic
	}
	header := make([]byte, BankHeaderSize)
	if _, err := io.ReadFull(file, header); err != nil {
		return Summary{}, false, err
	}
	if !bytes.Equal(header[:8], bankMagic) {
		return Summary{}, false, ErrMagic
	}
	if binary.LittleEndian.Uint32(header[0x14:]) != bankVersion {
		return Summary{}, false, ErrVersion
	}
	poolOffset := binary.LittleEndian.Uint32(header[0x0c:])
	firstAccount := binary.LittleEndian.Uint32(header[0x10:])
	tagOffset := binary.LittleEndian.Uint32(header[0x08:])
	if poolOffset < BankHeaderSize || int64(poolOffset) > length || firstAccount < BankHeaderSize || firstAccount > poolOffset {
		return Summary{}, false, ErrFormat
	}
	poolSize := length - int64(poolOffset)
	if poolSize > maxSummaryPoolBytes {
		return Summary{}, false, ErrFormat
	}
	poolRaw := make([]byte, poolSize)
	if _, err := file.ReadAt(poolRaw, int64(poolOffset)); err != nil {
		return Summary{}, false, err
	}
	pool := poolRaw
	if header[0x18] != 0 {
		decoded, err := decodeSummaryPool(poolRaw)
		if err != nil {
			// Retail records a pool-decompression diagnostic then treats the
			// stored bytes as its raw pool. Preserve that fallback, except for
			// this reader's explicit host-resource budget.
			if errors.Is(err, errSummaryBudget) {
				return Summary{}, false, ErrFormat
			}
		} else {
			pool = decoded
		}
	}
	if int(tagOffset) >= len(pool) {
		return Summary{}, false, ErrFormat
	}
	if !strings.EqualFold(readPoolString(pool, tagOffset), RetailTag) {
		return Summary{}, false, ErrTag
	}
	var summary *Account
	cursor := int64(firstAccount)
	poolEnd := int64(poolOffset)
	for iter := 0; iter < 4096 && cursor+AccountHeaderSize <= poolEnd; iter++ {
		head := make([]byte, AccountHeaderSize)
		if _, err := file.ReadAt(head, cursor); err != nil {
			return Summary{}, false, err
		}
		span := binary.LittleEndian.Uint32(head)
		if span == 0 {
			break
		}
		if span < AccountHeaderSize {
			cursor += AccountHeaderSize
			continue
		}
		if cursor+int64(span) > poolEnd {
			break
		}
		nameOffset := binary.LittleEndian.Uint32(head[4:])
		if readPoolString(pool, nameOffset) == SummaryAccount {
			intCount := int32(binary.LittleEndian.Uint32(head[8:]))
			doubleCount := int32(binary.LittleEndian.Uint32(head[0x0c:]))
			stringCount := int32(binary.LittleEndian.Uint32(head[0x10:]))
			prefix, err := summaryScalarPrefixLength(intCount, doubleCount, stringCount)
			if err != nil {
				return Summary{}, false, ErrFormat
			}
			boxCount := int32(0)
			want := prefix
			if withBoxes {
				// The selected file's panel needs the box descriptors and the
				// payloads that follow them, so the whole account body is kept
				// — still under the same host decode cap.
				boxCount = int32(binary.LittleEndian.Uint32(head[0x14:]))
				want = maxSummaryDecodedBytes
			}
			compressed := binary.LittleEndian.Uint32(head[0x18:]) != 0
			bodySize := int(span) - AccountHeaderSize
			if bodySize > maxSummaryStoredBytes {
				return Summary{}, false, ErrFormat
			}
			readSize := bodySize
			if !compressed && want < readSize {
				readSize = want
			}
			body := make([]byte, readSize)
			if _, err := file.ReadAt(body, cursor+AccountHeaderSize); err != nil {
				return Summary{}, false, err
			}
			if compressed {
				body, err = decodeSummaryPrefix(body, want)
				if err != nil {
					if errors.Is(err, errSummaryBudget) {
						return Summary{}, false, ErrFormat
					}
					cursor += int64(span)
					continue
				}
			}
			account := parseSummaryAccount(pool, body, span, uint32(cursor), nameOffset,
				intCount, doubleCount, stringCount, boxCount)
			if summary == nil {
				summary = account
			} else {
				mergeAccount(summary, account)
			}
		}
		cursor += int64(span)
	}
	if summary == nil {
		return Summary{}, false, nil
	}
	decoded, ok := ReadSummary(&Bank{accounts: []*Account{summary}})
	return decoded, ok, nil
}

func summaryScalarPrefixLength(intCount, doubleCount, stringCount int32) (int, error) {
	counts := [3]int32{intCount, doubleCount, stringCount}
	widths := [3]int64{8, 12, 8}
	var total int64
	for i, count := range counts {
		if count > 0 {
			total += int64(count) * widths[i]
		}
	}
	if total < 0 || total > maxSummaryScalarBytes {
		return 0, ErrFormat
	}
	return int(total), nil
}

func decodeSummaryPool(src []byte) ([]byte, error) {
	if len(src) < 19 || string(src[:4]) != "SQSH" {
		return nil, ErrFormat
	}
	decoded := binary.LittleEndian.Uint32(src[11:])
	if int64(decoded) > maxSummaryPoolBytes {
		return nil, errSummaryBudget
	}
	return decodeSummaryPrefix(src, int(decoded))
}

// decodeSummaryPrefix validates the SQSH framing and payload checksum, then
// retains only the scalar rows a Summary reader consumes. Its bounded output is
// a host policy for the filtered list path; Open remains the full-image reader.
func decodeSummaryPrefix(src []byte, want int) ([]byte, error) {
	if len(src) < 19 || string(src[:4]) != "SQSH" || want < 0 {
		return nil, ErrFormat
	}
	method, encoded := src[5], src[6]
	packed := binary.LittleEndian.Uint32(src[7:])
	decoded := binary.LittleEndian.Uint32(src[11:])
	if uint64(packed) > uint64(len(src)-19) {
		return nil, ErrFormat
	}
	if uint64(decoded) > maxSummaryDecodedBytes {
		return nil, errSummaryBudget
	}
	// A descriptor count can overstate a truncated logical body. Full account
	// parsing consumes the available complete scalar rows, so keep that same
	// prefix rather than rejecting the Summary account [08 R-SAVE-02 §1].
	if want > int(decoded) {
		want = int(decoded)
	}
	payload := src[19 : 19+int(packed)]
	if sumBytes(payload) != binary.LittleEndian.Uint32(src[15:]) {
		return nil, ErrFormat
	}
	if encoded != 0 {
		payload = append([]byte(nil), payload...)
		for i := range payload {
			payload[i] = byte((uint16(payload[i]) - uint16(i)) ^ uint16(i))
		}
	}
	switch method {
	case 1:
		return decodeSummaryLZPrefix(payload, want, int(decoded))
	case 2:
		r, err := zlib.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		out, err := decodeSummaryZlibPrefix(r, want, int(decoded))
		closeErr := r.Close()
		if err != nil {
			return nil, err
		}
		return out, closeErr
	default:
		return nil, ErrFormat
	}
}

func decodeSummaryZlibPrefix(r io.Reader, want, total int) ([]byte, error) {
	out := make([]byte, want)
	buf := make([]byte, 32<<10)
	produced := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if produced+n > total {
				return nil, ErrFormat
			}
			if produced < want {
				copy(out[produced:min(produced+n, want)], buf[:min(n, want-produced)])
			}
			produced += n
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if produced != total {
		return nil, ErrFormat
	}
	return out, nil
}

func decodeSummaryLZPrefix(payload []byte, want, total int) ([]byte, error) {
	window := make([]byte, 4096)
	write, pos, produced := 1, 0, 0
	out := make([]byte, 0, want)
	terminated := false
	for !terminated {
		if pos >= len(payload) {
			return nil, ErrFormat
		}
		tag := payload[pos]
		pos++
		for bit := 0; bit < 8; bit++ {
			if tag&(1<<bit) == 0 {
				if pos >= len(payload) {
					return nil, ErrFormat
				}
				if produced >= total {
					return nil, ErrFormat
				}
				v := payload[pos]
				pos++
				if produced < want {
					out = append(out, v)
				}
				produced++
				window[write] = v
				write = (write + 1) & 0xfff
				continue
			}
			if pos+2 > len(payload) {
				return nil, ErrFormat
			}
			word := binary.LittleEndian.Uint16(payload[pos:])
			pos += 2
			match := int(word >> 4)
			if match == 0 {
				terminated = true
				break
			}
			for n := int(word&0xf) + 2; n > 0; n-- {
				if produced >= total {
					return nil, ErrFormat
				}
				v := window[match]
				match = (match + 1) & 0xfff
				if produced < want {
					out = append(out, v)
				}
				produced++
				window[write] = v
				write = (write + 1) & 0xfff
			}
		}
	}
	if produced != total || len(out) != want {
		return nil, ErrFormat
	}
	return out, nil
}

// parseSummaryAccount decodes the scalar rows ReadSummary needs, and the box
// descriptors only when the caller asked for them (boxCount > 0). The save
// list passes zero: Summary's optional radar image is irrelevant to list
// metadata and can be as large as a map, while the summary panel of the one
// selected file wants exactly that box [08 R-SAVE-02 §1] [08 R-SAVE-02 §3].
func parseSummaryAccount(pool, body []byte, span, accountOffset, nameOffset uint32, intCount, doubleCount, stringCount, boxCount int32) *Account {
	account := &Account{Name: SummaryAccount, Span: span, NameOffset: nameOffset, Offset: accountOffset}
	if intCount < 0 {
		intCount = 0
	}
	if stringCount < 0 {
		stringCount = 0
	}
	if doubleCount < 0 {
		doubleCount = 0
	}
	read := 0
	for i := int32(0); i < intCount && read+8 <= len(body); i++ {
		account.Ints = append(account.Ints, IntItem{Name: readPoolString(pool, binary.LittleEndian.Uint32(body[read:])), Value: int32(binary.LittleEndian.Uint32(body[read+4:]))})
		read += 8
	}
	// Doubles precede strings. Their values are not Summary metadata, so skip
	// their fixed-width descriptors without decoding them.
	for i := int32(0); i < doubleCount && read+12 <= len(body); i++ {
		read += 12
	}
	for i := int32(0); i < stringCount && read+8 <= len(body); i++ {
		account.Strings = append(account.Strings, StringItem{Name: readPoolString(pool, binary.LittleEndian.Uint32(body[read:])), Value: readPoolString(pool, binary.LittleEndian.Uint32(body[read+4:]))})
		read += 8
	}
	if boxCount < 0 {
		boxCount = 0
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
		// A descriptor offset is an absolute offset into the pre-compression
		// image, so subtracting the account body base gives the same
		// body-relative position whether or not the body was stored packed
		// [08 R-ENTRY-02 §3]. A payload outside this body is dropped; the
		// panel then has no image, which is the short-read outcome.
		base := uint64(accountOffset) + AccountHeaderSize
		if uint64(payloadOffset) >= base {
			start := uint64(payloadOffset) - base
			if start+uint64(length) <= uint64(len(body)) {
				box.Data = append([]byte(nil), body[start:start+uint64(length)]...)
			}
		}
		account.Boxes = append(account.Boxes, box)
	}
	return account
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
