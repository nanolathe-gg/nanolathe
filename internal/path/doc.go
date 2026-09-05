// Package path is the route search: weighted A* over the plot grid, its heap
// and node store, the goal families a request can name, and the per-player
// request scheduler [04 §7.2] [04 §7.3] [04 R-PATH-01].
//
// Nothing here reads a unit or an order. A caller submits a request naming a
// start cell, a goal and a movement profile, and polls for the route the
// scheduler admits under the per-player share; internal/movement owns both
// sides of that exchange and publishes what comes back.
package path
