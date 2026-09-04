package render

import "github.com/nanolathe/nanolathe/internal/sim/numeric"

// Model-shadow geometry and gates, kept in the presentation package as the
// shared statement of the contract. The live shadow pass is the client's, and
// today it implements the structure branch only; nothing in this file has a
// non-test caller, so a correction here changes no pixel until the mobile and
// Digger silhouette branches of [03 R-REN-03D §6] are wired.

// Visual option bits used by the model and feature shadow gates [03 §5.3].
const (
	ShadowAntiAlias uint32 = 1 << 1
	ShadowMaster    uint32 = 1 << 2
	ShadowVehicles  uint32 = 1 << 3
	ShadowFeatures  uint32 = 1 << 4
	ShadowShading   uint32 = 1 << 5
	ShadowDitherFog uint32 = 1 << 6
)

// ModelShadowEnabled applies the global gates and per-instance suppression.
//
// The comment here used to say "the altitude of aircraft is intentionally not
// consulted: that unresolved behavior is retained as a research question". The
// omission is right but the reason was wrong, and it is no longer a question: a
// unit's own height never enters any model-shadow gate or placement. What makes
// a flying unit's shadow behave like one is the placement of [03 §5.3] — the
// body is sheared by the unit's own height while the shadow is sheared by the
// terrain height beneath it, so the two separate on screen as the unit climbs.
// The silhouette is copied at 1:1, so an aircraft's shadow neither shrinks nor
// fades with altitude.
//
// This gate is the master-plus-vehicle pair only. The mobile and Digger
// silhouette branches additionally reject `canhover` and `floater`, and the
// structure branch tests none of the three; that branch selection is the live
// client shadow pass's job [03 R-REN-03D §1].
func ModelShadowEnabled(options uint32, noShadow, runtimeEnabled bool) bool {
	return runtimeEnabled && !noShadow && options&ShadowMaster != 0 && options&ShadowVehicles != 0
}

func FeatureShadowEnabled(options uint32) bool {
	return options&ShadowMaster != 0 && options&ShadowFeatures != 0
}

// ShadowScreenVertex places a model shadow on screen: the sampled ground height
// under the subject supplies the half-height shear, and the whole shadow sits
// five pixels right of the body [03 §5.3][03 R-REN-03D §3].
//
// This previously omitted the five-pixel offset and carried "the residual water
// flag and exact aircraft-height behavior remain TODO(question)". Both are
// answered and the marker is retired.
//
// There is no water flag. What the marker was reaching for is the mobile
// silhouette branch's waterline erase, which is computed, not authored: with
// `t = seaLevel - trunc(unitY)`, a subject with `t > 0` is partly submerged and
// every pixel whose height key is at or below `t + 50` is erased before the
// blit, so only the above-water hull casts a shadow. The same threshold shape
// drives the body image's own waterline pass, where the local player's units
// are blue-tinted instead of erased [03 R-REN-03A §8]. That erase acts on the
// key plane, not on this projection, so it is the composing pass's business and
// not this function's.
//
// Aircraft height enters nowhere. The shadow's shear term is the terrain height
// beneath the unit; the body's is the unit's own height. That single difference
// is the whole altitude behavior — no scaling, no fade, no altitude gate.
func ShadowScreenVertex(world [3]numeric.Fixed, groundY numeric.Fixed, camX, camZ int32) (int32, int32) {
	// [03 §5.3] screenX = trunc(worldX - camX) + 133, i.e. the body's +128 plus
	// the shadow's own five pixels; screenY = trunc(worldZ - camZ) - (ground >> 1) + 32.
	return int32(world[0]>>16) - camX + 133,
		int32(world[2]>>16) - (int32(groundY>>16) >> 1) - camZ + 32
}

// ShadowDepthVisible is the non-strict key admission the model composer applies
// per span: a pixel is written when the stored key is less than or equal to the
// incoming one, so the highest face wins and equal keys go to the later-drawn
// face [03 R-REN-03A §2].
//
// The comment here previously called this "the established inclusive depth
// comparison … equal heights coalesce rather than darkening twice [03 §5.3]",
// attributing it to the shadow blitter. That was the misattribution [03 §5.3]
// itself records under "Correction": the shadow blitter performs no depth test
// at all — it blends every non-transparent source pixel through ALP
// [03 R-REN-03D §4]. The comparison is real, but it belongs to the body/shadow
// image rasterization, not to the blit.
func ShadowDepthVisible(dstDepth, srcDepth, bias int32) bool { return dstDepth <= srcDepth+bias }

// ShadowClipInclusive intersects an integer shadow rectangle with the target
// dimensions. The returned bounds are inclusive, matching the sprite and
// model shadow blitters [03 §5.3].
func ShadowClipInclusive(minX, minY, maxX, maxY, width, height int32) (int32, int32, int32, int32, bool) {
	if width <= 0 || height <= 0 {
		return 0, 0, -1, -1, false
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= width {
		maxX = width - 1
	}
	if maxY >= height {
		maxY = height - 1
	}
	return minX, minY, maxX, maxY, minX <= maxX && minY <= maxY
}

// Two helpers that stood here have been removed rather than repaired, because
// the behaviors they modelled are ones retail does not have and leaving them
// exported invites a future caller to reintroduce them:
//
//   - `ShadowDitherKeep`, a checker stencil "seeded from the 0x01010101 pattern
//     and screen parity". No shadow path dithers. The 0x01010101 fill is the
//     composition image's background prefill — the transparent index 1 written
//     four bytes at a time [03 R-REN-03A §1].
//   - `ShadeShadowIndex`, an SHD darken row applied per ground pixel, with the
//     row left as a caller-supplied input pending research. There is no row to
//     find: every model shadow pixel is palette index 0 and the blitter resolves
//     `dst = ALP[0*256 + dst]`, snapping each ground pixel halfway to black. The
//     SHD darken that the old text was chasing belongs to the submerged-hull
//     tint, which itself reads a 256-entry BLUE TABLE and not SHD
//     [03 §5.3 "Correction"][03 R-REN-03A §8][03 R-REN-03D §4].
//
// The live tinted blit is the client shadow pass's `tintedCommit`.
