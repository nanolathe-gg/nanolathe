// Package example is a third-party gameplay rule set, kept in the tree as the
// worked example of the extension point: Modern with the order seam's Hold
// Fire answer replaced by the retail one, so a held unit joins combat again.
// Only that seam changes — the combat seam keeps Modern's launch gate — which
// is exactly the point: an override is one answer of one package, not a mode.
//
// It exists to prove two things a design document can only assert. A set can
// be composed entirely from outside internal/ — this package changes no engine
// file — and overriding one answer of one package costs one method, because
// every other seam is filled from the base set it declares.
package example

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Name is the word `--gameplay` and the settings file select this set by.
const Name = "example"

// Registration is init-time: `mods` blank-imports this package and both
// commands import `mods`, so the name is selectable before any word is parsed.
func init() { session.RegisterRuleSet(Name, build) }

// orderRules is Modern's order policy with one answer replaced. Embedding the
// shipped implementation keeps the other four Modern order policies — the
// deferred bomber leash and the three guard-assistance legs — exactly as the
// engine ships them, and shadowing one promoted method is the whole override.
// It stays zero size, so binding it allocates nothing.
type orderRules struct{ orders.ModernRules }

// HoldsFire answers as Strict 3.1 does: a standing Hold Fire suppresses no
// combat join, which is the retail baseline of
// docs/DESIGN_UNITS_ORDERS_COB.md "Modern Hold Fire".
func (*orderRules) HoldsFire(*units.Unit) bool { return false }

// build states only what this set changes. The registry names the set after
// its registered name and fills every unstated seam from the declared base,
// which is Modern by default — so combat, construction and the unit-limit
// policy are the shipped Modern implementations.
func build() session.RuleSet {
	return session.RuleSet{Orders: &orderRules{}}
}
