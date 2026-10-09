package client

// The detail view's 2x art — contract D2 of DESIGN_GPU_RENDERER §14.3.
//
// The client takes one provider, installed by the command layer at battle entry
// and cleared with the terrain. It supplies a detail tile set for the terrain
// blitter and detail sprite banks parallel to the loaded feature banks. Where a
// variant is missing — an effect, a projectile, a bank the remaster did not
// cover, or no provider at all — the client doubles the loaded frame on first
// use and keeps that doubled frame for its own life. Frames are immutable after
// load, so the cache is keyed by the source frame's pointer. The provider is
// consulted only while the Enhanced executor presents (SetEnhanced): Original
// is the authored art at every scale.
//
// Everything here is presentation [I6]: no simulation phase reads the view
// scale or the art it selects.

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// DetailArt is the 2x art of DESIGN_GPU_RENDERER §14.3. Tiles is one 64x64
// index tile per Terrain.TileSet entry in the same order (nil = none). Banks
// maps a feature bank's lowercase filename (no path, no .gaf) to a bank
// parallel to the loaded one: same entry names, same frame counts, frames at
// 2x; a nil Frame slot means "double the loaded frame yourself". Chrome maps
// an interface bank's lowercase logical path to its 2x chrome remaster, laid
// out the same way (DESIGN_GPU_RENDERER §14.9).
type DetailArt struct {
	Tiles  [][detailTilePixels]byte
	Banks  map[string]*formats.GAF
	Chrome map[string]*formats.GAF
}

// SetDetailArt installs the detail-art provider, or clears it when art is nil.
// Installing rebuilds the frame-variant index from every feature bank already
// loaded; banks loaded afterwards are indexed as they load.
func (c *Client) SetDetailArt(art *DetailArt) {
	if c == nil {
		return
	}
	c.detailArt = art
	// Re-index provider variants; nearest-doubled fallbacks are provider-independent.
	c.detailFrames = nil
	c.chromeFrames = nil
	if art == nil {
		// Teardown: the next battle's HUD registers its own banks.
		c.chromeBanks = nil
		return
	}
	for _, registered := range c.chromeBanks {
		c.indexChromeBank(registered.path, registered.bank)
	}
	c.detailFrames = map[*formats.GAFFrame]*formats.GAFFrame{}
	for name, bank := range c.featureGAFs { // presentation cache; no sim order [I1]
		c.indexDetailBank(name, bank)
	}
}

// SetEnhanced records whether the Enhanced (modern) executor is presenting.
// The synthesized 2x art is an Enhanced feature: Original (classic) draws the
// authored tiles and frames at every scale, nearest-doubled at the detail
// scale, whether or not a provider is installed (DESIGN_GPU_RENDERER §14.3).
// The adapter sets it on each executor switch and the capture and benchmark
// routes set it for the executor they drive.
func (c *Client) SetEnhanced(enhanced bool) {
	if c == nil {
		return
	}
	if c.enhanced != enhanced {
		c.resetTrails()
	}
	c.enhanced = enhanced
}

// Enhanced reports whether the provider is consulted (see SetEnhanced).
func (c *Client) Enhanced() bool {
	return c != nil && c.enhanced
}

// DetailArt returns the installed provider, or nil.
func (c *Client) DetailArt() *DetailArt {
	if c == nil {
		return nil
	}
	return c.detailArt
}

// detailTiles returns the detail tile set for the loaded terrain at the
// current view scale, or nil when the scale is native, there is no provider
// or its tile set does not match the loaded one. A short or absent tile set
// is not an error: the terrain blitter resamples the 32x32 tiles it already
// has (DESIGN_GPU_RENDERER §14.2). At 2x the set is the provider's 64x64 tiles.
func (c *Client) detailTiles() [][detailTilePixels]byte {
	if c == nil || !c.enhanced || c.detailArt == nil || c.terrain == nil {
		return nil
	}
	tiles := c.detailArt.Tiles
	if len(tiles) == 0 || len(tiles) != len(c.terrain.TileSet) {
		return nil
	}
	if c.viewScale() == camera.ViewScaleDetail {
		return tiles
	}
	return nil
}

// indexDetailBank records the 2x variant of every frame of one loaded bank that
// the provider covers, matched by entry NAME and frame INDEX rather than by
// content, so the remastered bank and the loaded bank need not share pointers
// (DESIGN_GPU_RENDERER §14.3). A nil variant slot is left unmapped, which is
// what routes that frame to the doubled fallback.
func (c *Client) indexDetailBank(name string, bank *formats.GAF) {
	if c == nil || c.detailArt == nil || bank == nil {
		return
	}
	detail, ok := c.detailArt.Banks[strings.ToLower(strings.TrimSpace(name))]
	if !ok || detail == nil {
		return
	}
	if c.detailFrames == nil {
		c.detailFrames = map[*formats.GAFFrame]*formats.GAFFrame{}
	}
	for i := range bank.Entries {
		entry := &bank.Entries[i]
		variantEntry, ok := detail.Find(entry.Name)
		if !ok || variantEntry == nil {
			continue
		}
		for j := range entry.Frames {
			if j >= len(variantEntry.Frames) {
				break
			}
			source := entry.Frames[j].Frame
			variant := variantEntry.Frames[j].Frame
			if source == nil || variant == nil {
				continue
			}
			c.detailFrames[source] = variant
		}
	}
}

type chromeBank struct {
	path string
	bank *formats.GAF
}

// RegisterChromeBank records an interface bank the HUD draws from, under its
// logical path, so its frames can take the provider's 2x chrome remaster. One
// path may have several loaded copies: with a menu shell the HUD's buttons
// take their art from the shell's own commongui, not the HUD's.
func (c *Client) RegisterChromeBank(path string, bank *formats.GAF) {
	if c == nil || bank == nil {
		return
	}
	for _, registered := range c.chromeBanks {
		if registered.bank == bank {
			return
		}
	}
	path = strings.ToLower(strings.TrimSpace(path))
	c.chromeBanks = append(c.chromeBanks, chromeBank{path, bank})
	c.indexChromeBank(path, bank)
}

// indexChromeBank pairs a registered bank's frames with the provider's
// remaster by entry name and frame index, as indexDetailBank does.
func (c *Client) indexChromeBank(path string, bank *formats.GAF) {
	if c.detailArt == nil {
		return
	}
	remaster := c.detailArt.Chrome[path]
	if remaster == nil {
		return
	}
	if c.chromeFrames == nil {
		c.chromeFrames = map[*formats.GAFFrame]*formats.GAFFrame{}
	}
	// The remaster is built entry for entry, so pair by index: a bank may
	// repeat an entry name, which Find would resolve to the first.
	if len(remaster.Entries) != len(bank.Entries) {
		return
	}
	for i := range bank.Entries {
		entry, variants := &bank.Entries[i], &remaster.Entries[i]
		if !strings.EqualFold(entry.Name, variants.Name) {
			continue
		}
		for j := range min(len(entry.Frames), len(variants.Frames)) {
			if source, variant := entry.Frames[j].Frame, variants.Frames[j].Frame; source != nil && variant != nil {
				c.chromeFrames[source] = variant
			}
		}
	}
}

// chromeDetail is f's 2x chrome remaster while f is recorded inside a 2x
// chrome region under the Enhanced executor, else nil.
func (c *Client) chromeDetail(f *formats.GAFFrame) *formats.GAFFrame {
	if f == nil || !c.enhanced || !c.chrome.recorded || c.chrome.scale != 2 {
		return nil
	}
	return c.chromeFrames[f]
}

// viewFrame resolves the frame a world-space sprite draws at the current view
// scale (DESIGN_GPU_RENDERER §14.2, §14.3). At the native scale it is the
// identity, so nothing composed at scale 1 changes. At the detail scale it is
// the provider's 2x variant when one exists, and otherwise the nearest-doubled
// frame, built once and kept for the client's life.
//
// The variant carries its own scaled Width/Height and scaled authored anchor
// offsets, so every placement contract downstream — the anchor blit's
// subtraction, drawFeature's own subtraction, the composite leaf walk — lands
// on the scaled pixel grid without a second scale term.
func (c *Client) viewFrame(f *formats.GAFFrame) *formats.GAFFrame {
	if c == nil || f == nil || c.cam == nil {
		return f
	}
	scale := c.cam.EffectiveScale()
	if scale.Native() {
		return f
	}
	var detail *formats.GAFFrame
	if c.enhanced {
		detail = c.detailFrames[f]
	}
	if detail != nil {
		return detail
	}
	if f.Transient {
		return c.transientVariant(f)
	}
	return cachedVariant(&c.doubledFrames, f, func() *formats.GAFFrame { return f.Doubled() })
}

// cachedVariant returns the cached variant of f in one variant map, building
// and recording it on first use. The maps are looked up, never ranged [I1].
func cachedVariant(cache *map[*formats.GAFFrame]*formats.GAFFrame, f *formats.GAFFrame, build func() *formats.GAFFrame) *formats.GAFFrame {
	if variant, ok := (*cache)[f]; ok && variant != nil {
		return variant
	}
	if *cache == nil {
		*cache = map[*formats.GAFFrame]*formats.GAFFrame{}
	}
	variant := build()
	(*cache)[f] = variant
	return variant
}

// viewScale is the presentation view scale every world-space emission site
// scales its authored screen offsets and extents by, through ViewScale.Px
// (DESIGN_GPU_RENDERER §14.2). Positions need no such term — they come
// through camera.WorldToScreen, which applies the scale itself.
func (c *Client) viewScale() camera.ViewScale {
	if c == nil || c.cam == nil {
		return camera.ViewScaleNative
	}
	return c.cam.EffectiveScale()
}
