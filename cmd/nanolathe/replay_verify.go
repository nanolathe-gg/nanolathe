package main

// --verify-replay: play a replay headless at full speed with the installed
// content, check every recorded checksum, and say whether this build still
// reproduces it (docs/DESIGN_MULTIPLAYER.md §10).

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
)

// replayVerification is what a verified replay played.
type replayVerification struct {
	Header replay.Header
	// Ticks is the last tick played, Verified the checksums matched.
	Ticks    uint32
	Verified int
	// Truncated marks a recording without its end entry, played to its last
	// whole chunk; End is its end reason otherwise.
	Truncated bool
	End       replay.EndReason
	// Final is the played battle's unit checksum where playback stopped.
	Final [32]byte
}

// String is the verifier's one-line summary.
func (v replayVerification) String() string {
	ending := "complete, " + v.End.String()
	if v.Truncated {
		ending = "truncated at its last whole chunk"
	}
	return fmt.Sprintf("nanolathe: replay verified: %s on %s, %d ticks, %d checksums matched, %s", v.Header.Kind, v.Header.MapName, v.Ticks, v.Verified, ending)
}

// verifyReplay plays a replay to its end headless on cs. The battle binds the
// phase-7 model registry and fragment materials exactly as the recording
// host's did (installHeadlessModelTextureRegistry, as --headless and the
// window bind them). It returns the first mismatch as a *replay.Mismatch
// naming its tick and both checksums, and an install that cannot compose the
// battle as an error wrapping replay.ErrIncompatible.
func verifyReplay(data []byte, cs *contentSet) (replayVerification, error) {
	r, err := replay.NewReader(data)
	if err != nil {
		return replayVerification{}, err
	}
	v := replayVerification{Header: r.Header()}
	sess, err := replay.Compose(v.Header, replayContent(cs, nil))
	if err != nil {
		return v, err
	}
	if _, err := installHeadlessModelTextureRegistry(sess, cs.unmappedMount, cs.presentation.TeamLogos); err != nil {
		return v, err
	}
	if sess.Features != nil {
		defer sess.Features.SetDefinitionAdmissionObserver(nil)
	}
	player, err := replay.NewPlayer(sess, r)
	if err != nil {
		return v, err
	}
	var discarded []frame.EventView
	for {
		_, err := player.Step(1 << 16)
		// Nothing presents a verification: its events and receipts are
		// dropped as a displayless run drops them.
		if sess.Snapshot != nil {
			discarded = sess.Snapshot.DrainCommittedEvents(discarded)
		}
		sess.DrainCommandReceipts()
		v.Ticks, v.Verified, v.Truncated, v.End = player.Tick(), player.Verified(), player.Truncated(), player.End()
		v.Final = sess.UnitStateChecksum()
		if errors.Is(err, io.EOF) {
			return v, nil
		}
		if err != nil {
			return v, err
		}
	}
}

// runVerifyReplay is --verify-replay: it prints the summary on success and
// fails naming the first mismatch or the incompatibility.
func runVerifyReplay(opts Options, cs *contentSet, out io.Writer) error {
	data, err := os.ReadFile(opts.VerifyReplay)
	if err != nil {
		return fmt.Errorf("nanolathe: reading a replay failed: logical path %s, providers searched [--verify-replay], expected a readable file: %w", opts.VerifyReplay, err)
	}
	v, err := verifyReplay(data, cs)
	if err != nil {
		return fmt.Errorf("nanolathe: replay %s failed verification after %d ticks and %d matched checksums: %w", opts.VerifyReplay, v.Ticks, v.Verified, err)
	}
	_, err = fmt.Fprintln(out, v)
	return err
}

// selectReplayContent mounts the mod a replay names for --replay and
// --verify-replay: unless --mod chose one, the run takes the replay's mod, or
// none, rather than the saved choice. The playback still checks the mounted
// mod's identity against the recorded one.
func selectReplayContent(opts *Options) error {
	path := opts.VerifyReplay
	if path == "" {
		path = opts.Replay
	}
	if path == "" || opts.ModSet {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("nanolathe: reading a replay failed: logical path %s, providers searched [replays], expected a readable file: %w", path, err)
	}
	r, err := replay.NewReader(data)
	if err != nil {
		return err
	}
	mod := r.Header().Identity.Mod
	opts.Mod, opts.ModSet = "none", true
	if mod.ID != "" {
		opts.Mod = modSelectorOf(mod.ID, mod.Version)
		if _, _, err := modlibrary.ParseSelector(opts.Mod); err != nil {
			return fmt.Errorf("nanolathe: replay mod selection failed: logical path %s, providers searched [replay header], expected an installable mod selector: %w", path, err)
		}
	}
	return nil
}
