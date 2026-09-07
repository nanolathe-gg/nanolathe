package vfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveRejectsDirectoryPointersOutsideBlob(t *testing.T) {
	data := authoredArchive(t, []ArchiveFile{{Path: "entry", Data: []byte("data")}}, false)
	root := int(binary.LittleEndian.Uint32(data[16:20]))
	entries := int(binary.LittleEndian.Uint32(data[root+4 : root+8]))
	directoryEnd := binary.LittleEndian.Uint32(data[8:12])

	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{
			name: "name pointer at end",
			mutate: func(b []byte) {
				binary.LittleEndian.PutUint32(b[entries:entries+4], directoryEnd)
			},
		},
		{
			name: "record pointer beyond end",
			mutate: func(b []byte) {
				binary.LittleEndian.PutUint32(b[entries+4:entries+8], directoryEnd+1)
			},
		},
		{
			name: "child pointer at end",
			mutate: func(b []byte) {
				b[entries+8] = 1
				binary.LittleEndian.PutUint32(b[entries+4:entries+8], directoryEnd)
			},
		},
		{
			name: "entry list crosses end",
			mutate: func(b []byte) {
				binary.LittleEndian.PutUint32(b[root+4:root+8], directoryEnd-1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := append([]byte(nil), data...)
			test.mutate(mutated)
			if _, err := NewArchive("malformed.hpi", bytes.NewReader(mutated), int64(len(mutated)), ArchiveOptions{}); !errors.Is(err, ErrMalformedArchive) {
				t.Fatalf("NewArchive error = %v, want ErrMalformedArchive", err)
			}
		})
	}
}

func TestArchiveChunkSpansAndLimit(t *testing.T) {
	data := make([]byte, 2*hpiChunkSize+19)
	for i := range data {
		data[i] = byte(i)
	}
	archive := authoredArchive(t, []ArchiveFile{{Path: "large.bin", Data: data}}, true)

	for _, outputSize := range []uint32{hpiChunkSize - 1, hpiChunkSize + 1} {
		t.Run("reject non-final output span", func(t *testing.T) {
			mutated := append([]byte(nil), archive...)
			chunk := firstChunkOffset(mutated)
			binary.LittleEndian.PutUint32(mutated[chunk+11:chunk+15], outputSize)
			fs := New()
			if _, err := fs.MountArchiveReader("bad.hpi", bytes.NewReader(mutated), int64(len(mutated)), 1, ArchiveOptions{}); err != nil {
				t.Fatalf("mount: %v", err)
			}
			defer fs.Close()
			if _, err := fs.ReadFile("large.bin"); !errors.Is(err, ErrMalformedArchive) {
				t.Fatalf("ReadFile error = %v, want ErrMalformedArchive", err)
			}
		})
	}

	fs := New()
	if _, err := fs.MountArchiveReader("valid.hpi", bytes.NewReader(archive), int64(len(archive)), 1, ArchiveOptions{}); err != nil {
		t.Fatalf("mount valid archive: %v", err)
	}
	defer fs.Close()
	whole, err := fs.ReadFile("large.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(whole, data) {
		t.Fatal("whole-file decode differs from authored bytes")
	}
	for _, span := range []struct{ offset, length int }{{0, 32}, {hpiChunkSize - 5, 17}, {2 * hpiChunkSize, 19}} {
		got, err := fs.ReadFileRange("large.bin", int64(span.offset), span.length)
		if err != nil {
			t.Fatalf("range %d:+%d: %v", span.offset, span.length, err)
		}
		if want := whole[span.offset : span.offset+span.length]; !bytes.Equal(got, want) {
			t.Fatalf("range %d:+%d differs from whole read", span.offset, span.length)
		}
	}

	tracked := &countingReaderAt{reader: bytes.NewReader(archive)}
	limited := New()
	if _, err := limited.MountArchiveReader("limited.hpi", tracked, int64(len(archive)), 1, ArchiveOptions{}); err != nil {
		t.Fatalf("mount limited archive: %v", err)
	}
	tracked.reset()
	if _, err := limited.ReadFileLimit("large.bin", 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ReadFileLimit error = %v, want ErrTooLarge", err)
	}
	if tracked.calls != 0 {
		t.Fatalf("over-limit read opened archive payload %d times", tracked.calls)
	}
}

func TestArchiveLimitStreamsFileBackedChunkPayload(t *testing.T) {
	base := authoredArchive(t, []ArchiveFile{{Path: "small.bin", Data: []byte("x")}}, true)
	chunk := firstChunkOffset(base)
	storedSize := ^uint32(0)
	binary.LittleEndian.PutUint32(base[chunk-4:chunk], storedSize)
	binary.LittleEndian.PutUint32(base[chunk+7:chunk+11], storedSize-hpiChunkHeaderSize)

	logicalSize := int64(chunk) + int64(storedSize) + 36
	reader := &sparseReaderAt{
		prefix:  base,
		size:    logicalSize,
		tail:    append([]byte(nil), base[len(base)-36:]...),
		tailAt:  logicalSize - 36,
		blockAt: int64(chunk + hpiChunkHeaderSize),
	}
	fs := New()
	if _, err := fs.MountArchiveReader("sparse.hpi", reader, logicalSize, 1, ArchiveOptions{}); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fs.Close()
	reader.reset()
	if _, err := fs.ReadFileLimit("small.bin", 1); !errors.Is(err, ErrMalformedArchive) {
		t.Fatalf("ReadFileLimit error = %v, want ErrMalformedArchive", err)
	}
	if reader.maxRequest > 32<<10 {
		t.Fatalf("tiny limited read requested %d-byte encoded payload buffer", reader.maxRequest)
	}
}

func TestPinnedMountUsesMetadataAndOrdinaryReadGuards(t *testing.T) {
	archive := authoredArchive(t, []ArchiveFile{{Path: "same.txt", Data: []byte("lower")}}, true)
	tracked := &countingReaderAt{reader: bytes.NewReader(archive)}
	fs := New()
	if _, err := fs.MountArchiveReader("lower.hpi", tracked, int64(len(archive)), 1, ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "same.txt"), []byte("upper"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "only-upper.txt"), bytes.Repeat([]byte("x"), 16), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.MountDirectory(dir, 2); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	// The higher-priority loose directory is mount 0; the archive is the
	// pinned lower provider. Metadata must not open or decode that archive.
	pinned := fs.Pinned(1)
	tracked.reset()
	info, err := pinned.Stat("same.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Source.ProviderID(); got != "lower.hpi" {
		t.Fatalf("pinned provider = %q, want lower.hpi", got)
	}
	if _, err := pinned.CacheStamp("same.txt"); err != nil {
		t.Fatal(err)
	}
	if tracked.calls != 0 {
		t.Fatalf("pinned metadata read archive payload %d times", tracked.calls)
	}
	if _, err := pinned.ReadFileLimit("same.txt", 2); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("pinned limit error = %v, want ErrTooLarge", err)
	}
	if tracked.calls != 0 {
		t.Fatalf("pinned over-limit read archive payload %d times", tracked.calls)
	}
	tracked.fail = true
	if _, err := pinned.Open("same.txt"); err == nil {
		t.Fatal("pinned open succeeded with an unreadable archive")
	}
	info, err = pinned.Stat("same.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Source.ProviderID(); got != "lower.hpi" {
		t.Fatalf("unreadable pinned provider = %q, want lower.hpi", got)
	}
	stamp, err := pinned.CacheStamp("same.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stamp, "hpi|lower.hpi|") {
		t.Fatalf("unreadable pinned stamp = %q, want lower archive identity", stamp)
	}
	if _, err := pinned.Stat("only-upper.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing pinned Stat error = %v, want ErrNotFound", err)
	}
	if _, err := pinned.ReadFileLimit("only-upper.txt", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing pinned limit error = %v, want ErrNotFound", err)
	}

	dirFS := New()
	if err := dirFS.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	defer dirFS.Close()
	if _, err := dirFS.OpenMount(0, ""); !errors.Is(err, ErrIsDir) {
		t.Fatalf("OpenMount directory error = %v, want ErrIsDir", err)
	}
}

func authoredArchive(t *testing.T, files []ArchiveFile, compress bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := WriteArchive(&buffer, files, ArchiveWriteOptions{Compress: compress}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func firstChunkOffset(data []byte) int {
	root := int(binary.LittleEndian.Uint32(data[16:20]))
	entries := int(binary.LittleEndian.Uint32(data[root+4 : root+8]))
	record := int(binary.LittleEndian.Uint32(data[entries+4 : entries+8]))
	return int(binary.LittleEndian.Uint32(data[record:record+4])) + 4
}

type countingReaderAt struct {
	reader *bytes.Reader
	calls  int
	fail   bool
}

func (r *countingReaderAt) ReadAt(data []byte, offset int64) (int, error) {
	r.calls++
	if r.fail {
		return 0, io.ErrUnexpectedEOF
	}
	return r.reader.ReadAt(data, offset)
}

func (r *countingReaderAt) reset() { r.calls = 0 }

var _ io.ReaderAt = (*countingReaderAt)(nil)

// sparseReaderAt models a file-backed chunk payload whose declared extent is
// large while only its metadata is readable. It records request sizes without
// allocating the declared payload.
type sparseReaderAt struct {
	prefix     []byte
	size       int64
	tail       []byte
	tailAt     int64
	blockAt    int64
	maxRequest int
}

func (r *sparseReaderAt) ReadAt(data []byte, offset int64) (int, error) {
	if len(data) > r.maxRequest {
		r.maxRequest = len(data)
	}
	if offset == r.tailAt && len(data) == len(r.tail) {
		copy(data, r.tail)
		return len(data), nil
	}
	if offset < 0 || offset+int64(len(data)) > r.blockAt || offset+int64(len(data)) > r.size {
		return 0, io.ErrUnexpectedEOF
	}
	copy(data, r.prefix[offset:int(offset)+len(data)])
	return len(data), nil
}

func (r *sparseReaderAt) reset() { r.maxRequest = 0 }

var _ io.ReaderAt = (*sparseReaderAt)(nil)
