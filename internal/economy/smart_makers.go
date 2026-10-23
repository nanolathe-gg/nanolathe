package economy

import (
	"cmp"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The Smart makers mutator's controller (docs/DESIGN_MODS_MUTATORS.md "Smart
// makers"). After a human player's settlement pass it switches that player's
// finished metal makers — the definitions the mutator marked SmartMaker — with
// the player's energy:
//
//   - at or above 80% of energy storage it switches makers on, most efficient
//     first, as long as their upkeep fits in the pass's surplus plus the
//     stored energy above 80%, so one step never takes storage below 80%;
//   - below 40% it switches makers off, least efficient first, until their
//     upkeep covers the pass's shortfall plus the gap up to 40%;
//   - in between it changes nothing, and neither does a band whose budget or
//     shortfall is not positive.
//
// Efficiency is metal made per energy used, the authored makesmetal byte over
// energyuse; equal makers keep slot order. The decision reads only state the
// settlement has just committed — stock, capacity and the pass's produced and
// requested energy — and the makers' activation bits, so it keeps no state of
// its own, draws no random number and needs no save or checkpoint field. A
// switch goes through the unit's one activation writer, as the ON/OFF orders
// and the Classic computer player's eco toggle do [04 R-UNIT-06 §2]
// [08 R-AI-01 §2], so the change bills from the next pass.
//
// Arithmetic is integer: each stored single is converted once to whole
// energy units, truncated toward zero, and every threshold is compared in
// fifths of those units, so no division or new floating-point state is formed
// and no threshold is decided by a floating comparison (I2, I3). Fractions of
// an energy unit do not move a 40% or 80% decision by more than one unit.

// makerEnergyLimit bounds a converted magnitude, far above any reachable
// storage, so the conversion keeps the whole value and every product below
// stays inside 64 bits.
const makerEnergyLimit = 1 << 30

// makerEnergy converts a stored single to whole signed energy units,
// truncated toward zero. A magnitude beyond makerEnergyLimit saturates; NaN
// fails both comparisons and the defined conversion reads it as zero
// [01 R-DET-01 §1].
func makerEnergy(v float32) int64 {
	if v > makerEnergyLimit {
		v = makerEnergyLimit
	} else if v < -makerEnergyLimit {
		v = -makerEnergyLimit
	}
	return int64(numeric.TruncateFloat32ToLow32(v))
}

// smartMaker is one managed maker on one pass: its unit, the metal it makes
// and the energy it uses, in whole units and at least one.
type smartMaker struct {
	unit   *units.Unit
	makes  int64
	upkeep int64
}

// manageSmartMakers runs the controller for player after its settlement
// pass. Only a human player (control byte 1) that is not an observer is
// managed: a computer player keeps its own maker logic, which for the Classic
// player is retail's eco toggle [08 R-AI-01 §2]. A player whose upkeep is
// paused under the Modern activity holds is left alone, so the controller
// never restarts what the pause shut down. Under the Modern Generation hold
// a maker makes no metal, so the controller switches none on, though it
// still sheds makers below 40%.
func (s *Service) manageSmartMakers(player int, w *units.World) {
	if s == nil || w == nil || player < 0 || player >= len(s.Players) {
		return
	}
	p := &s.Players[player]
	if !p.Exists || p.ControllerState != 1 || p.IsObserver || s.ActivityHeld(uint8(player), ActivityUpkeep) {
		return
	}
	capacity := makerEnergy(p.Capacity[Energy])
	if capacity <= 0 {
		// With no storage there is no fraction to keep.
		return
	}
	stock := makerEnergy(p.Stock[Energy])
	net := makerEnergy(p.PassProduced[Energy]) - makerEnergy(p.PassConsumed[Energy])
	// In fifths: stock >= 80% of capacity is 5·stock >= 4·capacity, and
	// stock < 40% is 5·stock < 2·capacity.
	var budget, need int64
	switch {
	case 5*stock >= 4*capacity:
		budget = 5*net + 5*stock - 4*capacity
		if budget <= 0 || s.ActivityHeld(uint8(player), ActivityGeneration) {
			return
		}
	case 5*stock < 2*capacity:
		need = 2*capacity - 5*stock - 5*net
		if need <= 0 {
			return
		}
	default:
		return
	}
	var makers []smartMaker
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		if u.Def == nil || !u.Def.SmartMaker || !u.Def.OnOffable || u.Remaining != 0 {
			return
		}
		upkeep := makerEnergy(float32(u.Def.EnergyUse))
		if upkeep < 1 {
			upkeep = 1
		}
		makers = append(makers, smartMaker{unit: u, makes: int64(uint8(u.Def.MakesMetal)), upkeep: upkeep})
	})
	// Most efficient first: a before b when a.makes/a.upkeep exceeds
	// b.makes/b.upkeep, compared by cross-multiplication. The stable sort
	// keeps slot order between equals.
	slices.SortStableFunc(makers, func(a, b smartMaker) int {
		return cmp.Compare(b.makes*a.upkeep, a.makes*b.upkeep)
	})
	if budget > 0 {
		for _, m := range makers {
			if cost := 5 * m.upkeep; !m.unit.Activated && cost <= budget {
				m.unit.SetActivationEdge(true)
				budget -= cost
			}
		}
		return
	}
	for i := len(makers) - 1; i >= 0 && need > 0; i-- {
		if m := makers[i]; m.unit.Activated {
			m.unit.SetActivationEdge(false)
			need -= 5 * m.upkeep
		}
	}
}
