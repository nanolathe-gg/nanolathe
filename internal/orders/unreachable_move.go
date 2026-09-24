package orders

import "github.com/nanolathe-gg/nanolathe/internal/units"

// Nanolathe Modern policy: docs/DESIGN_UNITS_ORDERS_COB.md "Modern
// unreachable moves". Movement owns the unreachability certificate, its dwell
// and closing probe (docs/DESIGN_MOVEMENT_PATH.md "Modern unreachable
// moves"); orders owns which records may finish that way.

// UnreachableMoveArrival reports whether the bound order rules admit n as a
// move that may complete where its unit stands once movement has certified
// its goal unreachable. Movement asks it before probing and again before
// completing. It never writes the record or the queue and draws no RNG.
func UnreachableMoveArrival(u *units.Unit, n *Node) bool {
	return rulesOfUnit(u).UnreachableMoveArrival(u, n)
}

// UnreachableMoveArrival admits the crowded-arrival record eligibility without
// its radius, stillness and dwell, which movement's certificate replaces.
func (*ModernRules) UnreachableMoveArrival(u *units.Unit, n *Node) bool {
	return plainTerminalGroundMove(QueueOfUnit(u), u, n)
}
