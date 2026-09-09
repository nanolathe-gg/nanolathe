package audio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
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

// TestAuthoredSoundPathsCarryTheSoundsPrefix locks the alias registrar's probe
// candidates [03 §8.3 "Alias registration"]: the authored `sound` value is
// probed "with the `sounds/` prefix and the canonical candidate tries". Stock
// `allsound.tdf` authors bare stems, so dropping the prefix silences every
// alias-registered cue.
func TestAuthoredSoundPathsCarryTheSoundsPrefix(t *testing.T) {
	got := authoredSoundPaths("butmain1")
	if len(got) == 0 || got[0] != "sounds/butmain1" {
		t.Fatalf("authoredSoundPaths(butmain1) = %v, want the sounds/-prefixed form first", got)
	}
	if !containsPath(got, "sounds/butmain1.wav") {
		t.Fatalf("authoredSoundPaths(butmain1) = %v, want the prefixed .wav candidate", got)
	}
	if !containsPath(got, "butmain1") {
		t.Fatalf("authoredSoundPaths(butmain1) = %v, want the unprefixed candidate retained", got)
	}
	// An authored value that already carries the prefix is not prefixed twice.
	for _, p := range authoredSoundPaths("sounds/explode.wav") {
		if p == "sounds/sounds/explode.wav" {
			t.Fatalf("authoredSoundPaths doubled the prefix: %v", authoredSoundPaths("sounds/explode.wav"))
		}
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
