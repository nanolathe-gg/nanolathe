// Package content compiles retail's authored data into immutable definitions:
// the unit, weapon, feature, movement-class, side, sound, map, build-menu and
// AI-profile families, each discovered through the VFS and linked in a second
// pass so enumeration order cannot leak into identity [02 §5].
//
// Every catalog map is keyed by CanonicalKey, the one case-folding rule, so a
// lookup can never disagree with sort order. A cross-reference that misses
// takes its documented inactive, muted or null result rather than a fallback
// record; MOVEINFO.TDF, SIDEDATA.TDF and the gamedata directory are hard
// requirements, while translate.tdf and presentation resources are optional
// [02 §1][02 §5]. Optional OVR data participates only in the documented
// definition-hash replacement path and is never a source for normal catalog
// records [02 "Content checksum"].
package content
