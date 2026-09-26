package profiles_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Alpha 5's Base soundtrack stores its bonus intro alongside tracks 2..17.
// The profile must use tamus rather than the base install's music tree and
// retain the ordinary soundtrack's intro exclusion after directory mapping.
func TestZeroSoundtrackLayout(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"tamus", "music"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 17; i++ {
			if err := os.WriteFile(filepath.Join(root, dir, fmt.Sprintf("%d.mp3", i)), []byte(dir), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Lookup("zero")
	if err != nil {
		t.Fatal(err)
	}
	view := profile.Layout().Apply(fs)
	tracks := audio.MusicTracks(view)
	if len(tracks) != 16 || tracks[0] != "music/2.mp3" || tracks[15] != "music/17.mp3" {
		t.Fatalf("logical soundtrack = %v", tracks)
	}
	for _, track := range tracks {
		data, err := view.ReadFileLimit(track, 64)
		if err != nil || string(data) != "tamus" {
			t.Fatalf("%s: %q, %v", track, data, err)
		}
	}
}
