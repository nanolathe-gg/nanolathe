package replay

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// Writer appends a replay to an output the host owns: the preamble and
// header at once, then entries gathered into chunks. A chunk is sealed and
// written in one Write call when it reaches ChunkTargetBytes, on Flush, and
// on Close, so a file cut short by a crash plays up to its last whole chunk.
// A host flushes about once a minute; each flush writes only the entries
// since the previous one.
//
// Consecutive pumps of the same size are written as one run, which every
// other entry and every flush closes.
//
// Its methods are safe for concurrent use: a recorder appends from the
// simulation thread while the host flushes. Errors are returned, never
// panicked. An output error is sticky: every later call returns it. An
// entry the stream cannot hold — out of order, out of bounds — stops the
// recording instead (Stop), so the file stays a valid replay of everything
// before it and Close ends it with EndStopped.
type Writer struct {
	mu  sync.Mutex
	out io.Writer
	// st is the state of the entries already encoded; cursor also counts the
	// pending run.
	st     streamState
	cursor uint32
	// runCount pumps of runTicks ticks are recorded but not yet encoded.
	runCount, runTicks uint32
	raw                netproto.Writer
	packed             bytes.Buffer
	deflate            *flate.Writer
	err                error
	stopped            error
	closed             bool
}

// errWriterClosed refuses a call after Close.
var errWriterClosed = errors.New("nanolathe: replay writer is closed")

// NewWriter validates h and writes the file's preamble and header to out.
// The Writer never closes out.
func NewWriter(out io.Writer, h Header) (*Writer, error) {
	if out == nil {
		return nil, fmt.Errorf("nanolathe: replay writer refused: logical path output, providers searched [replay format v%d], expected an output", FormatVersion)
	}
	header, err := encodeHeader(h)
	if err != nil {
		return nil, err
	}
	var w netproto.Writer
	w.Raw([]byte(Magic))
	w.U16(FormatVersion)
	w.U32(uint32(len(header)))
	w.Raw(header)
	if _, err := out.Write(w.Bytes()); err != nil {
		return nil, fmt.Errorf("nanolathe: replay header write failed: %w", err)
	}
	deflate, err := flate.NewWriter(nil, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	return &Writer{out: out, st: streamState{kind: h.Kind, seat: h.LocalSeat}, deflate: deflate}, nil
}

// recording is the header's kind and local seat.
func (w *Writer) recording() (Kind, uint8) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.st.kind, w.st.seat
}

// usable is the refusal that ends every append, or nil. The caller holds mu.
func (w *Writer) usable() error {
	switch {
	case w.err != nil:
		return w.err
	case w.closed:
		return errWriterClosed
	}
	return w.stopped
}

// refuse stops the recording at an entry the stream cannot hold. The caller
// holds mu.
func (w *Writer) refuse(path, expected string) error {
	w.stopped = formatError(path, expected)
	return w.stopped
}

// Command records one command that phase 1 applies at tick, with the
// stream position it was stamped with and its encoded payload. The payload
// is copied.
func (w *Writer) Command(tick uint32, seat uint8, position uint64, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return err
	}
	if err := w.encodeRun(); err != nil {
		return err
	}
	if expected := w.st.admitCommand(tick, seat, position, len(payload)); expected != "" {
		return w.refuse(fmt.Sprintf("command at tick %d position %d", tick, position), expected)
	}
	w.begin()
	w.raw.U8(entryCommand)
	w.raw.U32(tick - w.st.cursor)
	w.raw.U8(seat)
	w.raw.U64(position - w.st.position)
	w.raw.U32(uint32(len(payload)))
	w.raw.Raw(payload)
	w.st.recordCommand(tick, position)
	return w.sealIfFull()
}

// Pump records one host pump that ran ticks ticks, ending at lastTick, the
// tick after the previous pump's plus ticks. A pump that runs no tick is not
// recorded.
func (w *Writer) Pump(lastTick uint32, ticks int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return err
	}
	path := fmt.Sprintf("pump ending at tick %d", lastTick)
	if ticks < 1 || uint64(ticks) > uint64(lastTick) || lastTick-uint32(ticks) != w.cursor {
		return w.refuse(path, fmt.Sprintf("a pump of at least one tick following the recorded pumps' tick %d", w.cursor))
	}
	t := uint32(ticks)
	if w.runCount != 0 && t == w.runTicks && w.runCount < ^uint32(0) {
		w.runCount++
		w.cursor = lastTick
		return nil
	}
	if err := w.encodeRun(); err != nil {
		return err
	}
	if expected := w.st.admitPumps(1, t); expected != "" {
		return w.refuse(path, expected)
	}
	w.runCount, w.runTicks, w.cursor = 1, t, lastTick
	return nil
}

// Checksum records the unit checksum after tick: the cadence's checksum the
// session observed during a pump, or one an online seat acknowledged after
// its tick.
func (w *Writer) Checksum(tick uint32, sum [32]byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return err
	}
	if err := w.encodeRun(); err != nil {
		return err
	}
	if expected := w.st.admitChecksum(tick); expected != "" {
		return w.refuse(fmt.Sprintf("checksum at tick %d", tick), expected)
	}
	w.begin()
	w.raw.U8(entryChecksum)
	w.raw.U32(tick - w.st.cursor)
	w.raw.Digest(sum)
	w.st.checksum = tick
	return w.sealIfFull()
}

// Stop ends recording early: the battle can no longer be recorded exactly,
// for example a command the replay form cannot express. Everything already
// recorded stays; later entries are dropped and Close writes EndStopped. The
// first cause is kept.
func (w *Writer) Stop(cause error) {
	if cause == nil {
		cause = errors.New("nanolathe: replay recording stopped")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped == nil && !w.closed {
		w.stopped = cause
	}
}

// Stopped is the cause that stopped the recording early, or nil.
func (w *Writer) Stopped() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped
}

// Flush seals the entries recorded since the last chunk into one chunk and
// writes it, so a crash after it loses nothing before it.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return errWriterClosed
	}
	if err := w.encodeRun(); err != nil {
		return err
	}
	return w.seal()
}

// Close writes the end entry with reason — EndStopped when the recording
// stopped early — and the last chunk. The replay is then complete; the host
// still owns, syncs and closes the output.
func (w *Writer) Close(reason EndReason) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return errWriterClosed
	}
	if err := w.encodeRun(); err != nil {
		return err
	}
	if w.stopped != nil {
		reason = EndStopped
	}
	if expected := w.st.admitEnd(w.st.cursor, reason); expected != "" {
		return formatError("end", expected)
	}
	w.begin()
	w.raw.U8(entryEnd)
	w.raw.U32(w.st.cursor)
	w.raw.U8(uint8(reason))
	w.st.ended = true
	w.closed = true
	return w.seal()
}

// encodeRun writes the pending run of pumps as one entry. The caller holds
// mu.
func (w *Writer) encodeRun() error {
	if w.runCount == 0 {
		return nil
	}
	count, ticks := w.runCount, w.runTicks
	w.runCount, w.runTicks = 0, 0
	if expected := w.st.admitPumps(count, ticks); expected != "" {
		// Pump admitted the run's first pump against this same state, so
		// this is unreachable short of a defect; stop rather than write it.
		return w.refuse("pumps", expected)
	}
	w.begin()
	w.raw.U8(entryPumps)
	w.raw.U32(count)
	w.raw.U32(ticks)
	w.st.pumps(count, ticks)
	return w.sealIfFull()
}

// begin opens a chunk with its preamble before its first entry. The caller
// holds mu.
func (w *Writer) begin() {
	if w.raw.Len() != 0 {
		return
	}
	c := w.st.start()
	w.raw.U32(c.cursor)
	w.raw.U64(c.position)
	w.raw.U32(c.checksum)
	w.raw.U32(c.command)
}

func (w *Writer) sealIfFull() error {
	if w.raw.Len() < ChunkTargetBytes {
		return nil
	}
	return w.seal()
}

// seal compresses the open chunk and writes it with its sizes in one Write.
// The caller holds mu.
func (w *Writer) seal() error {
	raw := w.raw.Bytes()
	if len(raw) == 0 {
		return nil
	}
	w.packed.Reset()
	w.deflate.Reset(&w.packed)
	if _, err := w.deflate.Write(raw); err != nil {
		w.err = fmt.Errorf("nanolathe: replay chunk compression failed: %w", err)
		return w.err
	}
	if err := w.deflate.Close(); err != nil {
		w.err = fmt.Errorf("nanolathe: replay chunk compression failed: %w", err)
		return w.err
	}
	if len(raw) > MaxChunkRawBytes || w.packed.Len() > MaxChunkCompressedBytes {
		w.err = formatError("chunk", fmt.Sprintf("at most %d raw and %d compressed bytes", MaxChunkRawBytes, MaxChunkCompressedBytes))
		return w.err
	}
	var frame netproto.Writer
	frame.U32(uint32(w.packed.Len()))
	frame.U32(uint32(len(raw)))
	frame.Raw(w.packed.Bytes())
	w.raw = netproto.Writer{}
	if _, err := w.out.Write(frame.Bytes()); err != nil {
		w.err = fmt.Errorf("nanolathe: replay chunk write failed: %w", err)
		return w.err
	}
	return nil
}
