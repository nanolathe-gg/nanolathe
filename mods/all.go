// Package mods is the list of gameplay rule sets a build links beyond the
// three reserved ones. Importing it registers every shipped set, which is what
// makes the set selectable by name from `--gameplay` and from the settings
// file (docs/DESIGN_GAMEPLAY_RULES.md §8).
//
// Only a command imports this package. Nothing under internal/ may, and a
// guard in internal/architecture enforces that: a simulation package that
// reached a mod would make its own behavior depend on which sets happen to be
// linked, and the dependency direction is cmd → mods → session → simulation.
//
// Everything linked here is selectable in the game. A research harness's own
// set (the AI arena's "aikit") is registered by that harness, not here.
package mods

import (
	// Not a rule set: the Modern AI computer player's think step, which the
	// session gives every computer player marked Modern in any rule set
	// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player").
	_ "github.com/nanolathe-gg/nanolathe/mods/aikit"
	// The example set: Modern with one order answer replaced, kept linked as
	// the proof that a set can be composed from outside internal/.
	_ "github.com/nanolathe-gg/nanolathe/mods/example"
)
