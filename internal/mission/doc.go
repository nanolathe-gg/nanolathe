// Package mission loads a campaign and its missions: campaign discovery and
// progression, the four mission kinds and their dispatch, schema selection,
// placement decoding, the InitialMission interpreter, and the mission-global
// state block [08 "Mission type dispatch"] [08 "Campaign discovery"]
// [04 §3.6] [02 "Mission-file diagnostics"].
//
// It produces the immutable Mission record a session enters battle with; it
// runs no ticks of its own.
package mission
