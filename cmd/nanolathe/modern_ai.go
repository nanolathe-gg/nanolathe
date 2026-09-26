package main

// The Modern AI computer players' configured brain parameters: the saved
// modernAI block, the --ai flag and a save's record
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Configuration"). Everything here runs before a battle; the session merges
// the layers per computer player at battle entry and the Modern AI
// controller only parses the canonical text it is handed.

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// aiParamProviders are the three layers whose keys a parameter may name.
var aiParamProviders = []string{"utility parameters", "variety switches", "tactics switches"}

// aiParamsError is a rejected parameter in the command's diagnostic shape.
func aiParamsError(logical string, err error) error {
	return fmt.Errorf("%w: %v", &missingProductError{
		what:      "Modern AI parameter rejected",
		logical:   logical,
		providers: aiParamProviders,
		expected:  "a key and value the util+tac brain reads",
	}, err)
}

// resolveStartupAIOverrides applies the mutators' precedence (§6.6 of
// docs/DESIGN_MODS_MUTATORS.md): --ai flags win over the saved block, and
// set every computer player's parameters; captures, benchmarks and
// displayless runs never read the block, so they reproduce from their
// command line. A bad key or value in the block is a start-up error.
func resolveStartupAIOverrides(opts Options) (session.AIOverrides, error) {
	if len(opts.AIArgs) > 0 {
		all, err := session.CanonicalAIParams(opts.AIArgs)
		if err != nil {
			return session.AIOverrides{}, aiParamsError("--ai", err)
		}
		return session.AIOverrides{All: all}, nil
	}
	if opts.ignoresSavedSelection() {
		return session.AIOverrides{}, nil
	}
	// A file that cannot be read starts from the defaults; the window's own
	// settings load reports it.
	stored, _ := settings.Load()
	path, _ := settings.Path()
	return aiOverridesFromSettings(stored.ModernAI, path)
}

// aiOverridesFromSettings checks and canonicalizes the saved block. Its
// difficulty keys are easy, medium and hard; its player keys are the
// lobby's slot numbers, 1 to 10.
func aiOverridesFromSettings(block settings.ModernAI, path string) (session.AIOverrides, error) {
	var out session.AIOverrides
	var err error
	if out.All, err = canonicalAILayer(block.All, path, "modernAI.all"); err != nil {
		return session.AIOverrides{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(block.Difficulty)) {
		index := slices.Index([]string{"easy", "medium", "hard"}, name)
		logical := "modernAI.difficulty." + name
		if index < 0 {
			return session.AIOverrides{}, &missingProductError{what: "Modern AI difficulty rejected", logical: logical + " in " + path, providers: []string{"settings"}, expected: "easy, medium or hard"}
		}
		if out.Difficulty[index], err = canonicalAILayer(block.Difficulty[name], path, logical); err != nil {
			return session.AIOverrides{}, err
		}
	}
	for _, slot := range slices.Sorted(maps.Keys(block.Players)) {
		n, convErr := strconv.Atoi(slot)
		logical := "modernAI.players." + slot
		if convErr != nil || n < 1 || n > len(out.Players) || strconv.Itoa(n) != slot {
			return session.AIOverrides{}, &missingProductError{what: "Modern AI player slot rejected", logical: logical + " in " + path, providers: []string{"settings"}, expected: fmt.Sprintf("a lobby slot number from 1 to %d", len(out.Players))}
		}
		if out.Players[n-1], err = canonicalAILayer(block.Players[slot], path, logical); err != nil {
			return session.AIOverrides{}, err
		}
	}
	return out, nil
}

// canonicalAILayer checks one layer against the brain's keys and spells it
// canonically.
func canonicalAILayer(layer settings.AIParams, path, logical string) (string, error) {
	where := logical + " in " + path
	if err := aikitmod.ValidateParams(layer); err != nil {
		return "", aiParamsError(where, err)
	}
	text, err := session.CanonicalAIParams(layer)
	if err != nil {
		return "", aiParamsError(where, err)
	}
	return text, nil
}

// validateRecordedAIOverrides refuses a save whose record names parameters
// this build's brain does not read, as an unknown mutator is refused, rather
// than loading its computer players with the defaults.
func validateRecordedAIOverrides(rec *session.AIControllers) error {
	if rec == nil {
		return nil
	}
	for _, o := range rec.Overrides {
		if _, err := aikitmod.ValidateParamsText(o.Params); err != nil {
			return refuseLoad("This game was saved with Modern AI parameters this build does not know (player %d): %v", int(o.Player)+1, err)
		}
	}
	return nil
}
