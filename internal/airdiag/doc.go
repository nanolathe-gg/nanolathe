// Package airdiag is a diagnostic harness: it composes a real authoritative
// session on an authored map, spawns a named aircraft, issues a real human
// order through the ordinary command boundary, and records one trace row per
// authoritative tick.
//
// Nothing here is simulation state. The harness only reads committed values
// after each Session.Step and never writes into a sim package, so it is outside
// the presentation boundary rules of [I6] in the same way a test is.
package airdiag
