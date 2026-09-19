// Package mods is the list of gameplay rule sets a build links beyond the two
// reserved ones. Importing it registers every shipped set, which is what makes
// the set selectable by name from `--gameplay` and from the settings file
// (docs/DESIGN_GAMEPLAY_RULES.md §8).
//
// Only a command imports this package. Nothing under internal/ may, and a
// guard in internal/architecture enforces that: a simulation package that
// reached a mod would make its own behavior depend on which sets happen to be
// linked, and the dependency direction is cmd → mods → session → simulation.
package mods

import (
	// The example set: Modern with one order answer replaced, kept linked as
	// the proof that a set can be composed from outside internal/.
	_ "github.com/nanolathe-gg/nanolathe/mods/example"
)
