package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// standingFixture builds a live builder/product pair owned by one player whose
// economy row carries the given control byte.
func standingFixture(t *testing.T, controlByte uint8) (*Service, *units.Unit, *units.Unit) {
	t.Helper()
	facDef := newProductDef("armfac", 1, 1, 100, 100)
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	prodDef.BMCode = 1
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		facDef.CanonicalKey:  facDef,
		prodDef.CanonicalKey: prodDef,
	}}
	w := newConstructionFixtureWorld(4, cat)
	fh, _ := w.Create(facDef, 0, 0, 0, 0)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	econ := &economy.Service{}
	econ.Players[0].Exists = true
	econ.Players[0].ControllerState = controlByte
	svc := NewService(nil, cat, w, econ)
	return svc, w.Unit(fh), w.Unit(ph)
}

// The post-build merge of [04 §3.8] / [04 R-FAC-02 §4]: standing-move bits
// 18-19 and standing-fire bits 20-21 copy from builder to product under the
// alive / death-latch guard, and the experience word rides the same block only
// when the owner's control byte reads as a computer player — 2, not 1
// [05 R-SHARE-01 §1].
func TestRallyInheritanceMergesStandingFields(t *testing.T) {
	t.Run("human owner copies the bits but not the experience word", func(t *testing.T) {
		svc, factory, product := standingFixture(t, 1)
		factory.Flags |= StandingMoveMask | StandingFireMask
		factory.Kills = 7
		product.Flags &^= StandingMoveMask | StandingFireMask
		product.Kills = 0

		svc.rallyInheritance(factory, product, 10)

		if got := product.Flags & (StandingMoveMask | StandingFireMask); got != StandingMoveMask|StandingFireMask {
			t.Fatalf("standing bits = %#x, want %#x", got, StandingMoveMask|StandingFireMask)
		}
		if product.Kills != 0 {
			t.Fatalf("experience word = %d, want it untouched for a human-owned builder", product.Kills)
		}
	})

	t.Run("computer owner also copies the experience word", func(t *testing.T) {
		svc, factory, product := standingFixture(t, controlByteComputer)
		factory.Flags |= StandingMoveMask | StandingFireMask
		factory.Kills = 7
		product.Flags &^= StandingMoveMask | StandingFireMask
		product.Kills = 0

		svc.rallyInheritance(factory, product, 10)

		if got := product.Flags & (StandingMoveMask | StandingFireMask); got != StandingMoveMask|StandingFireMask {
			t.Fatalf("standing bits = %#x, want %#x", got, StandingMoveMask|StandingFireMask)
		}
		if product.Kills != 7 {
			t.Fatalf("experience word = %d, want the builder's 7", product.Kills)
		}
	})

	t.Run("the death latch blocks both copies", func(t *testing.T) {
		svc, factory, product := standingFixture(t, controlByteComputer)
		factory.Flags |= StandingMoveMask | StandingFireMask
		factory.Kills = 7
		product.Flags &^= StandingMoveMask | StandingFireMask
		product.Kills = 0
		product.Dying = true

		svc.rallyInheritance(factory, product, 10)

		if got := product.Flags & (StandingMoveMask | StandingFireMask); got != 0 {
			t.Fatalf("standing bits = %#x, want none — the death latch closes the guard", got)
		}
		if product.Kills != 0 {
			t.Fatalf("experience word = %d, want it untouched behind the closed guard", product.Kills)
		}
	})
}
