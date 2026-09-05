// Package docs checks that the design documents cite research that exists.
//
// A citation is the only link between a unit of work and the contract it must
// implement. A dangling one sends a sub-agent to a section that is not there,
// and the usual outcome is an invented constant — the one unrecoverable
// failure mode in AGENTS.md §"The four rules". This package resolves every
// citation mechanically so that never happens silently.
//
// The four citation forms are the ones docs/ARCHITECTURE.md §"Citation
// conventions" defines: `[04 §7.2]` numbered section, `[05 "Player slot"]`
// heading text, `[08 R-AI-01 §3]` inline addendum anchor, and `[fmt tnt]`
// format document.
package docs
