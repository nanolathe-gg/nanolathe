// Package save reads and writes the retail HAPIBANK save container.
//
// A bank is a pool of strings plus a list of named accounts, each holding
// scalar items and numbered or named boxes [08 "Save-file organization"]. This
// package owns the container and the established box layouts — the summary,
// the player records, the unit and script records, the features — and it is
// the only save codec: there is no alternate Nanolathe format [I13].
//
// It knows nothing about a session. Projection into and out of live state
// belongs to the packages that own that state.
package save
