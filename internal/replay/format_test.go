package replay

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// testHeader is an authored header. The format never decodes the
// configuration, so any bytes stand in for it here.
func testHeader(kind Kind) Header {
	return Header{
		Kind:   kind,
		Config: []byte{1, 2, 3},
		Identity: netproto.Identity{Protocol: netproto.CommandSchemaVersion, Content: [32]byte{4}, Configuration: [32]byte{5},
			Rules: netproto.RuleIdentity{Name: "modern", Base: 2, Community: [32]byte{6}},
			Mod:   netproto.ModIdentity{ID: "mod", Version: "1.0", Archive: [32]byte{7}}},
		InitialChecksum: [32]byte{9},
		MapName:         "portable",
		Seats: []Seat{{Name: "Player", Side: "ARM", Color: 0, Role: session.MatchRoleHuman},
			{Name: "Computer", Side: "CORE", Color: 1, Role: session.MatchRoleComputer}},
		Started: 1760000000000,
	}
}

// op is one recorder call.
type op struct {
	kind     EntryKind
	tick     uint32
	ticks    int
	position uint64
	seat     uint8
	payload  []byte
	sum      [32]byte
	flush    bool
}

// singlePlayerScript is a recording of pumps of one to five ticks, commands
// applied inside them and at a paused boundary, the 30-tick checksums
// reported during the pumps that contain them, and host flushes.
func singlePlayerScript(pumps int) []op {
	var ops []op
	tick := uint32(0)
	position := uint64(0)
	next30 := uint32(30)
	for i := 0; i < pumps; i++ {
		ticks := 1 + (i/7)%5
		if i%11 == 3 {
			// Paused boundary: applied at the next tick, no pump.
			position += 2
			ops = append(ops, op{kind: EntryCommand, tick: tick + 1, position: position, payload: []byte{byte(i), 1}})
		}
		if i%5 == 0 {
			position++
			ops = append(ops, op{kind: EntryCommand, tick: tick + 1, position: position, payload: bytes.Repeat([]byte{byte(i)}, 1+i%9)})
		}
		if next30 <= tick+uint32(ticks) {
			ops = append(ops, op{kind: EntryChecksum, tick: next30, sum: [32]byte{byte(next30), byte(next30 >> 8)}})
			next30 += 30
		}
		tick += uint32(ticks)
		ops = append(ops, op{kind: EntryPumps, tick: tick, ticks: ticks})
		if i%40 == 39 {
			ops = append(ops, op{flush: true})
		}
	}
	return ops
}

// record writes ops to a new replay and returns its bytes and the offsets
// at which each chunk ended.
func record(t *testing.T, h Header, ops []op, reason EndReason) ([]byte, []int) {
	t.Helper()
	var out bytes.Buffer
	w, err := NewWriter(&out, h)
	if err != nil {
		t.Fatal(err)
	}
	boundaries := []int{out.Len()}
	for _, o := range ops {
		switch {
		case o.flush:
			err = w.Flush()
		case o.kind == EntryCommand:
			err = w.Command(o.tick, o.seat, o.position, o.payload)
		case o.kind == EntryChecksum:
			err = w.Checksum(o.tick, o.sum)
		case o.kind == EntryPumps:
			err = w.Pump(o.tick, o.ticks)
		}
		if err != nil {
			t.Fatal(err)
		}
		if out.Len() != boundaries[len(boundaries)-1] {
			boundaries = append(boundaries, out.Len())
		}
	}
	if err := w.Close(reason); err != nil {
		t.Fatal(err)
	}
	boundaries = append(boundaries, out.Len())
	return out.Bytes(), boundaries
}

// readAll returns every entry and the error that ended the stream.
func readAll(t *testing.T, data []byte) ([]Entry, error) {
	t.Helper()
	r, err := NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for {
		e, err := r.Next()
		if err != nil {
			return entries, err
		}
		entries = append(entries, e)
	}
}

// replayed expands read entries back into recorder calls, one per pump.
func replayed(entries []Entry) []op {
	var ops []op
	for _, e := range entries {
		switch e.Kind {
		case EntryCommand:
			ops = append(ops, op{kind: EntryCommand, tick: e.Tick, position: e.Position, seat: e.Seat, payload: e.Payload})
		case EntryChecksum:
			ops = append(ops, op{kind: EntryChecksum, tick: e.Tick, sum: e.Sum})
		case EntryPumps:
			tick := e.Tick - e.Pumps*e.PumpTicks
			for range e.Pumps {
				tick += e.PumpTicks
				ops = append(ops, op{kind: EntryPumps, tick: tick, ticks: int(e.PumpTicks)})
			}
		}
	}
	return ops
}

func withoutFlushes(ops []op) []op {
	var out []op
	for _, o := range ops {
		if !o.flush {
			out = append(out, o)
		}
	}
	return out
}

// A recording reads back as the calls that made it, the pumps run-length
// encoded, ending in its end entry; the header round-trips; the summary
// names the final tick from the last chunk alone.
func TestReplayRoundTrip(t *testing.T) {
	h := testHeader(KindSkirmish)
	ops := singlePlayerScript(400)
	data, boundaries := record(t, h, ops, EndLeft)
	r, err := NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Header(); !reflect.DeepEqual(got, h) {
		t.Fatalf("header = %+v, want %+v", got, h)
	}
	entries, err := readAll(t, data)
	if err != io.EOF {
		t.Fatalf("stream ended with %v, want io.EOF", err)
	}
	last := entries[len(entries)-1]
	if last.Kind != EntryEnd || last.Reason != EndLeft {
		t.Fatalf("last entry = %+v, want the end entry", last)
	}
	if got, want := replayed(entries), withoutFlushes(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %d calls, recorded %d", len(got), len(want))
	}
	pumpEntries, pumps := 0, 0
	for _, e := range entries {
		if e.Kind == EntryPumps {
			pumpEntries++
			pumps += int(e.Pumps)
		}
	}
	if pumps != 400 || pumpEntries >= pumps/2 {
		t.Fatalf("%d pumps in %d entries: runs were not merged", pumps, pumpEntries)
	}
	if len(boundaries) < 4 {
		t.Fatalf("flushes wrote %d chunks", len(boundaries)-2)
	}
	sum, err := Summarize(data)
	if err != nil {
		t.Fatal(err)
	}
	if sum.FinalTick != last.Tick || sum.End != EndLeft || !reflect.DeepEqual(sum.Header, h) {
		t.Fatalf("summary = tick %d end %v, want tick %d end %v", sum.FinalTick, sum.End, last.Tick, EndLeft)
	}
	again, _ := record(t, h, ops, EndLeft)
	if !bytes.Equal(again, data) {
		t.Fatal("the same recording encoded to other bytes")
	}
	t.Logf("400 pumps, %d ticks: %d bytes in %d chunks", last.Tick, len(data), len(boundaries)-2)
}

// A file cut anywhere reads up to its last whole chunk and then reports
// ErrTruncated; its summary names that chunk's last tick.
func TestReplayTruncatedPlaysToLastWholeChunk(t *testing.T) {
	data, boundaries := record(t, testHeader(KindSkirmish), singlePlayerScript(200), EndFinished)
	full, _ := readAll(t, data)
	whole := map[int][]Entry{}
	ticks := map[int]uint32{}
	for _, b := range boundaries {
		entries, err := readAll(t, data[:b])
		if b != len(data) && !errors.Is(err, ErrTruncated) {
			t.Fatalf("cut at chunk boundary %d: %v", b, err)
		}
		whole[b] = entries
		for _, e := range entries {
			if e.Kind == EntryPumps || e.Kind == EntryEnd {
				ticks[b] = e.Tick
			}
		}
	}
	for cut := boundaries[0]; cut <= len(data); cut++ {
		b := boundaries[0]
		for _, boundary := range boundaries {
			if boundary <= cut {
				b = boundary
			}
		}
		entries, err := readAll(t, data[:cut])
		if cut == len(data) {
			if err != io.EOF {
				t.Fatalf("whole file: %v", err)
			}
		} else if !errors.Is(err, ErrTruncated) {
			t.Fatalf("cut at %d: %v, want ErrTruncated", cut, err)
		}
		if len(entries) != len(whole[b]) || len(entries) != 0 && (!reflect.DeepEqual(entries, whole[b]) || !reflect.DeepEqual(entries, full[:len(entries)])) {
			t.Fatalf("cut at %d read %d entries, want the %d of whole chunks", cut, len(entries), len(whole[b]))
		}
		sum, err := Summarize(data[:cut])
		if err != nil {
			t.Fatalf("summary of cut at %d: %v", cut, err)
		}
		if sum.FinalTick != ticks[b] || (sum.End != 0) != (cut == len(data)) {
			t.Fatalf("summary of cut at %d = tick %d end %v, want tick %d", cut, sum.FinalTick, sum.End, ticks[b])
		}
	}
	for cut := 0; cut < boundaries[0]; cut++ {
		if _, err := NewReader(data[:cut]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("a file cut inside its header at %d: %v", cut, err)
		}
	}
}

// An online recording holds one-tick pumps, commands from every seat and
// the checksums acknowledged after their ticks.
func TestReplayOnlineStream(t *testing.T) {
	h := testHeader(KindOnlineSkirmish)
	var ops []op
	position := uint64(0)
	for tick := uint32(1); tick <= 300; tick++ {
		if tick%7 == 0 {
			for seat := uint8(0); seat < 2; seat++ {
				position++
				ops = append(ops, op{kind: EntryCommand, tick: tick, seat: seat, position: position, payload: []byte{seat, byte(tick)}})
			}
		}
		ops = append(ops, op{kind: EntryPumps, tick: tick, ticks: 1})
		if tick%30 == 0 {
			ops = append(ops, op{kind: EntryChecksum, tick: tick, sum: [32]byte{byte(tick)}})
		}
	}
	data, _ := record(t, h, ops, EndFinished)
	entries, err := readAll(t, data)
	if err != io.EOF {
		t.Fatal(err)
	}
	if got := replayed(entries); !reflect.DeepEqual(got, ops) {
		t.Fatal("online stream did not read back as recorded")
	}
	t.Logf("online, 300 ticks, 84 commands: %d bytes", len(data))
}

// errWriter fails every write after its first n.
type errWriter struct{ n int }

func (w *errWriter) Write(p []byte) (int, error) {
	if w.n == 0 {
		return 0, errors.New("disk full")
	}
	w.n--
	return len(p), nil
}

// The Writer refuses a header it could not read back, stops at an entry the
// stream cannot hold — the file stays a valid replay of what came before —
// and keeps an output error.
func TestWriterRefusals(t *testing.T) {
	for name, edit := range map[string]func(*Header){
		"kind":       func(h *Header) { h.Kind = 9 },
		"config":     func(h *Header) { h.Config = nil },
		"seat":       func(h *Header) { h.LocalSeat = 10 },
		"seats":      func(h *Header) { h.Seats = make([]Seat, MaxSeats+1) },
		"colour":     func(h *Header) { h.Seats[0].Color = 10 },
		"role":       func(h *Header) { h.Seats[1].Role = 0 },
		"nickname":   func(h *Header) { h.Seats[0].Name = strings.Repeat("n", maxNicknameBytes+1) },
		"utf-8":      func(h *Header) { h.MapName = "\xff" },
		"rule name":  func(h *Header) { h.Identity.Rules.Name = "" },
		"config cap": func(h *Header) { h.Config = make([]byte, MaxConfigBytes+1) },
	} {
		h := testHeader(KindSkirmish)
		edit(&h)
		if _, err := NewWriter(io.Discard, h); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: header accepted: %v", name, err)
		}
	}

	var out bytes.Buffer
	w, err := NewWriter(&out, testHeader(KindSkirmish))
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(w.Command(1, 0, 1, []byte{1}))
	must(w.Pump(2, 2))
	must(w.Pump(4, 2))
	if err := w.Pump(9, 2); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a pump that skips ticks: %v", err)
	}
	if w.Stopped() == nil || w.Command(5, 0, 2, []byte{1}) == nil {
		t.Fatal("the recording went on after an entry the stream cannot hold")
	}
	must(w.Close(EndFinished))
	if err := w.Flush(); err != errWriterClosed {
		t.Fatalf("flush after close: %v", err)
	}
	entries, err := readAll(t, out.Bytes())
	if err != io.EOF {
		t.Fatal(err)
	}
	if end := entries[len(entries)-1]; end.Reason != EndStopped || end.Tick != 4 {
		t.Fatalf("stopped recording ends %+v, want tick 4 recording stopped", end)
	}

	for name, call := range map[string]func(w *Writer) error{
		"other seat":       func(w *Writer) error { return w.Command(1, 1, 1, []byte{1}) },
		"empty payload":    func(w *Writer) error { return w.Command(1, 0, 1, nil) },
		"position 0":       func(w *Writer) error { return w.Command(1, 0, 0, []byte{1}) },
		"tick 0":           func(w *Writer) error { return w.Command(0, 0, 1, []byte{1}) },
		"zero-tick pump":   func(w *Writer) error { return w.Pump(0, 0) },
		"checksum tick 0":  func(w *Writer) error { return w.Checksum(0, [32]byte{}) },
		"oversize payload": func(w *Writer) error { return w.Command(1, 0, 1, make([]byte, netproto.MaxCommandBytes+1)) },
	} {
		w, err := NewWriter(io.Discard, testHeader(KindSkirmish))
		if err != nil {
			t.Fatal(err)
		}
		if err := call(w); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	w, err = NewWriter(io.Discard, testHeader(KindOnlineSkirmish))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Pump(2, 2); !errors.Is(err, ErrCorrupt) {
		t.Errorf("online two-tick pump: %v", err)
	}

	failing := &errWriter{n: 1}
	w, err = NewWriter(failing, testHeader(KindSkirmish))
	if err != nil {
		t.Fatal(err)
	}
	must(w.Pump(1, 1))
	if err := w.Flush(); err == nil {
		t.Fatal("flush reported no output error")
	}
	if err := w.Close(EndFinished); err == nil || err != w.Flush() {
		t.Fatalf("output error not sticky: %v", err)
	}
}

// Large commands seal chunks on their own, each within the bound.
func TestWriterSealsChunksBySize(t *testing.T) {
	var out bytes.Buffer
	w, err := NewWriter(&out, testHeader(KindOnlineSkirmish))
	if err != nil {
		t.Fatal(err)
	}
	header := out.Len()
	payload := bytes.Repeat([]byte("nanolathe"), 3000)
	for tick := uint32(1); tick <= 40; tick++ {
		if err := w.Command(tick, uint8(tick%2), uint64(tick), payload); err != nil {
			t.Fatal(err)
		}
		if err := w.Pump(tick, 1); err != nil {
			t.Fatal(err)
		}
	}
	if out.Len() == header {
		t.Fatal("no chunk was sealed before the flush")
	}
	if err := w.Close(EndFinished); err != nil {
		t.Fatal(err)
	}
	entries, err := readAll(t, out.Bytes())
	if err != io.EOF || len(entries) != 81 {
		t.Fatalf("%d entries, %v", len(entries), err)
	}
}

// craft builds a file from a header and raw chunk bodies, compressing and
// framing each, without the Writer's checks.
func craft(t *testing.T, h Header, chunks ...[]byte) []byte {
	t.Helper()
	header, err := encodeHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	var w netproto.Writer
	w.Raw([]byte(Magic))
	w.U16(FormatVersion)
	w.U32(uint32(len(header)))
	w.Raw(header)
	for _, raw := range chunks {
		w.Raw(frame(t, raw))
	}
	return w.Bytes()
}

func frame(t *testing.T, raw []byte) []byte {
	t.Helper()
	var packed bytes.Buffer
	z, _ := flate.NewWriter(&packed, flate.DefaultCompression)
	if _, err := z.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	var w netproto.Writer
	w.U32(uint32(packed.Len()))
	w.U32(uint32(len(raw)))
	w.Raw(packed.Bytes())
	return w.Bytes()
}

// body is a raw chunk: an opening state and entries.
func body(start chunkStart, entries func(*netproto.Writer)) []byte {
	var w netproto.Writer
	w.U32(start.cursor)
	w.U64(start.position)
	w.U32(start.checksum)
	w.U32(start.command)
	entries(&w)
	return w.Bytes()
}

// The Reader refuses each malformed part by name before it allocates from
// it, and reports an unknown version by number.
func TestReaderRefusals(t *testing.T) {
	sp := testHeader(KindSkirmish)
	valid := craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
		w.U8(entryCommand)
		w.U32(1)
		w.U8(0)
		w.U64(1)
		w.U32(1)
		w.U8(7)
		w.U8(entryPumps)
		w.U32(1)
		w.U32(1)
		w.U8(entryEnd)
		w.U32(1)
		w.U8(uint8(EndFinished))
	}))
	if entries, err := readAll(t, valid); err != io.EOF || len(entries) != 3 {
		t.Fatalf("authored file: %d entries, %v", len(entries), err)
	}

	future := append([]byte(nil), valid...)
	future[len(Magic)] = 2
	if _, err := NewReader(future); !errors.Is(err, ErrUnsupportedVersion) || !strings.Contains(err.Error(), "replay format version 2") || !strings.Contains(err.Error(), "replay format version 1") {
		t.Fatalf("future version: %v", err)
	}
	if _, err := NewReader([]byte("NOTAREPLAY")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("other file: %v", err)
	}
	var big netproto.Writer
	big.Raw([]byte(Magic))
	big.U16(FormatVersion)
	big.U32(MaxHeaderBytes + 1)
	if _, err := NewReader(big.Bytes()); !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "header of 1..") {
		t.Fatalf("oversize header: %v", err)
	}

	end := func(w *netproto.Writer, tick uint32) {
		w.U8(entryEnd)
		w.U32(tick)
		w.U8(uint8(EndFinished))
	}
	sizes := func(compressed, raw uint64) []byte {
		var w netproto.Writer
		w.U64(compressed)
		w.U64(raw)
		w.U8(0)
		return w.Bytes()
	}
	headerOnly := craft(t, sp)
	for name, data := range map[string][]byte{
		"raw size over bound":        append(append([]byte(nil), headerOnly...), sizes(10, MaxChunkRawBytes+1)...),
		"compressed size over bound": append(append([]byte(nil), headerOnly...), sizes(MaxChunkCompressedBytes+1, 10)...),
		"zero-size chunk":            append(append([]byte(nil), headerOnly...), 0, 0),
		"overlong varint":            append(append([]byte(nil), headerOnly...), 0x81, 0x00, 1, 0),
		"false raw size": func() []byte {
			f := frame(t, body(chunkStart{}, func(w *netproto.Writer) { end(w, 0) }))
			f[1]++
			return append(append([]byte(nil), headerOnly...), f...)
		}(),
		"chunk opening elsewhere": craft(t, sp, body(chunkStart{cursor: 5}, func(w *netproto.Writer) { end(w, 5) })),
		"empty chunk":             craft(t, sp, body(chunkStart{}, func(*netproto.Writer) {})),
		"unknown entry":           craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) { w.U8(9) })),
		"repeated position": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
			w.U8(entryCommand)
			w.U32(1)
			w.U8(0)
			w.U64(0)
			w.U32(1)
			w.U8(1)
		})),
		"oversize payload": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
			w.U8(entryCommand)
			w.U32(1)
			w.U8(0)
			w.U64(1)
			w.U32(netproto.MaxCommandBytes + 1)
		})),
		"other seat": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
			w.U8(entryCommand)
			w.U32(1)
			w.U8(1)
			w.U64(1)
			w.U32(1)
			w.U8(1)
		})),
		"online two-tick pump": craft(t, testHeader(KindOnlineSurvival), body(chunkStart{}, func(w *netproto.Writer) {
			w.U8(entryPumps)
			w.U32(1)
			w.U32(2)
		})),
		"pump short of a command": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
			w.U8(entryCommand)
			w.U32(3)
			w.U8(0)
			w.U64(1)
			w.U32(1)
			w.U8(1)
			w.U8(entryPumps)
			w.U32(4)
			w.U32(1)
		})),
		"repeated checksum tick": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
			w.U8(entryPumps)
			w.U32(1)
			w.U32(9)
			w.U8(entryChecksum)
			w.U32(0)
			w.Digest([32]byte{})
			w.U8(entryChecksum)
			w.U32(0)
			w.Digest([32]byte{})
		})),
		"end before the cursor": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
			w.U8(entryPumps)
			w.U32(1)
			w.U32(3)
			end(w, 2)
		})),
		"entry after the end": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) {
			end(w, 0)
			w.U8(entryPumps)
			w.U32(1)
			w.U32(1)
		})),
		"chunk after the end": craft(t, sp, body(chunkStart{}, func(w *netproto.Writer) { end(w, 0) }), body(chunkStart{}, func(w *netproto.Writer) { end(w, 0) })),
		"trailing compressed bytes": func() []byte {
			f := frame(t, body(chunkStart{}, func(w *netproto.Writer) { end(w, 0) }))
			f[0]++
			return append(append(append([]byte(nil), headerOnly...), f...), 0)
		}(),
	} {
		_, err := readAll(t, data)
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
