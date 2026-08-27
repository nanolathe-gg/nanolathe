package render

import (
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

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
// The altitude of aircraft is intentionally not consulted: that unresolved
// behavior is retained as a research question [03 §5.3].
func ModelShadowEnabled(options uint32, noShadow, runtimeEnabled bool) bool {
	return runtimeEnabled && !noShadow && options&ShadowMaster != 0 && options&ShadowVehicles != 0
}

func FeatureShadowEnabled(options uint32) bool {
	return options&ShadowMaster != 0 && options&ShadowFeatures != 0
}

// ShadowScreenVertex projects a model vertex onto the sampled ground plane.
// The height contributes to the orthographic shear only; the residual water
// flag and exact aircraft-height behavior remain TODO(question) [03 §5.3].
func ShadowScreenVertex(world [3]numeric.Fixed, groundY numeric.Fixed, camX, camZ int32) (int32, int32) {
	return int32(world[0]>>16) - camX + 128,
		int32(world[2]>>16) - (int32(groundY>>16) >> 1) - camZ + 32
}

// ShadowDepthVisible is the established inclusive depth comparison. Equal
// heights coalesce rather than darkening twice [03 §5.3].
func ShadowDepthVisible(dstDepth, srcDepth, bias int32) bool { return dstDepth <= srcDepth+bias }

// ShadowDitherKeep returns the checker-stencil decision used by the dithered
// shadow family. The stencil is seeded from the 0x01010101 pattern and screen
// parity; no palette alpha is involved [03 §5.3].
func ShadowDitherKeep(x, y int32) bool { return ((x ^ y) & 1) == 0 }

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

// ShadeShadowIndex applies the supplied SHD darken row to one ground index.
// The exact row is deliberately an input because retail's row identity is a
// remaining research question [03 §5.3]; callers must not synthesize one.
func ShadeShadowIndex(tables *palette.Tables, ground byte, row int) byte {
	if tables == nil {
		return ground
	}
	if row < 0 {
		row = 0
	} else if row >= len(tables.Shade) {
		row = len(tables.Shade) - 1
	}
	// The framebuffer is still an indexed PALETTE value at this boundary.
	// SHD consumes that ground byte directly; Logical→physical conversion is
	// reserved for final presentation [03 §4.3].
	return tables.Shade[row][ground]
}
