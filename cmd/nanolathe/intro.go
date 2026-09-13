package main

import (
	"errors"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// showIntroUnavailable keeps an explicit Intro request visible while playback
// is unfinished. The diagnostic is Nanolathe policy: retail silently skips a
// missing Data/2.zrb [07 R-FE-01 §3][08 R-OOS-01 §4].
func (g *gameShell) showIntroUnavailable() error {
	const logical = "data/2.zrb"
	message := "The original intro movie (Data/2.zrb) is missing from your game files. Intro playback is not yet supported."
	if g.cs != nil && g.cs.fs != nil {
		info, err := g.cs.fs.Stat(logical)
		switch {
		case err == nil && !info.IsDir:
			message = "The original intro movie (Data/2.zrb) was found, but Intro playback is not yet supported."
		case err != nil && !errors.Is(err, vfs.ErrNotFound):
			message = "The original intro movie (Data/2.zrb) could not be accessed. Intro playback is not yet supported."
		}
	}
	// TODO(question): implement and validate an independently authored video
	// decoder against the original Data/2.zrb; its container, frame cadence,
	// palette and audio tracks need confirmation from that missing asset.
	// Keep the main menu and its audio alive until playback can actually start.
	return g.showRetailMessage(message)
}
