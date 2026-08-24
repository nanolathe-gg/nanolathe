// Package content documents content resolution, OVR overrides, sound variants,
// and error policy [P1-12].
//
// Fail-soft vs fatal: per-reference weapon/corpse/movementclass/side/sound
// resolve to sentinel/inactive/muted not fatal; file-level MOVEINFO missing is
// fatal [P1-12][02 §1][SPEC_CONFLICTS SC2]. OVR/Compatability/TA Unit Override
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// map/definition checksums [P1-12].
//
// Sound variant SC7: gather K1.. regardless of bare [P1-12][SPEC_CONFLICTS SC7];
// stock sound.tdf authors select1 120, ok1 76, cant1 76, arrived1 63 with no bare
// [P1-12]; following spec letter would mute core voices. Alias precedence via VFS
// mount tier first-win (lexical within tier, FindFirstFileA unsorted but SC3
// shows 2/323 differ only anims/armhp1.gaf) [P1-12][SPEC_CONFLICTS SC3]; alias
// cap 255×32 B [P1-12][02 "Sound aliases"]; 5 diagnostics verbatim title
// "Parse error in .TDF File!" [P1-12][02 §4]; comment blanking preserves offsets
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// linear, duplicate keys case-variant distinct via lower-bound not last-wins [P1-12].
package content

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// in 848 exported + 3901 boundary scan — NEGATIVE-BOUNDED inert [P1-12].
// They are likely TA 3.0 original override tier or editor; VFS mount wildcards
// are rev*.GP3 flag1 → *.CCX flag1 → *.UFO flag0 → *.HPI flag0 (lexical tie-
// breaker per SC1) → CDROM flag0 [P1-12][02 §2]; "*.OVR" not in that list and no
// .ovr files exist in stock install (0) [P1-12]. Keep inert with TODO(T23).
const (
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	OVRString = "OVR" // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	CompatabilityString = "Compatability" // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TAUnitOverrideString = "TA Unit Override" // TODO(question): Historical analysis omitted; independently worded behavior is needed.
)

// TODO(T23): OVR/Compatability/TA Unit Override purpose — residual strings with
// no consumer in current scan; possibly *.OVR archive tier or editor [P1-12].
// Bounded negative 848+3901; not gating simulation.

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// ADD/XOR/ADD(i^byte)/XOR(i+byte) → 32-bit packed LSByte first [P1-12].
// It checks TNT header 64 B + plot + feature and per-unit content identity vs
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// SPEC_CONFLICTS SC5/SC7 pattern: we use sha256 over canonical bytes including
// defaults (I1) [02 §5] C12, not the retail primitive, but provenance and
// manifest retain provider identity for diagnostics [P1-12].

// Error policy per [P1-12]:
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// - corpse miss → 0xFFFF no corpse, not fatal
// - movementclass miss → nil ptr, VTOL bypass, not fatal (degraded)
// - side miss → case-sensitive mismatch rejects pick, not crash
// - soundcategory miss → index 0xFF muted, not fatal
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// - translate.tdf missing → empty map, byte-exact identity, not fatal [P1-12][02 §3]
// - panorama GAF missing → null pointer, visual absent degraded not fatal [P1-12]
// - GAMEDATA.TDF missing → not fatal SC2 [P1-12][SPEC_CONFLICTS SC2]
// - sound SC7 diverge: gather K1.. regardless bare [P1-12][SPEC_CONFLICTS SC7]
// Alias precedence: VFS mount tier first-win, lexical within tier [P1-12][SPEC_CONFLICTS SC3]
// Alias cap 255×32 B [P1-12][02 "Sound aliases"]; registration order is file order capped.
// TDF handling: comment blanking preserves offsets via blankComments [P1-12][02 §4],
// 5 diagnostics verbatim title "Parse error in .TDF File!" [P1-12][02 §4],
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// duplicate keys case-variant coexistence lower-bound not last-wins [P1-12][02 §4].
