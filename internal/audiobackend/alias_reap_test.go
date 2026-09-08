package audiobackend

import (
	"io"
	"testing"
	"time"
)

// Registered admission uses the tracked count until an actual reaper call.
// A later completed voice must not save an older live voice from stealing
// [03 R-AUD-01 §1 steps 2,7][03 R-AUD-02 §2].
func TestRegisteredAdmissionRetainsStoppedSlotsUntilPump(t *testing.T) {
	b, made := newTransientBackend(t, 2)
	now := time.Unix(100, 0)
	b.Pump(now)
	for range 2 {
		if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	(*made)[1].playing = false
	if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if (*made)[0].playing || (*made)[0].stops != 1 || (*made)[1].stops != 0 || len(b.players) != 2 {
		t.Fatal("registered admission swept a completed slot instead of stealing the oldest tracked voice")
	}
	b.Pump(now.Add(99 * time.Millisecond))
	if len(b.players) != 2 {
		t.Fatal("stopped slot disappeared before pump deadline")
	}
	b.Pump(now.Add(100 * time.Millisecond))
	if len(b.players) != 1 || b.players[0].player != (*made)[2] || (*made)[1].stops != 1 || !(*made)[2].playing {
		t.Fatal("eligible pump did not reclaim only the completed slot")
	}
}

func TestTransientReapPrecedesDeviceCreationOnly(t *testing.T) {
	for _, stopsDuringCreation := range []bool{false, true} {
		name := "stopped before load"
		if stopsDuringCreation {
			name = "stopped during creation"
		}
		t.Run(name, func(t *testing.T) {
			b, made := newTransientBackend(t, 2)
			for range 2 {
				if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
					t.Fatal(err)
				}
			}
			if stopsDuringCreation {
				create := b.createPlayer
				b.createPlayer = func(r io.Reader) (outputPlayer, error) { (*made)[1].playing = false; return create(r) }
			} else {
				(*made)[1].playing = false
			}
			if err := b.PlaySample(transientSample(), 1, 0); err != nil {
				t.Fatal(err)
			}
			if len(*made) != 3 || len(b.players) != 2 || len(b.transients) != 1 {
				t.Fatal("unexpected admission state")
			}
			if stopsDuringCreation {
				if (*made)[0].playing || (*made)[0].stops != 1 || (*made)[1].stops != 0 {
					t.Fatal("mixer performed a second transient status sweep after creation")
				}
			} else if !(*made)[0].playing || (*made)[0].stops != 0 || (*made)[1].stops != 1 {
				t.Fatal("transient load omitted its pre-creation reap")
			}
		})
	}
}
