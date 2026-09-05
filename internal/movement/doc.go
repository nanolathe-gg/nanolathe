// Package movement moves units: movement profiles, route publication and the
// ground follower, collision and occupancy, flight and its command block,
// takeoff and landing, and transports [04 §8] [04 §10.1] [04 §10.2]
// [04 R-MOV-01] [04 R-COLL-01] [04 R-AIR-01].
//
// It reads routes from internal/path and orders from internal/orders and
// writes the unit record's position, heading and mover mode. The per-file
// notes below and in the other files of this package say which contract each
// file owns.
package movement
