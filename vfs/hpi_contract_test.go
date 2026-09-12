package vfs

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// These authored archives lock the low-byte key and flag contracts [02 §2].
func TestArchivePlainKeyAndFlagBits(t *testing.T) {
	for _, key := range []uint32{0, 0x1200, 0xff, 0x123456ff} {
		data := authoredArchive(t, []ArchiveFile{{Path: "dir/entry", Data: []byte("payload")}}, false)
		binary.LittleEndian.PutUint32(data[12:16], key)
		root := int(binary.LittleEndian.Uint32(data[16:20]))
		entries := int(binary.LittleEndian.Uint32(data[root+4 : root+8]))
		data[entries+8] = 0xff // Bit zero still makes this a directory.
		child := int(binary.LittleEndian.Uint32(data[entries+4 : entries+8]))
		files := int(binary.LittleEndian.Uint32(data[child+4 : child+8]))
		data[files+8] = 0xfe // Shadow and reserved bits do not make a directory.
		fs := New()
		if _, err := fs.MountArchiveReader("authored.hpi", bytes.NewReader(data), int64(len(data)), 1, ArchiveOptions{}); err != nil {
			t.Fatalf("key %#x: mount: %v", key, err)
		}
		got, err := fs.ReadFile("dir/entry")
		fs.Close()
		if err != nil || string(got) != "payload" {
			t.Fatalf("key %#x: read = %q, %v", key, got, err)
		}
	}
}

func TestLZ77RequiresTerminatorAndExactOutput(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    []byte
		expected uint64
		want     []byte
		valid    bool
	}{
		{"literal and terminator", []byte{2, 'x', 0, 0}, 1, []byte{'x'}, true},
		{"missing terminator", []byte{0, 'x'}, 1, nil, false},
		{"early terminator", []byte{1, 0, 0}, 1, nil, false},
		{"extra output", []byte{4, 'x', 'y', 0, 0}, 1, nil, false},
		{"empty terminated stream", []byte{1, 0, 0}, 0, []byte{}, true},
		{"empty missing stream", nil, 0, nil, false},
		{"zero initialized window", []byte{3, 0x10, 0, 0, 0}, 2, []byte{0, 0}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeLZ77(bytes.NewReader(test.input), test.expected)
			if (err == nil) != test.valid || test.valid && !bytes.Equal(got, test.want) {
				t.Fatalf("decode = %v, %v; want %v, valid %v", got, err, test.want, test.valid)
			}
		})
	}
}
