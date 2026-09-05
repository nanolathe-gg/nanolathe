// Package vfs provides Nanolathe's logical content namespace: an overlay of
// loose directories and HPI-family archives resolved by mount order [02 §2].
//
// Mounting indexes names and metadata only. Archive payloads are read and
// decompressed when the corresponding file is opened, which keeps startup
// independent of the size of the installed game data.
package vfs
