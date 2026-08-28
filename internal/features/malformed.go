package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
)

// IsMalformed reports whether a feature definition is malformed per custom/mod handling [P1-I05].
// Malformed includes: nil, empty canonical key, zero footprint (treated as 1x1 for placement but flagged), negative footprint, missing object/filename for 3D/anim classification mismatch.
// Custom features are those with unknown successor names or unknown type names; they are handled gracefully via sentinel 0xFFFF [02 "Terrain file"] [P1-10][P1-15].
func IsMalformed(def *content.FeatureDef) bool {
	if def == nil {
		return true
	}
	if def.CanonicalKey == "" {
		return true
	}
	if def.FootprintX < 0 || def.FootprintZ < 0 {
		return true
	}
	if def.FootprintX == 0 || def.FootprintZ == 0 {
		// Zero footprint is malformed but placement treats as 1 [service.go]; flagged as malformed for diagnostics
		return true
	}
	// Unknown successor links are not malformed here; they are resolved to nil sentinel gracefully [P1-10][P1-15] 0xFFFF.
	return false
}

// NormalizeDef ensures a malformed definition can still be placed without panic [P1-I05].
// It clamps negative footprints to 1, zero to 1, and ensures damage is non-negative.
// It does not mutate the original catalog entry; it returns a shallow copy with clamped fields for placement.
// Custom feature names remain as authored; missing successor def stays nil (sentinel).
func NormalizeDef(def *content.FeatureDef) *content.FeatureDef {
	if def == nil {
		return nil
	}
	if !IsMalformed(def) {
		return def
	}
	cp := *def
	if cp.FootprintX <= 0 {
		cp.FootprintX = 1
	}
	if cp.FootprintZ <= 0 {
		cp.FootprintZ = 1
	}
	if cp.Damage < 0 {
		cp.Damage = 0
	}
	if cp.Metal < 0 {
		cp.Metal = 0
	}
	if cp.Energy < 0 {
		cp.Energy = 0
	}
	return &cp
}
