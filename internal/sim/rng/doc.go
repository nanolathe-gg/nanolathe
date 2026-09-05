// Package rng provides the two deterministic random streams used by the
// retail engine [01 §7.1], [01 §7.2], [01 §7.3].
//
// There are exactly two streams and they are not interchangeable:
//
//   - Simulation is the process-wide Park–Miller stream. Every authoritative
//     gameplay draw comes from it, and call order — not entity identity — is
//     what isolates consumers [01 §7.1]. It yields 31 bits per draw.
//   - CRT is the per-thread MSVCRT stream used for meteor geometry [06 §6.5],
//     screen shake [03 §5.6], audio variants [03 §8.3] and briefing wind
//     [01 §7.3]. It yields 15 bits per draw, so bounds above 32,767 need the
//     chunk-concatenation helper below.
//
// No other stream exists. Do not add per-entity, per-player or name-seeded
// substreams: they look deterministic and reproduce nothing retail does (I4).
package rng
