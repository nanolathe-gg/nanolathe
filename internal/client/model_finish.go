package client

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// modelTextureMaterial is a curated art annotation, not retail material metadata
// (DESIGN_GPU_RENDERER §29). The reviewed stock texture sheet supplies neutral
// plate and painted camouflage; unknown art keeps its existing lighting.
func modelTextureMaterial(texture string) uint8 {
	switch strings.ToLower(texture) {
	case "metal3a", "metal3b", "metal3c", "metal3d", "graynoise1", "graynoise2", "graynoise3", "graynoise4", "graynoise5", "arm01b", "arm01c", "arm01d", "corsea5a", "corsea5b", "corsea5c", "corsea5d", "corsea6a", "corsea6b", "corsea6c", "corsea6d":
		return drawlist.ModelMaterialMetal
	case "camob2", "camob3", "camob4", "camob5", "camob6", "camoflage", "descamo2", "descamo3", "descamo4", "corcam4b", "corcam4c", "corcam4d", "corcam5c", "corcam5d", "corcam6c", "corcam6d", "camod01", "camod02", "camoe01", "camoe02", "armcam2a", "bluenoise1", "bluenoise2", "bluenoise3", "bluenoise4":
		return drawlist.ModelMaterialPaint
	}
	return drawlist.ModelMaterialDefault
}
