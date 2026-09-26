// Package gameplay names Nanolathe's explicit simulation policy choices.
package gameplay

import (
	"fmt"
	"strings"
)

// Mode is independent of the presentation renderer. The zero value selects Modern.
//
// A mode word has two forms: one of the three reserved words below, or the name of
// any rule set this build registered (docs/DESIGN_GAMEPLAY_RULES.md §1). The
// reserved words are the vocabulary every persisted and reported value uses;
// a registered name is a selection the host carries from its flag or its
// settings file to the session, which resolves it once and then reports the
// reserved word its set derives from.
type Mode string

const (
	Modern      Mode = "modern"
	Community39 Mode = "community-3.9"
	Strict31    Mode = "strict-3.1"
)

// RetiredModernAI is the word of the rule set that once put the Modern AI
// behind every computer player. The Modern AI is chosen per computer player
// since 2026-09-25 (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer
// player", "Per-player selection"), so no build selects the word: Parse
// refuses it with the replacement, and a settings file that stores it loads
// as Modern with every computer row on the Modern AI (internal/settings).
const RetiredModernAI Mode = "modern-ai"

// NameRegistry is how a build tells this package which rule-set names it can
// select. The names belong to internal/session, which owns the registry and
// would be an import cycle here, so the session installs a view of it instead
// and this package stays a leaf.
type NameRegistry interface {
	// Known reports whether a word names a selectable rule set, reserved or
	// registered. It is asked once per parsed or normalized word and never in
	// a tick, but it is a lookup rather than a listing so that answering
	// allocates nothing.
	Known(name string) bool
	// Names lists every selectable name for a diagnostic, reserved first.
	Names() []string
}

// names is the installed view, written once before any word is parsed and
// read-only afterwards. A build that installs none — a test binary of a
// package below the session, for instance — knows only the reserved words.
var names NameRegistry

// UseNameRegistry installs the build's selectable rule-set names. It is an
// init-time call with a single writer (internal/session), so nothing
// synchronizes it: every read happens after every package init has run.
func UseNameRegistry(r NameRegistry) { names = r }

// selectable reports whether this build can select the word as it stands.
func (m Mode) selectable() bool {
	if m == Modern || m == Community39 || m == Strict31 {
		return true
	}
	return names != nil && names.Known(string(m))
}

// selectableNames lists what a rejected word could have been.
func selectableNames() []string {
	if names != nil {
		return names.Names()
	}
	return []string{string(Modern), string(Community39), string(Strict31)}
}

// Normalize canonicalizes a stored or parsed selection: a reserved word and a
// registered name are kept as they stand, and a word this build cannot select
// becomes Modern, the default. It is what a settings loader, a host option and
// a session constructor apply to a word of unknown provenance.
//
// Normalize answers the *selection* question, not the reserved-base one. A
// registered set declares which reserved set it derives from and the
// session's bound set answers that instead; see
// docs/DESIGN_GAMEPLAY_RULES.md §1.
func (m Mode) Normalize() Mode {
	if m.selectable() {
		return m
	}
	return Modern
}

// Parse accepts a reserved word or the name of a rule set this build
// registered, and rejects anything else with the selectable names in the
// diagnostic so the caller can act on the failure.
func Parse(text string) (Mode, error) {
	if mode := Mode(text); mode.selectable() {
		return mode, nil
	}
	if Mode(text) == RetiredModernAI {
		return Modern, fmt.Errorf("nanolathe: gameplay rule set %q was removed: logical path <command line>, providers searched [gameplay, mods], expected one of %s, with each computer player's AI chosen per player (--gameplay modern --ai-player all=modern)", text, strings.Join(selectableNames(), ", "))
	}
	return Modern, fmt.Errorf("nanolathe: invalid gameplay rule set %q: logical path <command line>, providers searched [gameplay, mods], expected one of %s", text, strings.Join(selectableNames(), ", "))
}
