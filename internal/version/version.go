package version

// Profile names this engine build. Content identity lives in the VFS manifest
// hash and the content catalog hash, never here.
const Profile = "nanolathe-1.0"

// ProfileID returns the profile name. It exists as a function so callers that
// log identity do not accidentally embed a constant at compile time in a way
// that hides a future change.
func ProfileID() string { return Profile }
