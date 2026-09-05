// Package construction owns what a builder does to the world: factory and
// mobile production, the assist and repair work step, capture, reclaim,
// resurrection and the placement a finished building holds.
//
// The service drives its own per-unit step rather than the order pump. It
// registers the build rows on each queue it binds, so the pump dispatches them
// and stops, and the state machine of [05 "Factory production lifecycle"] then
// owns each record's phase, gate and deadline. Everything it spends goes
// through the economy's admission, and everything it creates goes through the
// nanoframe allocator and the placement reservation.
package construction
