package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// applyEntryMutators applies the battle's mutators to the catalog one battle
// entry runs on (docs/DESIGN_MODS_MUTATORS.md §6.3). A zero set returns cat
// itself, so a battle without mutators runs on exactly the catalog it always
// did and every fingerprint lock is unchanged. Otherwise it returns a mutated
// clone: the catalog handed in may be shared between battles and is never
// written.
//
// Mutators are a transform of content, not a gameplay rule, so there is no
// gameplay-mode test here: they apply in Strict 3.1 as in every other mode
// (INVARIANTS I11, decision D1).
func applyEntryMutators(cat *content.Catalog, m content.Mutators) (*content.Catalog, error) {
	if cat == nil || m.IsZero() {
		return cat, nil
	}
	out := cat.Clone()
	if err := out.ApplyMutators(m); err != nil {
		return nil, fmt.Errorf("session: battle-entry mutators %q: %w", m.String(), err)
	}
	return out, nil
}
