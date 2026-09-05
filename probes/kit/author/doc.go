// Package author writes the small binary fixtures the probe kit ships.
//
// Every layout here follows research/formats byte for byte — tnt.md, gaf.md,
// 3do.md, cob.md and pal.md — and nothing else: the writers emit exactly the
// fields those documents describe, with the "unknown / always zero" words
// written as zero. The package is deliberately standard-library only and is
// used by the `go run`-able generators under probes/<slug>/gen/.
//
// Nothing here is a parser or a simulation rule; it only serialises authored
// data. The probes' data files are committed next to their generators so a
// human can copy them into a retail install without building anything.
package author
