package audio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func TestRegistryUsesAuthoredPathAndRetainsFailedIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "camps"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "camps", "voice.wav"), buildRIFF(1, 11025, 8, []byte{128, 129}), 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	r := NewRegistry(fs)
	id := r.RegisterPath("VOICE", "camps/voice.wav")
	if id == NullAlias || r.Count() != 1 {
		t.Fatalf("registration id=%d count=%d", id, r.Count())
	}
	s, err := r.Load(id)
	if err != nil || s == nil {
		t.Fatalf("authored path load sample=%v err=%v", s, err)
	}
	if s.Provenance.LogicalPath != "camps/voice.wav" {
		t.Fatalf("authored path provenance=%+v", s.Provenance)
	}
	missing := r.RegisterPath("MISSING", "camps/no-file.wav")
	if missing == NullAlias || r.Count() != 2 {
		t.Fatalf("failed probe must consume identity id=%d count=%d", missing, r.Count())
	}
	if _, err := r.Load(missing); err == nil {
		t.Fatal("missing registered path should remain silent/error on load")
	}
}
