package replay

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Content is the installed content a recorded battle composes from.
type Content struct {
	// FS is the mounted content view, the content profile applied, as
	// battle entry reads it.
	FS vfs.FSOps
	// Catalog is the unrestricted catalog compiled from FS, or nil to
	// compile one.
	Catalog *content.Catalog
	// Mod is the mounted mod's public identity, zero for base content.
	Mod session.MatchMod
	// Progress observes the load, or nil.
	Progress content.Progress
}

// compatibilityError is the one diagnostic shape of a replay this install
// cannot compose.
func compatibilityError(path, expected string, cause error) error {
	err := fmt.Errorf("%w: logical path %s, providers searched [replay header, installed content], expected %s", ErrIncompatible, path, expected)
	if cause != nil {
		return fmt.Errorf("%w: %w", err, cause)
	}
	return err
}

// Compose rebuilds a recorded battle for playback from installed content:
// it decodes the header's configuration, freezes the content it names and
// compares this install's identity with the recorded one — protocol,
// content, map, rules, mod and configuration; the build is advisory, as
// online (DESIGN_MULTIPLAYER §8.2) — then composes the battle as the
// recording host did and prepares it for its first tick. A single-player
// recording composes through the admitted path (session.NewAdmittedSkirmish,
// which composes the battle single-player entry composes) and an online one
// through the composition online play uses (session.NewPlaytestSkirmish) for
// the recording seat. Every refusal of the content wraps ErrIncompatible.
//
// The host binds presentation services, such as the phase-7 registry, as it
// does for any battle before its first pump; the battle then plays with
// NewPlayer.
func Compose(h Header, c Content) (*session.Session, error) {
	config, err := session.DecodeMatchConfig(h.Config)
	if err != nil {
		return nil, compatibilityError("header.config", "a configuration this build decodes", err)
	}
	if got := config.Request().SessionKind; got != h.Kind.configKind() {
		return nil, formatError("header.config", fmt.Sprintf("a configuration of the header's %s kind, got session kind %d", h.Kind, got))
	}
	if c.FS == nil {
		return nil, compatibilityError("content", "mounted content", nil)
	}
	inputs, err := session.FreezeMatchInputs(c.FS, c.Catalog, config, c.Progress)
	if err != nil {
		return nil, compatibilityError("content", "content this install can freeze for the recorded configuration", err)
	}
	// The build is advisory: the playback's own checksums decide whether
	// this simulation reproduces the recording.
	recorded := h.Identity
	recorded.Build = [32]byte{}
	if recorded.Map == ([32]byte{}) {
		recorded.Map = session.MatchMapIdentity(inputs)
	}
	if recorded.Content == ([32]byte{}) {
		recorded.Content = inputs.Digest()
	}
	if err := session.CompareMatchIdentity(session.MatchJoin{Inputs: inputs, Config: config, Mod: c.Mod}, recorded); err != nil {
		return nil, compatibilityError("header.identity", "the recorded identity", err)
	}
	var s *session.Session
	if h.Kind.Online() {
		s, err = session.NewPlaytestSkirmish(inputs, config, h.LocalSeat, c.Progress)
		if err == nil {
			err = s.PrepareGrantedBattle()
		}
	} else {
		s, err = session.NewAdmittedSkirmish(inputs, config, c.Progress)
		if err == nil {
			err = s.PrepareRecordedBattle()
		}
	}
	if err != nil {
		if errors.Is(err, session.ErrMatchNeedsMultiSeat) || errors.Is(err, session.ErrMatchConfigurationRejected) {
			return nil, compatibilityError("header.config", "a battle this build composes", err)
		}
		return nil, err
	}
	return s, nil
}
