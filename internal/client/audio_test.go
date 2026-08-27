package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestPlayPositionalRequiresVisibilityPredicate(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	cache := audio.NewCache(nil)
	if _, err := cache.Put("pos_alias", []byte{128, 128}); err != nil {
		t.Fatal(err)
	}
	c.SetAudioCache(cache)
	be := audio.NewBackend(true)
	old := audio.GlobalBackend()
	audio.SetGlobalBackend(be)
	defer audio.SetGlobalBackend(old)

	pos := [3]numeric.Fixed{}
	if _, _, ok := c.PlayPositional("pos_alias", pos, nil); ok {
		t.Fatal("positional audio should fail closed without a visibility predicate")
	}
	if be.PlayCount() != 0 {
		t.Fatalf("positional audio without a visibility predicate played %d times", be.PlayCount())
	}

	// Other positional contracts may opt into an explicit always-audible test
	// predicate; this must continue to exercise pan/attenuation and playback.
	if _, _, ok := c.PlayPositional("pos_alias", pos, func([3]numeric.Fixed) bool { return true }); !ok {
		t.Fatal("explicit visibility predicate should admit positional audio")
	}
	if be.PlayCount() != 1 {
		t.Fatalf("explicitly audible positional audio played %d times, want 1", be.PlayCount())
	}
}
