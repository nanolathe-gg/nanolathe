package replay

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// EntryKind names one recorded entry.
type EntryKind uint8

// The entry kinds a Reader returns.
const (
	// EntryCommand: a command phase 1 applies at Tick, stamped with Seat and
	// Position, its payload a SeatCommand encoding in the header kind's
	// command context.
	EntryCommand EntryKind = EntryKind(entryCommand)
	// EntryPumps: Pumps host pumps of PumpTicks ticks each, ending at Tick.
	EntryPumps EntryKind = EntryKind(entryPumps)
	// EntryChecksum: the unit checksum Sum after Tick.
	EntryChecksum EntryKind = EntryKind(entryChecksum)
	// EntryEnd: the recording ends at Tick for Reason.
	EntryEnd EntryKind = EntryKind(entryEnd)
)

// Entry is one recorded entry, with every tick and position absolute.
type Entry struct {
	Kind     EntryKind
	Tick     uint32
	Seat     uint8
	Position uint64
	// Payload is the command's own copy.
	Payload   []byte
	Pumps     uint32
	PumpTicks uint32
	Sum       [32]byte
	Reason    EndReason
}

// Reader reads a replay held in memory. NewReader checks the preamble and
// header; Next decodes the stream one entry at a time, a chunk at a time,
// checking every entry against the stream so far. A replay of an hour is a
// few hundred kilobytes, so a host reads the whole file first. The Reader
// keeps data and never changes it.
type Reader struct {
	data   []byte
	header Header
	// next is the offset of the next chunk.
	next int
	st   streamState
	// entries reads the current chunk's raw bytes, nil between chunks.
	entries *netproto.Reader
	raw     bytes.Buffer
	inflate io.ReadCloser
	err     error
}

// NewReader checks a replay's preamble and reads its header. It refuses a
// file that is not a replay, a format version this build does not read —
// naming both versions — and a header out of bounds.
func NewReader(data []byte) (*Reader, error) {
	header, next, err := readPreamble(data)
	if err != nil {
		return nil, err
	}
	return &Reader{data: data, header: header, next: next, st: streamState{kind: header.Kind, seat: header.LocalSeat}}, nil
}

// readPreamble reads the magic, version and header, returning the offset of
// the first chunk.
func readPreamble(data []byte) (Header, int, error) {
	if len(data) < len(Magic) || string(data[:len(Magic)]) != Magic {
		return Header{}, 0, formatError("magic", fmt.Sprintf("a file starting %q", Magic))
	}
	r := netproto.NewReader(data[len(Magic):], func(path, expected string) error { return formatError("preamble "+path, expected) })
	version := r.U16()
	if err := r.Err(); err != nil {
		return Header{}, 0, err
	}
	if version != FormatVersion {
		return Header{}, 0, fmt.Errorf("%w: logical path replay format version %d, providers searched [replay format v%d], expected replay format version %d", ErrUnsupportedVersion, version, FormatVersion, FormatVersion)
	}
	size := r.U32()
	if r.Err() == nil && (size == 0 || size > MaxHeaderBytes) {
		r.Fail(fmt.Sprintf("a header of 1..%d bytes", MaxHeaderBytes))
	}
	raw := r.Raw(int(size))
	if err := r.Err(); err != nil {
		return Header{}, 0, err
	}
	h, err := decodeHeader(raw)
	if err != nil {
		return Header{}, 0, err
	}
	return h, len(Magic) + r.Offset(), nil
}

// Header is the replay's header, a copy.
func (r *Reader) Header() Header { return r.header.clone() }

// Next returns the next entry. It returns io.EOF after the end entry and
// ErrTruncated when the file stops without one — mid-chunk or after a whole
// chunk — having returned every entry of every whole chunk. Any other error
// names the malformed part and wraps ErrCorrupt. Errors are sticky.
func (r *Reader) Next() (Entry, error) {
	if r.err != nil {
		return Entry{}, r.err
	}
	for r.entries == nil || r.entries.Offset() == r.raw.Len() {
		r.entries = nil
		if r.st.ended {
			if r.next != len(r.data) {
				return r.fail(formatError(fmt.Sprintf("file byte %d", r.next), "no bytes after the end entry"))
			}
			return r.fail(io.EOF)
		}
		chunk, next, err := chunkAt(r.data, r.next)
		if err != nil {
			return r.fail(err)
		}
		start, err := r.open(chunk, r.next)
		if err != nil {
			return r.fail(err)
		}
		if start != r.st.start() {
			return r.fail(formatError(fmt.Sprintf("chunk at file byte %d", r.next), "a chunk opening where the previous chunk ended"))
		}
		r.next = next
	}
	e, err := readEntry(r.entries, &r.st)
	if err != nil {
		return r.fail(err)
	}
	if e.Kind == EntryEnd && r.entries.Offset() != r.raw.Len() {
		return r.fail(formatError(fmt.Sprintf("chunk ending before file byte %d", r.next), "no entry after the end entry"))
	}
	return e, nil
}

func (r *Reader) fail(err error) (Entry, error) {
	r.err = err
	return Entry{}, err
}

// open inflates a chunk into r.raw and reads its preamble.
func (r *Reader) open(chunk []byte, at int) (chunkStart, error) {
	start, entries, err := inflateChunk(chunk, at, &r.raw, &r.inflate)
	if err != nil {
		return chunkStart{}, err
	}
	r.entries = entries
	return start, nil
}

// chunkAt frames the chunk at offset at: it returns the chunk's encoded
// bytes (its sizes included) and the next chunk's offset. ErrTruncated
// reports a chunk the file stops inside, or no chunk at all at the end of
// the file.
func chunkAt(data []byte, at int) ([]byte, int, error) {
	if at == len(data) {
		return nil, at, ErrTruncated
	}
	off := at
	var sizes [2]uint64
	for i := range sizes {
		v, n := binary.Uvarint(data[off:])
		switch {
		case n == 0:
			return nil, at, ErrTruncated
		case n < 0 || n != netproto.UvarintLen(v) || v > 1<<32-1:
			return nil, at, formatError(fmt.Sprintf("chunk at file byte %d", at), "chunk sizes as shortest 32-bit varints")
		}
		sizes[i] = v
		off += n
	}
	if sizes[0] == 0 || sizes[0] > MaxChunkCompressedBytes || sizes[1] == 0 || sizes[1] > MaxChunkRawBytes {
		return nil, at, formatError(fmt.Sprintf("chunk at file byte %d", at), fmt.Sprintf("1..%d compressed and 1..%d raw bytes", MaxChunkCompressedBytes, MaxChunkRawBytes))
	}
	if sizes[0] > uint64(len(data)-off) {
		return nil, at, ErrTruncated
	}
	end := off + int(sizes[0])
	return data[at:end], end, nil
}

// inflateChunk decompresses a framed chunk into raw and reads its preamble,
// returning a reader positioned at its first entry. It grows raw only as
// the compressed data actually inflates, so a false raw size allocates
// nothing; the inflated size must be exactly the recorded one, with no
// compressed byte left over.
func inflateChunk(chunk []byte, at int, raw *bytes.Buffer, inflate *io.ReadCloser) (chunkStart, *netproto.Reader, error) {
	path := fmt.Sprintf("chunk at file byte %d", at)
	compressed, n1 := binary.Uvarint(chunk)
	size, n2 := binary.Uvarint(chunk[n1:])
	packed := bytes.NewReader(chunk[n1+n2:])
	if *inflate == nil {
		*inflate = flate.NewReader(packed)
	} else if err := (*inflate).(flate.Resetter).Reset(packed, nil); err != nil {
		return chunkStart{}, nil, formatError(path, "a DEFLATE stream")
	}
	raw.Reset()
	n, err := io.Copy(raw, io.LimitReader(*inflate, int64(size)+1))
	if err != nil || uint64(n) != size || packed.Len() != 0 || uint64(len(chunk)-n1-n2) != compressed {
		return chunkStart{}, nil, formatError(path, fmt.Sprintf("one DEFLATE stream of %d bytes inflating to exactly %d bytes", compressed, size))
	}
	entries := netproto.NewReader(raw.Bytes(), func(p, expected string) error { return formatError(path+" "+p, expected) })
	var start chunkStart
	start.cursor = entries.U32()
	start.position = entries.U64()
	start.checksum = entries.U32()
	start.command = entries.U32()
	if err := entries.Err(); err != nil {
		return chunkStart{}, nil, err
	}
	if entries.Offset() == raw.Len() {
		return chunkStart{}, nil, formatError(path, "at least one entry after the chunk's opening state")
	}
	return start, entries, nil
}

// readEntry decodes one entry, checks it against st and advances st.
func readEntry(r *netproto.Reader, st *streamState) (Entry, error) {
	at := r.Offset()
	refuse := func(expected string) (Entry, error) {
		r.FailAt(at, expected)
		return Entry{}, r.Err()
	}
	var e Entry
	switch kind := r.U8(); kind {
	case entryCommand:
		delta := r.U32()
		e.Seat = r.U8()
		step := r.U64()
		size := r.U32()
		if r.Err() != nil {
			return Entry{}, r.Err()
		}
		e.Kind = EntryCommand
		e.Tick = st.cursor + delta
		e.Position = st.position + step
		if delta == 0 || uint64(st.cursor)+uint64(delta) > 1<<32-1 || step == 0 || st.position+step < st.position {
			return refuse("a command after the recorded pumps with a later stream position")
		}
		if expected := st.admitCommand(e.Tick, e.Seat, e.Position, int(min(uint64(size), uint64(netproto.MaxCommandBytes)+1))); expected != "" {
			return refuse(expected)
		}
		e.Payload = append([]byte(nil), r.Raw(int(size))...)
		if r.Err() != nil {
			return Entry{}, r.Err()
		}
		st.recordCommand(e.Tick, e.Position)
	case entryPumps:
		e.Pumps = r.U32()
		e.PumpTicks = r.U32()
		if r.Err() != nil {
			return Entry{}, r.Err()
		}
		if expected := st.admitPumps(e.Pumps, e.PumpTicks); expected != "" {
			return refuse(expected)
		}
		st.pumps(e.Pumps, e.PumpTicks)
		e.Kind, e.Tick = EntryPumps, st.cursor
	case entryChecksum:
		delta := r.U32()
		e.Sum = r.Digest()
		if r.Err() != nil {
			return Entry{}, r.Err()
		}
		if uint64(st.cursor)+uint64(delta) > 1<<32-1 {
			return refuse("a checksum tick within 32 bits")
		}
		e.Kind, e.Tick = EntryChecksum, st.cursor+delta
		if expected := st.admitChecksum(e.Tick); expected != "" {
			return refuse(expected)
		}
		st.checksum = e.Tick
	case entryEnd:
		e.Tick = r.U32()
		e.Reason = EndReason(r.U8())
		if r.Err() != nil {
			return Entry{}, r.Err()
		}
		if expected := st.admitEnd(e.Tick, e.Reason); expected != "" {
			return refuse(expected)
		}
		e.Kind = EntryEnd
		st.ended = true
	default:
		if r.Err() != nil {
			return Entry{}, r.Err()
		}
		return refuse("entry kind 1 command, 2 pumps, 3 checksum or 4 end")
	}
	return e, nil
}

// Summary is what a list screen shows of a replay without playing it.
type Summary struct {
	Header Header
	// FinalTick is the end entry's final tick, or the last tick of the last
	// whole chunk's pumps when the recording stops without one.
	FinalTick uint32
	// End is the end entry's reason, zero when the recording has none.
	End EndReason
}

// Summarize reads a replay's header and its final tick, inflating only the
// last whole chunk: every chunk opens with the stream state at its start.
// It does not check the chunks before the last; playing them does.
func Summarize(data []byte) (Summary, error) {
	h, at, err := readPreamble(data)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{Header: h}
	var last []byte
	lastAt := at
	for at < len(data) {
		chunk, next, err := chunkAt(data, at)
		if errors.Is(err, ErrTruncated) {
			break
		}
		if err != nil {
			return Summary{}, err
		}
		last, lastAt, at = chunk, at, next
	}
	if last == nil {
		return sum, nil
	}
	var raw bytes.Buffer
	var inflate io.ReadCloser
	start, entries, err := inflateChunk(last, lastAt, &raw, &inflate)
	if err != nil {
		return Summary{}, err
	}
	st := streamState{kind: h.Kind, seat: h.LocalSeat}
	st.adopt(start)
	for entries.Offset() != raw.Len() {
		e, err := readEntry(entries, &st)
		if err != nil {
			return Summary{}, err
		}
		if e.Kind == EntryEnd {
			sum.End = e.Reason
		}
	}
	sum.FinalTick = st.cursor
	return sum, nil
}
