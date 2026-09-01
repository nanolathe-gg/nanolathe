package save

import (
	"bytes"
	"encoding/binary"
	"math"
)

// retailPool is the writer's own string pool, so that the encounter order of
// the image's logical pool is decided by the layout below and nothing else.
type retailPool struct {
	data    []byte
	offsets map[string]uint32
}

func newRetailPool(tag string) *retailPool {
	p := &retailPool{offsets: make(map[string]uint32)}
	p.offset(tag)
	return p
}

func (p *retailPool) offset(value string) uint32 {
	if offset, ok := p.offsets[value]; ok {
		return offset
	}
	offset := uint32(len(p.data))
	p.offsets[value] = offset
	p.data = append(p.data, value...)
	p.data = append(p.data, 0)
	return offset
}

type retailAccountImage struct {
	name    uint32
	stored  []byte
	span    uint32
	packed  bool
	ints    int
	doubles int
	strings int
	boxes   []*Box
}

// Bytes lays out the Builder as a retail HAPIBANK image: accounts in creation
// order, nonempty boxes retaining their order, and the established 34-byte
// bank / 32-byte account headers [08 "Location and representation"]
// [08 R-ENTRY-02 §3].
func (b *Builder) Bytes() []byte {
	if b == nil {
		return nil
	}
	tag := b.tag
	if tag == "" {
		tag = RetailTag
	}
	p := newRetailPool(tag)
	accounts := make([]retailAccountImage, 0, len(b.Accounts))
	accountOffset := uint32(BankHeaderSize)

	for _, account := range b.Accounts {
		if account == nil {
			continue
		}
		boxes := make([]*Box, 0, len(account.Boxes))
		for _, box := range account.Boxes {
			if box != nil && len(box.Data) > 0 {
				boxes = append(boxes, box)
			}
		}
		if len(account.Ints) == 0 && len(account.Doubles) == 0 && len(account.Strings) == 0 && len(boxes) == 0 {
			continue
		}

		image := retailAccountImage{
			name:    p.offset(account.Name),
			ints:    len(account.Ints),
			doubles: len(account.Doubles),
			strings: len(account.Strings),
			boxes:   boxes,
		}
		var body bytes.Buffer
		for _, item := range account.Ints {
			var row [8]byte
			binary.LittleEndian.PutUint32(row[0:], p.offset(item.Name))
			binary.LittleEndian.PutUint32(row[4:], uint32(item.Value))
			body.Write(row[:])
		}
		for _, item := range account.Doubles {
			var row [12]byte
			binary.LittleEndian.PutUint32(row[0:], p.offset(item.Name))
			binary.LittleEndian.PutUint64(row[4:], math.Float64bits(item.Value))
			body.Write(row[:])
		}
		for _, item := range account.Strings {
			var row [8]byte
			binary.LittleEndian.PutUint32(row[0:], p.offset(item.Name))
			binary.LittleEndian.PutUint32(row[4:], p.offset(item.Value))
			body.Write(row[:])
		}

		// Descriptor offsets are absolute offsets into the uncompressed image.
		// They are therefore computed before deciding whether this body is packed
		// [08 R-ENTRY-02 §3].
		payloadOffset := accountOffset + AccountHeaderSize + uint32(body.Len()+len(boxes)*16)
		for _, box := range boxes {
			var row [16]byte
			if box.Name == "" {
				binary.LittleEndian.PutUint32(row[0:], math.MaxUint32) // numbered marker -1
				binary.LittleEndian.PutUint32(row[4:], uint32(box.Number))
			} else {
				binary.LittleEndian.PutUint32(row[0:], p.offset(box.Name))
				// Named boxes carry number zero.
			}
			binary.LittleEndian.PutUint32(row[8:], payloadOffset)
			binary.LittleEndian.PutUint32(row[12:], uint32(len(box.Data)))
			body.Write(row[:])
			payloadOffset += uint32(len(box.Data))
		}
		for _, box := range boxes {
			body.Write(box.Data)
		}
		image.stored, image.packed = retailStoredChunk(body.Bytes())
		image.span = uint32(AccountHeaderSize + len(image.stored))
		accounts = append(accounts, image)
		accountOffset += image.span
	}

	poolFileOffset := accountOffset
	storedPool, poolPacked := retailStoredChunk(p.data)
	image := make([]byte, BankHeaderSize, int(poolFileOffset)+len(storedPool))
	for _, account := range accounts {
		var head [AccountHeaderSize]byte
		binary.LittleEndian.PutUint32(head[0:], account.span)
		binary.LittleEndian.PutUint32(head[4:], account.name)
		binary.LittleEndian.PutUint32(head[8:], uint32(account.ints))
		binary.LittleEndian.PutUint32(head[0x0C:], uint32(account.doubles))
		binary.LittleEndian.PutUint32(head[0x10:], uint32(account.strings))
		binary.LittleEndian.PutUint32(head[0x14:], uint32(len(account.boxes)))
		if account.packed {
			binary.LittleEndian.PutUint32(head[0x18:], 1)
		}
		image = append(image, head[:]...)
		image = append(image, account.stored...)
	}
	image = append(image, storedPool...)

	copy(image[:8], bankMagic)
	binary.LittleEndian.PutUint32(image[0x08:], 0) // tag is first pool string
	binary.LittleEndian.PutUint32(image[0x0C:], poolFileOffset)
	binary.LittleEndian.PutUint32(image[0x10:], BankHeaderSize)
	binary.LittleEndian.PutUint32(image[0x14:], bankVersion)
	if poolPacked {
		image[0x18] = 1
	}
	return image
}

// retailStoredChunk keeps one SQSH chunk only when its complete 19-byte
// framing is strictly shorter than the raw logical bytes [fmt hpi] [08
// R-ENTRY-02 §3]. The payload has no chunk obfuscation.
func retailStoredChunk(raw []byte) ([]byte, bool) {
	chunk := encodeRetailSQSH(raw)
	if len(chunk) < len(raw) {
		return chunk, true
	}
	return raw, false
}

func encodeRetailSQSH(raw []byte) []byte {
	payload := encodeRetailLZ(raw)
	chunk := make([]byte, 19+len(payload))
	copy(chunk, []byte("SQSH"))
	chunk[4] = 2
	chunk[5] = 1
	chunk[6] = 0
	binary.LittleEndian.PutUint32(chunk[7:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(chunk[11:], uint32(len(raw)))
	binary.LittleEndian.PutUint32(chunk[15:], sumBytes(payload))
	copy(chunk[19:], payload)
	return chunk
}

// encodeRetailLZ is a deterministic greedy encoder for the byte-oriented
// 4096-byte-window method-1 stream [fmt hpi]. The search uses the full recent
// window for each two-byte prefix and accepts the first longest match in
// newest-to-oldest order; match lengths are bounded at the format's 17 bytes.
func encodeRetailLZ(src []byte) []byte {
	positions := make(map[uint16][]int)
	var payload bytes.Buffer
	for pos := 0; pos < len(src); {
		var tag byte
		items := make([]byte, 0, 16)
		itemCount := 0
		for itemCount < 8 && pos < len(src) {
			matchPos, matchLen := retailMatch(src, pos, positions)
			if matchLen >= 2 {
				tag |= 1 << itemCount
				word := uint16((1+matchPos)&0xfff)<<4 | uint16(matchLen-2)
				items = append(items, byte(word), byte(word>>8))
				for i := 0; i < matchLen; i++ {
					retailRemember(positions, src, pos+i)
				}
				pos += matchLen
				itemCount++
				continue
			}
			items = append(items, src[pos])
			retailRemember(positions, src, pos)
			pos++
			itemCount++
		}
		// The terminator is an item in the same tag group. Placing it in a
		// separate group after a partial final group would make the unused zero
		// tag bits look like literal bytes to the decoder.
		if pos == len(src) && itemCount < 8 {
			tag |= 1 << itemCount
			items = append(items, 0, 0)
			payload.WriteByte(tag)
			payload.Write(items)
			// The final zero is the normal retail padding byte after the
			// two-byte terminator.
			payload.WriteByte(0)
			return payload.Bytes()
		}
		payload.WriteByte(tag)
		payload.Write(items)
	}
	// A full final group needs a new tag byte for the one-bit terminator.
	payload.WriteByte(1)
	payload.Write([]byte{0, 0, 0})
	return payload.Bytes()
}

func retailRemember(positions map[uint16][]int, src []byte, pos int) {
	if pos+1 >= len(src) {
		return
	}
	key := uint16(src[pos])<<8 | uint16(src[pos+1])
	list := append(positions[key], pos)
	cutoff := pos - 4096
	first := 0
	for first < len(list) && list[first] < cutoff {
		first++
	}
	if first > 0 {
		list = append([]int(nil), list[first:]...)
	}
	positions[key] = list
}

func retailMatch(src []byte, pos int, positions map[uint16][]int) (int, int) {
	if pos+1 >= len(src) {
		return 0, 0
	}
	key := uint16(src[pos])<<8 | uint16(src[pos+1])
	list := positions[key]
	bestPos, bestLen := 0, 0
	for i := len(list) - 1; i >= 0; i-- {
		candidate := list[i]
		distance := pos - candidate
		if distance > 4096 {
			break
		}
		windowPos := (1 + candidate) & 0xfff
		if windowPos == 0 {
			continue
		}
		length := 0
		for length < 17 && pos+length < len(src) {
			reference := candidate + length
			if reference >= pos {
				reference = pos + (reference-pos)%distance
			}
			if src[pos+length] != src[reference] {
				break
			}
			length++
		}
		if length > bestLen {
			bestPos, bestLen = candidate, length
			if bestLen == 17 {
				break
			}
		}
	}
	if bestLen < 2 {
		return 0, 0
	}
	return bestPos, bestLen
}
