package palette

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Tables holds the indexed renderer palettes and blend/lighting tables.
//
// Byte layouts are raw tables identified by size [fmt pal]; none has a
// header. Retail ships 1024-byte PAL files (256×{R,G,B,0}) [03 §4.3];
// 768-byte RGB-triplet community palettes are accepted by size.
//
// LHT is brighten-only: row 0 near-identity (242 of 256), row 31 the
// brightest halo (+51.51 mean). It is the explosion and muzzle flash ground
// halo table [03 §4.3.1] [fmt pal]. SHD is the full signed ramp that also
// darkens. Keep the two tables distinct — the flash halo must use LHT, not
// the top end of SHD, even though their bright ends overlap at mid granularity.
type Tables struct {
	Base [256][4]byte // PALETTE.PAL [03 §4.3] [fmt pal]
	// GUI is the authored GUIPAL.PAL semantic color-field table. Retail's
	// indexed display still resolves through Base (PALETTE.PAL); frontend setup
	// uses GUI only when resolving a .GUI color field, never for image pixels.
	GUI   [256][4]byte
	Alpha [65536]byte   // PALETTE.ALP 256×256 nearest-color blend [fmt pal]
	Light [8192]byte    // PALETTE.LHT 32×256 brightening [03 §4.3.1] [fmt pal]
	Shade [32][256]byte // PALETTE.SHD 32×256 shading/darkening [03 §4.3.2] [fmt pal]
	// Logical is the 256-byte logical→physical lookup retail builds at GUI
	// bootstrap: entry i is the PALETTE.PAL index nearest GUIPAL.PAL[i]
	// [03 §4.3]. BuildLogicalMap fills it; Load calls that. It resolves
	// semantic colour entries only, never image bytes — see the package
	// comment and BuildLogicalMap.
	Logical [256]byte
	// Gray is the retail "GRAY TABLE": a 256→256 palette LUT mapping each
	// palette index to the palette entry nearest its grayscale average. Retail
	// builds it at palette install into a named shared block and applies it to
	// the screen for fogged-but-explored tiles, desaturating terrain while
	// preserving texture [03 §3.3].
	// It holds physical (Base) indices on both sides; apply after the
	// logical→physical lookup when the Logical map is animated.
	Gray [256]byte
	// Blue is the retail "BLUE TABLE": the fifth and last table the renderer
	// installs, allocated beside the gray table at session initialisation and
	// built from PALETTE.PAL by the same nearest-colour search. Its one and
	// only consumer is the submerged-hull recolour of the model waterline
	// pass — the blue cast retail puts on the part of a unit below the water
	// surface [03 R-WATER-01 §2][03 R-REN-03A §8].
	// Physical (Base) indices on both sides, like Gray.
	Blue [256]byte
}

// Load loads all palette tables from the VFS.
//
// Logical paths are the retail install layout under palettes/ [fmt pal]:
//
//	palettes/palette.pal  (1024 B, 256×4) [03 §4.3]
//	palettes/guipal.pal   (1024 B)
//	palettes/palette.alp  (65536 B, 256×256) [fmt pal]
//	palettes/palette.lht  (8192 B, 32×256) [fmt pal]
//	palettes/palette.shd  (8192 B, 32×256) [03 §4.3]
//
// The 256-byte logical→physical lookup is not a file on disk. Retail installs
// PALETTE.PAL as the display palette and then, at GUI bootstrap, builds the
// lookup by matching the authored GUIPAL.PAL entries into it [03 §4.3]. Load
// is Nanolathe's equivalent of that install-then-bootstrap pair: it is the
// only point that holds both inputs, and nothing reads the tables before it
// returns. The identity fill below is the pre-bootstrap state; BuildLogicalMap
// replaces it once both palettes are in hand.
func Load(fs vfs.FSOps) (*Tables, error) {
	t := &Tables{}
	// Pre-bootstrap state: identity until the GUI bootstrap builds the real
	// map below [03 §4.3].
	for i := 0; i < 256; i++ {
		t.Logical[i] = byte(i)
	}
	baseRecovered, err := loadPAL(fs, "palettes/palette.pal", &t.Base)
	if err != nil {
		return nil, err
	}
	guiRecovered, err := loadPAL(fs, "palettes/guipal.pal", &t.GUI)
	if err != nil {
		return nil, err
	}
	// Recovery invalidates all three derived files [02 R-MALF-01 §9]. This
	// loader rebuilds in memory, keeping the authored installation read-only.
	recovered := baseRecovered || guiRecovered
	if err := loadOrBuildTable(fs, "palettes/palette.alp", t.Alpha[:], recovered, func() { buildAlphaTable(t) }); err != nil {
		return nil, err
	}
	if err := loadOrBuildTable(fs, "palettes/palette.lht", t.Light[:], recovered, func() { buildLightTable(t) }); err != nil {
		return nil, err
	}
	var shade [8192]byte
	if err := loadOrBuildTable(fs, "palettes/palette.shd", shade[:], recovered, func() {
		buildShadeTable(t)
		for row := range t.Shade {
			copy(shade[row*256:(row+1)*256], t.Shade[row][:])
		}
	}); err != nil {
		return nil, err
	}
	for row := range t.Shade {
		copy(t.Shade[row][:], shade[row*256:(row+1)*256])
	}
	buildGrayTable(t)
	buildBlueTable(t)
	t.BuildLogicalMap() // GUI bootstrap [03 §4.3]
	return t, nil
}

// BuildLogicalMap builds the 256-byte logical→physical lookup [03 §4.3].
//
// Retail's GUI bootstrap copies the 256 four-byte GUIPAL.PAL entries into the
// window's GUI palette record, then compares each entry's RGB triple with all
// 256 installed PALETTE.PAL entries using the sum of absolute per-channel
// differences. The lowest distance wins; a tie keeps the lowest destination
// index, which an ascending 0..255 scan with a strict-improvement compare
// yields [03 §4.3][07 "Retail palette contract"].
//
// This matters because the two authored files order colours differently:
// PALETTE.PAL entries 10..15 are the zeroed Windows-reserved slots, while
// GUIPAL.PAL 10 is bright green, 12 bright red, 14 bright yellow and 15 white.
// Reading a logical entry straight out of PALETTE.PAL paints those black.
//
// Consumers of this map, exhaustively as research establishes them:
//   - GUI colour fields and FNT foreground/background colours
//     [07 "Retail palette contract"];
//   - the HUD health primitive's dcb[10]/dcb[14]/dcb[12] thresholds and its
//     dcb[0] outer rectangle, and the resource text colours dcb[15] (normal),
//     dcb[10] (production) and dcb[12] (consumption) [07 §6];
//   - the drag-selection rectangle (outer entry 6 or 4 under a MOBILEBUILD
//     latch, else 15; inner entry 0) and the minimap viewport cross's entry 15
//     [07 §6];
//   - the selected-unit footprint quad, logical entry 10 — "the physical byte
//     the logical-to-physical map holds for logical entry 10, the same map
//     beams use" [03 R-WATER-01 §1].
//
// It is NOT applied to: GAF frame bytes, PCX backgrounds or TNT tiles, which
// are already active palette indices; the side-authored energycolor/metalcolor
// resource-bar fills and the footer's raw palette index 83, which [07 §6]
// names as raw active indices; the LHT and SHD lookups, whose source index is
// already post-map [03 §4.3.1]; and the final indexed→RGB present-time
// resolution, where "no GUI lookup is performed again"
// [07 "Retail palette contract"].
func (t *Tables) BuildLogicalMap() {
	if t == nil {
		return
	}
	for src := 0; src < 256; src++ {
		best := 0
		// Sum of absolute per-channel differences is bounded by 765, so any
		// larger sentinel is out of reach of a real candidate.
		bestDistance := 1 << 20
		for dst := 0; dst < 256; dst++ {
			dr := absPaletteDistance(t.GUI[src][0], t.Base[dst][0])
			dg := absPaletteDistance(t.GUI[src][1], t.Base[dst][1])
			db := absPaletteDistance(t.GUI[src][2], t.Base[dst][2])
			// Strict improvement over an ascending scan keeps the lowest
			// destination index on a tie [03 §4.3].
			if distance := dr + dg + db; distance < bestDistance {
				bestDistance = distance
				best = dst
			}
		}
		t.Logical[src] = byte(best)
	}
}

// buildGrayTable constructs the retail gray-table LUT [03 §4.3.3]. It prepares
// one sum-sorted view of the palette, then for each index computes the floored
// average of R, G, and B and finds the nearest palette entry to that gray.
//
// The sorted view is what makes the search's early-out legal: it walks sort
// positions, skipping while the sum is below targetSum-40 and
// BREAKING at the first sum above targetSum+40. Walking raw palette order
// instead terminates the scan almost immediately on TA's unsorted PALETTE.PAL
// and leaves the fogged fringe remapped to arbitrary hues — red and yellow
// where retail shows grey.
func buildGrayTable(t *Tables) {
	sums, perm := sortPaletteBySum(t)
	for i := 0; i < 256; i++ {
		e := t.Base[i]
		avg := (int(e[0]) + int(e[1]) + int(e[2])) / 3
		t.Gray[i] = nearestBySum(t, sums, perm, avg, avg, avg)
	}
}

// buildBlueTable constructs the retail blue-table LUT [03 R-WATER-01 §2]. It is
// the gray table's build with one difference: the target colour is not the
// entry's own grey but the entry halved and pushed toward blue —
// (r>>1, g>>1, (b>>1)+50) — so every palette entry resolves to the nearest
// darker, bluer entry. That is the whole of retail's submerged tint: the
// waterline pass rewrites each below-surface pixel through this table, which is
// why a submarine reads as a blue silhouette rather than a shaded hull.
//
// Retail guards the blue channel with `(b>>1)+60 < 256 else 255`. The guard can
// never fire — (255>>1)+60 is 187 — so the target is exactly as written and the
// 255 arm is dead. The +50 is the target, the +60 only the guard's test; they
// are deliberately different numbers.
func buildBlueTable(t *Tables) {
	sums, perm := sortPaletteBySum(t)
	for i := 0; i < 256; i++ {
		e := t.Base[i]
		t.Blue[i] = nearestBySum(t, sums, perm, int(e[0])>>1, int(e[1])>>1, int(e[2])>>1+50)
	}
}

// sortPaletteBySum seeds sums[i] = r+g+b and
// perm[i] = i, then run retail's exchange sort — for every i, compare against
// every later j and swap both arrays when sums[i] > sums[j]. The swap is on a
// strict greater-than, and the sort is reproduced loop-for-loop rather than
// delegated to sort.Slice so the resulting permutation matches retail's for
// equal sums.
func sortPaletteBySum(t *Tables) (sums [256]int32, perm [256]uint8) {
	for i := 0; i < 256; i++ {
		e := t.Base[i]
		sums[i] = int32(e[0]) + int32(e[1]) + int32(e[2])
		perm[i] = uint8(i)
	}
	for i := 0; i < 256; i++ {
		for j := i + 1; j < 256; j++ {
			if sums[i] > sums[j] {
				sums[i], sums[j] = sums[j], sums[i]
				perm[i], perm[j] = perm[j], perm[i]
			}
		}
	}
	return sums, perm
}

// nearestBySum scans sort positions in ascending sum
// order, skip below targetSum-40, stop above targetSum+40, and keep the
// strictly smaller squared RGB distance so ties keep the earliest position.
// When no candidate fell inside the window retail keeps the loop counter at
// exit — the position that broke the scan, or 256 masked to 0 when the scan
// ran to completion [03 §4.3.3].
// The returned value is perm[best]: a physical palette index, not a position.
func nearestBySum(t *Tables, sums [256]int32, perm [256]uint8, r, g, b int) uint8 {
	targetSum := int32(r + g + b)
	const window = 40
	best := 0
	bestDist := int32(0x3b9aca00) // retail's established sentinel [03 §4.3.3]
	found := false
	position := 256
	for k := 0; k < 256; k++ {
		s := sums[k]
		if s < targetSum-window {
			continue // below the bounded sum window [03 §4.3.3]
		}
		if s > targetSum+window {
			position = k // stop above the bounded sum window [03 §4.3.3]
			break
		}
		e := t.Base[perm[k]]
		dr := int32(e[0]) - int32(r)
		dg := int32(e[1]) - int32(g)
		db := int32(e[2]) - int32(b)
		if dist := dr*dr + dg*dg + db*db; dist < bestDist { // strict improvement [03 §4.3.3]
			bestDist = dist
			best = k
			found = true
		}
	}
	if !found {
		best = position & 0xFF
	}
	return perm[best]
}

// RGBA resolves a final indexed pixel to RGBA at present time.
//
// The index is already a physical PALETTE.PAL index, so the lookup is Base
// alone: "every final indexed pixel is resolved to RGB only at present time
// through PALETTE.PAL", and "no GUI lookup is performed again during
// indexed-to-RGB presentation" [03 §4.3][07 "Retail palette contract"].
// Callers that hold a *semantic* colour entry resolve it through
// GUIColor/Logical before writing it into the surface, never here.
//
// The fourth PAL byte is observed zero in every retail entry [fmt pal] and
// matches a Windows PALETTEENTRY; it is not an alpha. Returned alpha is always
// 255 (opaque). Model lighting selects an SHD row before this lookup
// [03 §4.3.2]; the explosion halo selects an LHT row [03 §4.3.1].
func (t *Tables) RGBA(index byte) (r, g, b, a uint8) {
	if t == nil {
		return 0, 0, 0, 255
	}
	e := t.Base[index]
	return e[0], e[1], e[2], 255
}

// LightLookup brightens a palette index through the LHT table.
//
// level 0 .. 31, index 0 .. 255. The index is "the source palette index after
// the logical-to-physical map" [03 §4.3.1] — that is, it is already physical,
// because the halo reads back pixels the composer has already written. The map
// is not applied again here. The table is the 8192-byte PALETTE.LHT file
// [fmt pal] [03 §4.3.1]. Result is again a palette index, not an RGB triple.
// Level is clamped to 0 .. 31. Use for the explosion and muzzle flash ground
// halo; no other retail consumer is established.
func (t *Tables) LightLookup(level int, index byte) byte {
	if t == nil {
		return index
	}
	if level < 0 {
		level = 0
	} else if level > 31 {
		level = 31
	}
	return t.Light[level*256+int(index)]
}

// ShadeLookup maps a palette index through the SHD table at the given row
// (0 .. 31, middle row 15 is near-identity) [03 §4.3.2]. The index is a
// texture/screen byte and is therefore already physical; the logical→physical
// map is a semantic-colour route and is not applied here [03 §4.3].
func (t *Tables) ShadeLookup(row int, index byte) byte {
	if t == nil {
		return index
	}
	if row < 0 {
		row = 0
	} else if row > 31 {
		row = 31
	}
	return t.Shade[row][index]
}

// GUIToBase returns the retail frontend's semantic color-field map. There is
// only one such map and it is Logical: retail's GUI bootstrap builds the
// logical→physical lookup out of GUIPAL.PAL precisely so GUI colour fields can
// be resolved into the installed PALETTE.PAL [03 §4.3]
// [07 "Retail palette contract"]. Callers on a hand-built Tables must call
// BuildLogicalMap first; Load does.
//
// This map must not be applied to GAF/PCX/TNT pixel bytes, which already are
// active palette indices [07 "Retail palette contract"].
func (t *Tables) GUIToBase() [256]byte {
	if t == nil {
		return [256]byte{}
	}
	return t.Logical
}

// GUIColor resolves one GUI file color field to an active PALETTE.PAL index.
// It is the semantic-color counterpart to RGBA: callers use it before writing
// FNT glyphs or direct GUI primitives, never while presenting image pixels.
func (t *Tables) GUIColor(source byte) byte {
	if t == nil {
		return source
	}
	return t.Logical[source]
}

func absPaletteDistance(a, b byte) int {
	if a >= b {
		return int(a - b)
	}
	return int(b - a)
}

func paletteLoadError(files vfs.FSOps, name, expected string, err error) error {
	var providers []string
	if source, ok := files.(interface{ Providers() []vfs.ProviderInfo }); ok {
		for _, provider := range source.Providers() {
			providers = append(providers, provider.ID)
		}
	}
	return fmt.Errorf("nanolathe: palette load failed: logical path %s, providers searched [%s], expected %s: %w", name, strings.Join(providers, ", "), expected, err)
}

func missingPaletteFile(err error) bool {
	return errors.Is(err, vfs.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

func loadPAL(files vfs.FSOps, name string, dst *[256][4]byte) (bool, error) {
	data, err := files.ReadFileLimit(name, 1<<20)
	recovered := missingPaletteFile(err) || (err == nil && len(data) == 0)
	var pal *formats.Palette
	if recovered {
		pcxName := strings.TrimSuffix(name, path.Ext(name)) + ".pcx"
		pcx, pcxErr := formats.LoadPCXFile(files, pcxName)
		if pcxErr != nil {
			return false, paletteLoadError(files, pcxName, "PCX recovery palette", pcxErr)
		}
		pal = &formats.Palette{Colors: pcx.Palette}
	} else {
		if err != nil {
			return false, paletteLoadError(files, name, "PAL palette", err)
		}
		pal, err = formats.LoadPAL(data)
		if err != nil {
			return false, paletteLoadError(files, name, "PAL palette", err)
		}
	}
	for i, c := range pal.Colors {
		dst[i] = [4]byte{c.R, c.G, c.B, c.A}
	}
	return recovered, nil
}

func loadOrBuildTable(files vfs.FSOps, name string, dst []byte, invalidated bool, build func()) error {
	if invalidated {
		build()
		return nil
	}
	data, err := files.ReadFileLimit(name, 1<<20)
	if missingPaletteFile(err) || (err == nil && len(data) == 0) {
		build()
		return nil
	}
	if err != nil {
		return paletteLoadError(files, name, "derived palette table", err)
	}
	if len(data) != len(dst) {
		return paletteLoadError(files, name, fmt.Sprintf("%d-byte palette table", len(dst)), fmt.Errorf("got %d bytes", len(data)))
	}
	copy(dst, data)
	return nil
}
