// Optional OVR data and the cross-reference miss policy.
// sound aliases, and content identity. Optional OVR data participates only in
// the documented definition-hash replacement path; it is not a fallback source
// for normal catalog records [02 "Content checksum"].
//
// Per-reference misses use their documented inactive, muted, or null result;
// MOVEINFO.TDF, SIDEDATA.TDF, and the gamedata directory are hard requirements,
// while translate.tdf and presentation resources are optional [02 §1][02 §5].
// Sound variants gather numbered keys even when the bare key is absent [02
// "Sound category record"][03 §8.3][docs/SPEC_CONFLICTS.md SC7]. VFS tier
// precedence and alias limits follow [02 §2] and [02 "Sound aliases"].

package content

// OVR data is addressed through units/<unit>.OVR as a HapiBank account filtered
// to TA Unit Override. The Compatability account can replace the computed
// definition hash using the decimal checksum key; no other account or item is
// probed [02 "Content checksum"]. The installed VFS does not mount .OVR as a
// provider extension, so this path is stock-inert [02 §2].
const (
	// OVRString is the optional override-file suffix [02 "Content checksum"].
	OVRString = "OVR"
	// CompatabilityString is the authored account name used by OVR banks.
	CompatabilityString = "Compatability"
	// TAUnitOverrideString selects the override account [02 "Content checksum"].
	TAUnitOverrideString = "TA Unit Override"
)

// The retail content checksum has four 8-bit accumulators: add the byte, XOR
// the byte, add (index XOR byte), and XOR (index plus byte), then pack the
// accumulators least-significant byte first [02 "Content checksum"]. Catalog
// hashing uses canonical SHA-256 bytes including defaults and preserves
// provenance separately [02 §5] [I1].

// Error policy per [02 §5]:
// - weapon references miss to record 0, the inactive sentinel;
// - corpse references miss to the no-corpse sentinel;
// - movement-class, model, side, and sound-category misses use their
//   documented null, rejected, or muted result;
// - missing MOVEINFO.TDF or SIDEDATA.TDF is fatal, while missing translate.tdf
//   leaves byte-exact identity mappings and missing GAMEDATA.TDF is not fatal
//   [02 §1][02 §3][docs/SPEC_CONFLICTS.md SC2].
// TDF comment blanking preserves offsets and duplicate sections remain
// enumerable; first-match access and case-sensitive duplicate-key handling are
// part of [02 §4].
