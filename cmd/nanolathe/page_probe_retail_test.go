//go:build retail

package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// authoredBuildPageCount counts the contiguous authored <unit>N.GUI pages an
// install ships for one definition, by probing them, so a test can compare the
// compiled page count against the files on disk rather than against itself
// [07 §9]. Retail's unit-definition page-count byte has two writers, and the
// probe is the clean-data equivalent of the first: the catalog compiler sets it
// from its own existence probe of the numbered `guis\<n>N.GUI` pages — the byte
// becomes the index of the first missing numbered page when at least one
// existed, else 1 when page 0 exists, else 0 [02 R-CAT-01 §5]. The later
// download-menu pass can only RAISE that byte, to the largest MENU number of
// any item resolving to the definition, and never lower it
// [02 R-CAT-01 §8][07 §6].
//
// It was a buildPageCount method on retailBattleHUD, with a pageCounts cache
// field beside it on the HUD. Nothing in the battle drew on either: the
// production page count comes from hud.BuilderPageCount, and this probe existed
// so one retail test could check that against the authored windows. A cache on
// the production HUD for a count production never asks for is a fixture living
// in the shipped struct, so both moved here.
func authoredBuildPageCount(h *retailBattleHUD, def *content.UnitDef) int {
	if h == nil || def == nil {
		return 0
	}
	key := strings.ToLower(def.UnitName)
	count := 0
	for page := 1; page <= 8; page++ {
		if window, _ := h.loadWindowProbe(fmt.Sprintf("%s%d", key, page)); window == nil {
			break
		}
		count++
	}
	return count
}
