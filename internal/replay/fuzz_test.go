package replay

import (
	"bytes"
	"testing"
)

// FuzzReader feeds arbitrary bytes to the Reader and the summary: neither
// may panic or allocate beyond the bounds it checks, whatever the input.
func FuzzReader(f *testing.F) {
	var out bytes.Buffer
	w, err := NewWriter(&out, testHeader(KindSkirmish))
	if err != nil {
		f.Fatal(err)
	}
	for _, o := range singlePlayerScript(60) {
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
			f.Fatal(err)
		}
	}
	if err := w.Close(EndFinished); err != nil {
		f.Fatal(err)
	}
	valid := out.Bytes()
	f.Add(valid)
	f.Add(valid[:len(valid)/2])
	flipped := append([]byte(nil), valid...)
	flipped[len(flipped)-5] ^= 0x40
	f.Add(flipped)
	f.Add([]byte(Magic))
	f.Fuzz(func(t *testing.T, data []byte) {
		if r, err := NewReader(data); err == nil {
			_ = r.Header()
			for range 1 << 16 {
				if _, err := r.Next(); err != nil {
					break
				}
			}
		}
		_, _ = Summarize(data)
	})
}
