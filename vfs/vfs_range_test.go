package vfs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReadFileRangeProvidersAgree locks the two range providers to one
// contract, because the same logical read must not depend on whether the file
// happens to be loose or archived:
//
//   - a length that runs past the end is clamped to what remains, and the
//     allocation is bounded by the file rather than by the caller's word — a
//     caller's "read the rest" sentinel is a natural way to hit this;
//   - a negative length reads to the end;
//   - an offset exactly at the end is an empty read, not an error;
//   - an offset past the end is refused.
//
// The archive reader already did all four. The loose reader allocated the
// caller's length verbatim and answered a past-the-end offset with an empty
// slice and no error.
func TestReadFileRangeProvidersAgree(t *testing.T) {
	whole := make([]byte, 3000)
	for i := range whole {
		whole[i] = byte(i*7 + 1)
	}
	const name = "ranged.bin"

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), whole, 0o644); err != nil {
		t.Fatal(err)
	}
	loose := New()
	defer loose.Close()
	if err := loose.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}

	for _, compress := range []bool{false, true} {
		archived := New()
		defer archived.Close()
		bytesOfArchive := authoredArchive(t, []ArchiveFile{{Path: name, Data: whole}}, compress)
		if _, err := archived.MountArchiveReader("ranged.hpi", bytes.NewReader(bytesOfArchive), int64(len(bytesOfArchive)), 1, ArchiveOptions{}); err != nil {
			t.Fatalf("mount archive (compress=%v): %v", compress, err)
		}

		providers := []struct {
			label string
			fs    *FS
		}{{"loose", loose}, {"archived", archived}}
		for _, p := range providers {
			label := p.label
			if label == "archived" && compress {
				label = "archived-compressed"
			}

			// A length far past the end is clamped, and the allocation with
			// it: the returned slice's capacity is the read the provider
			// actually made, which is what an unclamped make() blows up.
			for _, offset := range []int{0, 1, len(whole) / 2, len(whole) - 1} {
				got, err := p.fs.ReadFileRange(name, int64(offset), 1<<28)
				if err != nil {
					t.Fatalf("%s: oversize length at %d: %v", label, offset, err)
				}
				if want := whole[offset:]; !bytes.Equal(got, want) {
					t.Fatalf("%s: oversize length at %d returned %d bytes, want %d", label, offset, len(got), len(want))
				}
				if cap(got) > len(whole) {
					t.Fatalf("%s: oversize length at %d allocated %d bytes for a %d-byte file", label, offset, cap(got), len(whole))
				}
			}

			// A negative length reads to the end.
			tail, err := p.fs.ReadFileRange(name, int64(len(whole)-16), -1)
			if err != nil || !bytes.Equal(tail, whole[len(whole)-16:]) {
				t.Fatalf("%s: read to end: %v", label, err)
			}

			// An offset exactly at the end is an empty read, not an error.
			empty, err := p.fs.ReadFileRange(name, int64(len(whole)), 64)
			if err != nil {
				t.Fatalf("%s: offset at the end: %v", label, err)
			}
			if len(empty) != 0 {
				t.Fatalf("%s: offset at the end returned %d bytes", label, len(empty))
			}

			// An offset past the end is refused by both providers.
			if _, err := p.fs.ReadFileRange(name, int64(len(whole))+1, 64); err == nil {
				t.Fatalf("%s: offset past the end was accepted", label)
			}
			if _, err := p.fs.ReadFileRange(name, int64(len(whole))+1, -1); err == nil {
				t.Fatalf("%s: offset past the end with a negative length was accepted", label)
			}
		}
	}

	// The loose provider names itself in its refusal, as every vfs diagnostic
	// does; the archive reader names a malformed archive.
	if _, err := loose.ReadFileRange(name, int64(len(whole))+1, 64); !errors.Is(err, ErrNotFound) {
		t.Fatalf("loose past-the-end error = %v, want ErrNotFound", err)
	}
}
