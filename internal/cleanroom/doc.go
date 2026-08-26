// Package cleanroom holds the repository's clean-room compliance check.
//
// AGENTS.md forbids committing the raw analysis trail: memory addresses or
// offsets into the retail executable, decompiler-generated symbol names, and
// register-level narration. Clean-room description — what the algorithm does,
// in our own words — is what enters research/, code comments, and commit
// messages. The address-level trail belongs in /tmp/ta-decompile/notes/ so a
// later agent can re-derive a finding without re-doing the search.
//
// The check is a ratchet, not a switch. A census of the existing violations
// is recorded in baseline.go; the scanner fails when any tracked file exceeds
// its recorded count, so no new raw-forensics text can enter, and it fails
// when a count drops without the baseline being updated, so the census cannot
// silently go stale while the backlog is worked down.
package cleanroom
