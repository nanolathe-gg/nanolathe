package vfs

import (
	"bytes"
	"testing"
)

func TestWriteArchiveRoundTrip(t *testing.T) {
	big := make([]byte, 65536*2+123) // three chunks, the last a remainder
	for i := range big {
		big[i] = byte(i * 7)
	}
	files := []ArchiveFile{
		{Path: "objects3d/corkrog.3do", Data: []byte("model bytes")},
		{Path: `textures\rm_corkrog.gaf`, Data: big},
		{Path: "readme.txt", Data: []byte("hello")},
		{Path: "textures/empty.gaf", Data: nil},
	}
	for _, compress := range []bool{false, true} {
		var buf bytes.Buffer
		if err := WriteArchive(&buf, files, ArchiveWriteOptions{Compress: compress, Year: 2026}); err != nil {
			t.Fatal(err)
		}
		if !bytes.HasSuffix(buf.Bytes(), []byte("Copyright 2026 Cavedog Entertainment")) {
			t.Fatal("missing the 36-byte trailer the retail mount validator requires")
		}
		fs := New()
		data := buf.Bytes()
		if _, err := fs.MountArchiveReader("test.hpi", bytes.NewReader(data), int64(len(data)), 10, ArchiveOptions{VerifyChecksums: true}); err != nil {
			t.Fatalf("compress=%v: %v", compress, err)
		}
		for _, file := range files {
			got, err := fs.ReadFile(file.Path)
			if err != nil {
				t.Fatalf("compress=%v: %s: %v", compress, file.Path, err)
			}
			if !bytes.Equal(got, file.Data) {
				t.Fatalf("compress=%v: %s: %d bytes want %d", compress, file.Path, len(got), len(file.Data))
			}
		}
		entries, err := fs.ReadDir("textures")
		if err != nil || len(entries) != 2 {
			t.Fatalf("compress=%v: textures dir %v %v", compress, entries, err)
		}
		fs.Close()
	}
}
