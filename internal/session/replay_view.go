package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The playback perspective (docs/DESIGN_MULTIPLAYER.md §10 "Viewing is
// presentation"). In a single-player battle the local and viewing owners are
// simulation inputs: the viewing slot chooses whose sensor pass runs at its
// settlement deadline, whose units leave temporary sight and what `+view`
// moves, and the canonical checkpoint hashes both. A playback therefore never
// moves them. It sets this override instead, which only publication reads,
// after every authoritative read of the tick is done.

// PresentationPerspective chooses what playback draws without touching the
// simulation's local or viewing owner, which are simulation inputs in a
// single-player battle. Override false draws as the battle always has.
// RevealAll draws the whole map with no fog.
type PresentationPerspective struct {
	Owner     uint8
	Override  bool
	RevealAll bool
}

// SetPresentationPerspective sets the perspective publication draws from.
// It changes no random stream, checksum or canonical digest, and takes effect
// at the next publication; a paused playback shows it at once through
// RepublishPresentation. It may be called while a pump runs on another
// goroutine.
//
// An overridden owner publishes that player's coverage, line of sight and
// contacts, and no fog, since the fog cache is the simulation viewer's. A
// single-player battle keeps one sensor bank, the viewing owner's, so unit
// sensor bits stay that player's; an online battle keeps one per seat
// (DESIGN_MULTIPLAYER §16.4.1). Placement previews, the audio audience and
// the online effect filter keep the simulation's viewing owner.
func (s *Session) SetPresentationPerspective(p PresentationPerspective) error {
	if s == nil {
		return fmt.Errorf("nanolathe: presentation perspective refused: logical path session, providers searched [session], expected a session")
	}
	if p.Override && p.Owner > 9 {
		return fmt.Errorf("nanolathe: presentation perspective refused: logical path owner %d, providers searched [session], expected a player 0..9", p.Owner)
	}
	if !p.Override {
		p.Owner = 0
	}
	r := &s.seatCommands.replay
	r.mu.Lock()
	r.perspective = p
	r.mu.Unlock()
	return nil
}

// RepublishPresentation publishes the committed tick again from unchanged
// state, so a perspective change shows while playback is paused. It is the
// paused-input boundary's republication with no input applied
// (DESIGN_INTERFACE_HUD_INPUT §3.12): no tick runs, no input is drained and
// no authoritative state changes. Call it between pumps, as every session
// call that is not documented otherwise. It reports whether a frame was
// published.
func (s *Session) RepublishPresentation() bool {
	if s == nil || s.Snapshot == nil || s.Clock == nil || s.State != StateBattle {
		return false
	}
	if _, published := s.Snapshot.PublishedTick(); !published {
		return false
	}
	// The boundary's leading act: the interface facts staged for the last
	// publication were delivered with it and must not be delivered again.
	s.resetBigBrotherEvents()
	s.publishPausedSnapshot(s.Clock.GlobalTick)
	if s.publicationObserver != nil {
		s.publicationObserver(s.Snapshot.Current())
	}
	return true
}

// publicationPerspective is what one publication draws: the viewer whose
// sight it publishes and whether the whole map is revealed.
func (s *Session) publicationPerspective() (viewer uint8, reveal bool) {
	r := &s.seatCommands.replay
	r.mu.Lock()
	p := r.perspective
	r.mu.Unlock()
	viewer = s.ViewingOwner
	if p.Override {
		viewer = p.Owner
	}
	return viewer, p.RevealAll
}

// publishPerspectiveView finishes the published visibility channels for the
// perspective, after the authoritative visibility and fog work of the
// publication has run unchanged. Revealing publishes every cell visible and
// explored under an identity of its own, so no frame slot restores it as the
// viewer's real coverage and no presentation cache confuses the two; the fog
// is not drawn. The fog cache describes the simulation's viewing owner only
// (the visibility service derives it for its local player), so another
// viewer's frame carries no fog rather than a different player's; drawing
// that viewer's fog needs a visibility read that derives it for a given
// player into caller storage.
func (s *Session) publishPerspectiveView(published *frame.Frame, viewer uint8, reveal bool) {
	if s.Vis == nil {
		return
	}
	if reveal {
		if w, h := s.Vis.GridDimensions(); w > 0 && h > 0 {
			n := int(w) * int(h)
			v := &published.Visibility
			v.Visible = fillBytesInto(v.Visible, n, 1)
			v.WordVisible = fillWordsInto(v.WordVisible, n, 0xffff)
			v.W, v.H = w, h
			v.CoverageBytes, v.Valid = true, true
			v.MappingSource, v.MappingVersion = 0, 0
			if source := s.Vis.PresentationIdentity(); source != 0 {
				// Real sources count up from one; the complement never
				// meets one.
				v.MappingSource, v.MappingVersion = ^source, 1
			}
		}
	}
	if reveal || viewer != s.ViewingOwner {
		published.Fog.Valid = false
		published.Fog.Version, published.Fog.Source = 0, 0
	}
}

func fillBytesInto(dst []uint8, n int, v uint8) []uint8 {
	if cap(dst) < n {
		dst = make([]uint8, n)
	}
	dst = dst[:n]
	for i := range dst {
		dst[i] = v
	}
	return dst
}

func fillWordsInto(dst []uint16, n int, v uint16) []uint16 {
	if cap(dst) < n {
		dst = make([]uint16, n)
	}
	dst = dst[:n]
	for i := range dst {
		dst[i] = v
	}
	return dst
}
