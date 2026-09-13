package vfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The container gate rejects one discovered candidate, preserving later
// providers and loose content [02 §2]. Explicit archive mounts still fail.
func TestDiscoveredArchiveRejectionPreservesLaterProviders(t *testing.T) {
	valid := authoredArchive(t, []ArchiveFile{{Path: "archive.txt", Data: []byte("archive")}}, false)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"short", valid[:10]},
		{"marker", append([]byte("ZIP!"), valid[4:]...)},
		{"version", append(append([]byte(nil), valid[:4]...), append([]byte{0, 0, 2, 0}, valid[8:]...)...)},
		{"footer", append(append([]byte(nil), valid[:len(valid)-1]...), '!')},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			bad := filepath.Join(dir, "a-rejected.ufo")
			writeMountFixture(t, bad, test.data)
			writeMountFixture(t, filepath.Join(dir, "z-valid.hpi"), valid)
			writeMountFixture(t, filepath.Join(dir, "loose.txt"), []byte("loose"))
			fs := New()
			defer fs.Close()
			if err := fs.MountGameDirectory(dir); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"archive.txt", "loose.txt"} {
				if _, err := fs.ReadFile(name); err != nil {
					t.Fatalf("surviving content %s: %v", name, err)
				}
			}
			notes := strings.Join(fs.Notes(), "\n")
			if !strings.Contains(notes, "a-rejected.ufo") || strings.Contains(notes, dir) {
				t.Fatalf("rejection must retain portable provider identity: %s", notes)
			}
			// A rejected discovery must not poison direct-call dedup, nor a
			// later retry after the download is repaired [02 §2].
			if _, err := fs.MountArchive(bad, 20); !errors.Is(err, ErrRejectedArchive) {
				t.Fatalf("explicit rejected archive: %v", err)
			}
			writeMountFixture(t, bad, valid)
			if archive, err := fs.MountArchive(bad, 20); err != nil || archive == nil {
				t.Fatalf("repaired archive retry: %v, %v", archive, err)
			}
		})
	}
}

func TestDiscoveredArchiveDirectoryFailureRemainsFatal(t *testing.T) {
	data := authoredArchive(t, []ArchiveFile{{Path: "file", Data: []byte("data")}}, false)
	binary.LittleEndian.PutUint32(data[16:20], ^uint32(0))
	dir := t.TempDir()
	writeMountFixture(t, filepath.Join(dir, "invalid-directory.hpi"), data)
	fs := New()
	defer fs.Close()
	err := fs.MountGameDirectory(dir)
	if !errors.Is(err, ErrMalformedArchive) || errors.Is(err, ErrRejectedArchive) {
		t.Fatalf("directory failure must not be container-gate rejection: %v", err)
	}
}

func TestDiscoveredArchivePayloadFailureRemainsReadError(t *testing.T) {
	data := authoredArchive(t, []ArchiveFile{{Path: "file", Data: bytes.Repeat([]byte("data"), 100)}}, true)
	chunk := firstChunkOffset(data)
	data[chunk] = '!'
	dir := t.TempDir()
	writeMountFixture(t, filepath.Join(dir, "bad-payload.hpi"), data)
	fs := New()
	defer fs.Close()
	if err := fs.MountGameDirectory(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile("file"); !errors.Is(err, ErrMalformedArchive) || errors.Is(err, ErrRejectedArchive) {
		t.Fatalf("payload failure must remain a read error: %v", err)
	}
}

func writeMountFixture(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
