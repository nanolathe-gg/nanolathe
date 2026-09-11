package vfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// Every path component selects the last matching directory entry [02 §2].
// A replaced directory does not merge its children into its replacement.
func TestArchiveDuplicateDirectoryDoesNotMergeSubtrees(t *testing.T) {
	for _, replacement := range []ArchiveFile{
		{Path: "second/new.txt", Data: []byte("new")},
		{Path: "second", Data: []byte("file")},
	} {
		t.Run(replacement.Path, func(t *testing.T) {
			data := authoredArchive(t, []ArchiveFile{{Path: "first/old.txt", Data: []byte("old")}, replacement}, false)
			root := binary.LittleEndian.Uint32(data[16:20])
			entries := binary.LittleEndian.Uint32(data[root+4 : root+8])
			// The authored writer orders first before second. Point the second
			// entry's name at the first name to author a duplicate component.
			copy(data[entries+9:entries+13], data[entries:entries+4])
			fs := New()
			if _, err := fs.MountArchiveReader("duplicate.hpi", bytes.NewReader(data), int64(len(data)), 1, ArchiveOptions{}); err != nil {
				t.Fatal(err)
			}
			defer fs.Close()
			if got, err := fs.ReadFile("first/old.txt"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("shadowed child read = %q, %v; want not found", got, err)
			}
			if _, err := fs.Stat("first/old.txt"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("shadowed child stat = %v; want not found", err)
			}
			retained := false
			for _, entry := range fs.Entries() {
				retained = retained || entry.Path == "first/old.txt"
			}
			if !retained {
				t.Fatal("raw diagnostic index discarded the shadowed subtree")
			}
			if replacement.Path == "second/new.txt" {
				if got, err := fs.ReadFile("first/new.txt"); err != nil || string(got) != "new" {
					t.Fatalf("winning child = %q, %v", got, err)
				}
				for _, readDir := range []func(string) ([]EntryInfo, error){fs.ReadDir, fs.RetailReadDir} {
					got, err := readDir("first")
					if err != nil || len(got) != 1 || got[0].Path != "first/new.txt" {
						t.Fatalf("winning subtree = %+v, %v", got, err)
					}
				}
			} else {
				if got, err := fs.ReadFile("first"); err != nil || string(got) != "file" {
					t.Fatalf("winning file = %q, %v", got, err)
				}
				if got, err := fs.RetailReadDir("first"); err != nil || len(got) != 0 {
					t.Fatalf("file-shadowed subtree enumerated = %+v, %v", got, err)
				}
			}
		})
	}
}
