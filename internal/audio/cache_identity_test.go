package audio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Mode-0 aliases resolve their registered path; mode-1 voices resolve the
// voice filename, even when their names coincide [03 R-AUD-01 §1].
func TestVoiceAndRegisteredAliasKeepDistinctSamples(t *testing.T) {
	for _, voiceFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "alias first", true: "voice first"}[voiceFirst], func(t *testing.T) {
			s := cacheIdentityService(t)
			if voiceFirst {
				if sample := s.loadVoiceLine("voice"); sample == nil || sample.Provenance.LogicalPath != "sounds/voice.wav" {
					t.Fatalf("voice sample = %#v", sample)
				}
			}
			id := s.Registry.RegisterPath("voice", "sounds/alternate.wav")
			alias, err := s.Registry.Load(id)
			if err != nil || alias == nil || alias.Provenance.LogicalPath != "sounds/alternate.wav" {
				t.Fatalf("registered sample = %#v, err=%v", alias, err)
			}
			voice := s.loadVoiceLine("voice")
			if voice == nil || voice.Provenance.LogicalPath != "sounds/voice.wav" {
				t.Fatalf("voice reused registered alias: %#v", voice)
			}
			if got, err := s.Registry.Load(id); err != nil || got != alias {
				t.Fatalf("voice replaced registered sample: %#v, err=%v", got, err)
			}
		})
	}
}

// Narration carries an exact resource path, not an alias name. An alias
// with the same spelling must not redirect the stream [03 R-AUD-02 §1].
func TestStreamPathDoesNotReadRegisteredAliasCache(t *testing.T) {
	s := cacheIdentityService(t)
	s.Registry.RegisterPath("sounds/voice.wav", "sounds/alternate.wav")
	sample, err := s.Cache.LoadPath("sounds/voice.wav")
	if err != nil || sample == nil || sample.Provenance.LogicalPath != "sounds/voice.wav" {
		t.Fatalf("stream reused alias sample: %#v, err=%v", sample, err)
	}
}

func cacheIdentityService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sounds"), 0755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"voice", "alternate"} {
		if err := os.WriteFile(filepath.Join(root, "sounds", name+".wav"), buildRIFF(1, 11025, 8, []byte{128, byte(129 + i)}), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return NewService(fs)
}
