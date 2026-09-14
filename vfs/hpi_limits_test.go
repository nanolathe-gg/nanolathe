package vfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// These authored graphs exercise Nanolathe's host safety policy [I11], not
// retail acceptance of shared directories [02 R-MALF-01 §3]. Each level
// references one next node repeatedly; the last node is empty. Distinct names
// expose all paths, while identical names hide all but the last subtree.
func directoryGraph(levels, fanout int, name string, duplicate bool) []byte {
	stride := hpiDirectoryNodeSize + fanout*hpiEntrySize
	names := hpiHeaderSize + levels*stride + hpiDirectoryNodeSize
	nameSize := len(name) + 2
	nameCount := fanout
	if duplicate {
		nameCount = 1
	}
	data := make([]byte, names+nameCount*nameSize)
	put := func(at, value int) { binary.LittleEndian.PutUint32(data[at:], uint32(value)) }
	copy(data, "HAPI")
	put(4, 0x00010000)
	put(8, len(data))
	put(16, hpiHeaderSize)
	for i := 0; i < nameCount; i++ {
		copy(data[names+i*nameSize:], name)
		data[names+i*nameSize+len(name)] = byte('a' + i)
	}
	for level := 0; level < levels; level++ {
		node := hpiHeaderSize + level*stride
		put(node, fanout)
		put(node+4, node+hpiDirectoryNodeSize)
		for i := 0; i < fanout; i++ {
			entry := node + hpiDirectoryNodeSize + i*hpiEntrySize
			nameIndex := i
			if duplicate {
				nameIndex = 0
			}
			put(entry, names+nameIndex*nameSize)
			put(entry+4, node+stride)
			data[entry+8] = 1
		}
	}
	put(hpiHeaderSize+levels*stride+4, names)
	return append(data, []byte("Copyright 0000 Cavedog Entertainment")...)
}

func requireDirectoryLimit(t *testing.T, data []byte, options ArchiveOptions, reason string) {
	t.Helper()
	fs := New()
	defer fs.Close()
	_, err := fs.MountArchiveReader("limited.hpi", bytes.NewReader(data), int64(len(data)), 0, options)
	if !errors.Is(err, ErrMalformedArchive) || !strings.Contains(err.Error(), reason) {
		t.Fatalf("mount = %v; want ErrMalformedArchive and %q", err, reason)
	}
	if fs.MountCount() != 0 || len(fs.Entries()) != 0 {
		t.Fatal("rejected archive published partial mount state")
	}
}

func TestArchiveDirectoryExpansionLimits(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		name := "visible branches"
		if duplicate {
			name = "hidden duplicate branches"
		}
		t.Run(name, func(t *testing.T) {
			// Twenty-eight levels encode hundreds of millions of visits in less
			// than one KiB. The entry budget stops both visible and hidden work.
			requireDirectoryLimit(t, directoryGraph(28, 2, "", duplicate), ArchiveOptions{}, "entry limit")
		})
	}
	// The empty terminal node still consumes depth, even though it has no
	// entries to count. Reject before growing the stack for that directory.
	requireDirectoryLimit(t, directoryGraph(64, 1, "", false), ArchiveOptions{}, "depth limit")
	// The first pass sees repeated long-name pointers before path creation;
	// an entry count alone cannot constrain this repeated scanning work.
	requireDirectoryLimit(t, directoryGraph(1, 32, strings.Repeat("x", 1<<20), true), ArchiveOptions{}, "string work limit")
}

func TestArchiveDirectoryExactBudgetsAndSharedReferences(t *testing.T) {
	data := directoryGraph(2, 2, "", false)
	// Six entries, root plus two directory levels, twelve two-byte name
	// scans, and joined paths of 1, 1, 3, 3, 3, 3 bytes counted twice.
	exact := ArchiveOptions{MaxDirectoryEntries: 6, MaxDirectoryDepth: 3, MaxDirectoryStringBytes: 52}
	archive, err := NewArchive("shared.hpi", bytes.NewReader(data), int64(len(data)), exact)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	want := []string{"a", "a/a", "a/b", "b", "b/a", "b/b"}
	for i, entry := range archive.auditEntries() {
		if i >= len(want) || entry.Path != want[i] || entry.OriginalPath != want[i] || entry.Source.SourcePath != "shared.hpi" {
			t.Fatalf("entry %d = %+v; want authored order/path/provenance", i, entry)
		}
	}
	if len(archive.auditEntries()) != len(want) || len(archive.retailEntries()) != len(want) {
		t.Fatal("benign shared references lost entries")
	}
	for _, test := range []struct {
		name    string
		options ArchiveOptions
	}{
		{"entry limit", ArchiveOptions{MaxDirectoryEntries: 5}},
		{"depth limit", ArchiveOptions{MaxDirectoryDepth: 2}},
		{"string work limit", ArchiveOptions{MaxDirectoryStringBytes: 51}},
	} {
		t.Run(test.name, func(t *testing.T) { requireDirectoryLimit(t, data, test.options, test.name) })
	}
}

func TestArchiveDirectoryStringBudgetBeforeNormalization(t *testing.T) {
	// Normalization removes this repeated prefix. Work must be charged from
	// the joined inputs, not the shorter resulting logical path.
	data := authoredArchive(t, []ArchiveFile{{Path: "entry", Data: []byte("x")}}, false)
	root := binary.LittleEndian.Uint32(data[16:20])
	list := binary.LittleEndian.Uint32(data[root+4 : root+8])
	name := binary.LittleEndian.Uint32(data[list : list+4])
	copy(data[name:name+5], "././a")
	for _, budget := range []int64{5, 6, 11, 12, 21} {
		// A scan needs six bytes including NUL, both scans need twelve, and
		// path materialization needs another ten even though the result is "a".
		requireDirectoryLimit(t, data, ArchiveOptions{MaxDirectoryStringBytes: budget}, "string work limit")
	}
	a, err := NewArchive("normalized.hpi", bytes.NewReader(data), int64(len(data)), ArchiveOptions{MaxDirectoryStringBytes: 22})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, ok := a.lookup("a"); !ok {
		t.Fatal("accepted path normalization changed")
	}
}

func TestArchiveDirectoryNonpositiveLimitsUseDefaults(t *testing.T) {
	defaults := ArchiveOptions{}.withDefaults()
	negative := ArchiveOptions{MaxDirectoryEntries: -1, MaxDirectoryDepth: -1, MaxDirectoryStringBytes: -1}.withDefaults()
	if defaults.MaxDirectoryEntries != negative.MaxDirectoryEntries || defaults.MaxDirectoryDepth != negative.MaxDirectoryDepth || defaults.MaxDirectoryStringBytes != negative.MaxDirectoryStringBytes {
		t.Fatal("nonpositive limits must select the same safe defaults")
	}
	data := directoryGraph(2, 2, "", false)
	if _, err := NewArchive("defaults.hpi", bytes.NewReader(data), int64(len(data)), negative); err != nil {
		t.Fatal(err)
	}
}
