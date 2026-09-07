package vfs

import (
	"bytes"
	"testing"
)

func TestAuthoredPathBytesSurviveArchiveAndOverlay(t *testing.T) {
	// Distinct legacy bytes must not collapse into the Unicode replacement
	// character. High-byte case equivalence remains unknown [02 §2].
	names := []string{"TEXTURES/\xc0.GAF", "TEXTURES/\xc1.GAF"}
	var data bytes.Buffer
	if err := WriteArchive(&data, []ArchiveFile{{Path: names[0], Data: []byte("first")}, {Path: names[1], Data: []byte("second")}}, ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	fs := New()
	defer fs.Close()
	if _, err := fs.MountArchiveReader("bytes.hpi", bytes.NewReader(data.Bytes()), int64(data.Len()), 10, ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		logical, err := cleanPath(name)
		if err != nil || logical != "textures/"+string([]byte{byte(0xc0 + i)})+".gaf" {
			t.Fatalf("logical path = %q, %v", logical, err)
		}
		got, err := fs.ReadFile(logical)
		want := []string{"first", "second"}[i]
		if err != nil || string(got) != want {
			t.Fatalf("read %q = %q, %v; want %q", logical, got, err, want)
		}
	}
	for _, read := range []func(string) ([]EntryInfo, error){fs.ReadDir, fs.RetailReadDir} {
		entries, err := read("TEXTURES")
		if err != nil || len(entries) != 2 || entries[0].Path == entries[1].Path {
			t.Fatalf("byte-distinct directory entries = %v, %v", entries, err)
		}
	}
}

func TestArchiveWriterRejectsASCIICaseDuplicate(t *testing.T) {
	var data bytes.Buffer
	err := WriteArchive(&data, []ArchiveFile{{Path: "textures/A.GAF"}, {Path: "TEXTURES/a.gaf"}}, ArchiveWriteOptions{})
	if err == nil {
		t.Fatal("ASCII case variants stopped identifying the same archive entry [02 §2]")
	}
}
