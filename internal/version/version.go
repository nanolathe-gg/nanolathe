// Package version carries the engine's build identity. It is deliberately a
// name, not a content hash: retail's scheduler box stores a profile identity
// and recomputes content identity separately [01 §3.1].
package version

// Profile names this engine build. Content identity lives in the VFS manifest
// hash and the content catalog hash, never here.
const Profile = "nanolathe-1.0"

// ProfileID returns the profile name. It exists as a function so callers that
// log identity do not accidentally embed a constant at compile time in a way
// that hides a future change.
func ProfileID() string { return Profile }
