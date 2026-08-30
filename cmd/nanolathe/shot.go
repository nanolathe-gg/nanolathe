package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/nanolathe/nanolathe/internal/client"
)

// runShot composes one frame of a battle without opening a window and writes it
// as a PNG. It is the diagnostic path `Client.ComposeFrame` was written for
// [03 §2.4][I6]: the same session composition, the same presentation entry, and
// the same one-pass committed-frame composer the windowed loop calls, with the
// Ebitengine loop left unentered.
//
// It exists so a visual change can be reviewed as a picture rather than as an
// assertion that it should look right. Nothing here is a second renderer: every
// pixel comes from the production composer, and the session is stepped through
// the ordinary viewer step so the frame captured is a genuinely committed one.
func runShot(opts Options, cs *contentSet) error {
	if opts.Shot == "" {
		return fmt.Errorf("nanolathe: shot: no output path: logical path <command line>, providers searched [none], expected --shot <file.png>")
	}
	request, _, err := headlessFreshBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return err
	}
	sess := authoritative.Session

	var (
		b  *battleSession
		cl *client.Client
	)
	cl, err = client.New(client.Options{
		Buffer: sess.Snapshot,
		Width:  retailScreenW,
		Height: retailScreenH,
		Title:  "Nanolathe — " + opts.Map,
		Step:   func(delta float64) { b.viewerStep(delta, cl) },
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl.SetModelFS(cs.fs)
	b, err = composeBattleEntry(sess, authoritative.Session.Catalog, cs, cl, nil)
	if err != nil {
		return err
	}
	defer b.teardown(cl)

	// One tick of wall clock per authoritative tick at the 30 Hz rate [01 §4.1].
	// The viewer step owns the clock, the sub-tick budget and the publication
	// boundary, so driving it is what makes the captured frame a committed one.
	const tickSeconds = 1.0 / 30.0
	for i := 0; i < opts.ShotTicks; i++ {
		b.viewerStep(tickSeconds, cl)
	}

	img := cl.ComposeFrame()
	file, err := os.Create(opts.Shot)
	if err != nil {
		return fmt.Errorf("nanolathe: create shot %q: %w", opts.Shot, err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		return fmt.Errorf("nanolathe: write shot %q: %w", opts.Shot, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("nanolathe: close shot %q: %w", opts.Shot, err)
	}
	return nil
}
