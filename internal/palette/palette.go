// Package palette loads the retail palette tables and performs logical →
// physical lookups at present time.
//
// [03 §4.3] defines the five file-backed tables: PALETTE.PAL 1024 B
// (256×4 RGB+pad), GUIPAL.PAL, ALP 64 K (256×256), LHT 8 K (32×256),
// SHD 8 K (32×256), plus a 256-byte logical→physical lookup that the
// palette-install helper maintains. [fmt pal] gives the raw layouts
// (no header, size-identified) and the per-entry PAL stride.
//
// C7 requires that every indexed-pixel → RGBA conversion go through the
// 256-byte Logical table at present time, so palette animation stays
// possible, and that model lighting select an SHD row and the explosion flash
// halo select an LHT row [03 §4.3.1].
package palette

import (
	"fmt"

	"github.com/nanolathe/nanolathe/vfs"
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
	GUI     [256][4]byte
	Alpha   [65536]byte   // PALETTE.ALP 256×256 nearest-color blend [fmt pal]
	Light   [8192]byte    // PALETTE.LHT 32×256 brightening [03 §4.3.1] [fmt pal]
	Shade   [32][256]byte // PALETTE.SHD 32×256 shading/darkening [03 §4.3.2] [fmt pal]
	Logical [256]byte     // logical → physical 256-byte lookup [03 §4.3] (C7)
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
// The 256-byte logical→physical lookup is not a file on disk; retail
// maintains it as the LOGPALETTE mapping [03 §4.3]. It is initialized to
// identity (i → i) so palette animation can mutate it later. All indexed
// → RGBA conversion must go through it at present time (C7).
func Load(fs vfs.FSOps) (*Tables, error) {
	t := &Tables{}
	// Identity logical→physical until animated [03 §4.3].
	for i := 0; i < 256; i++ {
		t.Logical[i] = byte(i)
	}
	if err := loadPAL(fs, "palettes/palette.pal", &t.Base); err != nil {
		return nil, err
	}
	if err := loadPAL(fs, "palettes/guipal.pal", &t.GUI); err != nil {
		return nil, err
	}
	if err := loadRaw(fs, "palettes/palette.alp", t.Alpha[:], 65536); err != nil {
		return nil, err
	}
	if err := loadRaw(fs, "palettes/palette.lht", t.Light[:], 8192); err != nil {
		return nil, err
	}
	shd, err := loadRawTable(fs, "palettes/palette.shd", 8192)
	if err != nil {
		return nil, err
	}
	for row := 0; row < 32; row++ {
		copy(t.Shade[row][:], shd[row*256:(row+1)*256])
	}
	return t, nil
}

// RGBA resolves an indexed pixel to RGBA at present time (C7).
//
// Lookup is logical → physical through the 256-byte table [03 §4.3],
// then through Base. The fourth PAL byte is observed zero in every retail
// entry [fmt pal] and matches a Windows PALETTEENTRY; it is not an alpha.
// Returned alpha is always 255 (opaque). Model lighting selects an SHD row
// before this lookup [03 §4.3.2]; the explosion halo selects an LHT row
// [03 §4.3.1].
func (t *Tables) RGBA(index byte) (r, g, b, a uint8) {
	if t == nil {
		return 0, 0, 0, 255
	}
	phys := t.Logical[index] // C7: logical→physical
	e := t.Base[phys]
	return e[0], e[1], e[2], 255
}

// LightLookup brightens a palette index through the LHT table.
//
// level 0 .. 31, index 0 .. 255 already logical→physical. The table is the
// 8192-byte PALETTE.LHT file [fmt pal] [03 §4.3.1]. Result is again a palette
// index, not an RGB triple. Level is clamped to 0 .. 31. Use for the
// explosion and muzzle flash ground halo; no other retail consumer is
// established.
func (t *Tables) LightLookup(level int, index byte) byte {
	if t == nil {
		return index
	}
	if level < 0 {
		level = 0
	} else if level > 31 {
		level = 31
	}
	phys := t.Logical[index]
	return t.Light[level*256+int(phys)]
}

// ShadeLookup maps a palette index through the SHD table at the given row
// (0 .. 31, middle row 15 is near-identity) [03 §4.3.2].
func (t *Tables) ShadeLookup(row int, index byte) byte {
	if t == nil {
		return index
	}
	if row < 0 {
		row = 0
	} else if row > 31 {
		row = 31
	}
	phys := t.Logical[index]
	return t.Shade[row][phys]
}

// GUIToBase returns the retail frontend's semantic color-field map. GUI color
// fields are authored against GUIPAL.PAL, while the indexed display surface is
// presented through PALETTE.PAL. Retail chooses, for each source GUI entry,
// the first PALETTE entry with the smallest Manhattan RGB distance. This map
// must not be applied to GAF/PCX/TNT pixel bytes, which already are active
// palette indices [07 "Retail palette contract"].
func (t *Tables) GUIToBase() [256]byte {
	var remap [256]byte
	if t == nil {
		return remap
	}
	for src := 0; src < 256; src++ {
		best := 0
		bestDistance := int(^uint(0) >> 1)
		for dst := 0; dst < 256; dst++ {
			dr := absPaletteDistance(t.GUI[src][0], t.Base[dst][0])
			dg := absPaletteDistance(t.GUI[src][1], t.Base[dst][1])
			db := absPaletteDistance(t.GUI[src][2], t.Base[dst][2])
			distance := dr + dg + db
			// Retail's strict-lower comparison keeps the first palette entry
			// on a tie.
			if distance < bestDistance {
				bestDistance = distance
				best = dst
			}
		}
		remap[src] = byte(best)
	}
	return remap
}

// GUIColor resolves one GUI file color field to an active PALETTE.PAL index.
// It is the semantic-color counterpart to RGBA: callers use it before writing
// FNT glyphs or direct GUI primitives, never while presenting image pixels.
func (t *Tables) GUIColor(source byte) byte {
	if t == nil {
		return source
	}
	return t.GUIToBase()[source]
}

func absPaletteDistance(a, b byte) int {
	if a >= b {
		return int(a - b)
	}
	return int(b - a)
}

func loadPAL(fs vfs.FSOps, name string, dst *[256][4]byte) error {
	// PAL files are raw 768 or 1024 bytes [fmt pal]; retail ships 1024 [03 §4.3].
	data, err := fs.ReadFileLimit(name, 1<<20)
	if err != nil {
		return fmt.Errorf("palette: %s: %w", name, err)
	}
	if len(data) != 768 && len(data) != 1024 {
		return fmt.Errorf("palette: %s: expected 768 or 1024 bytes, got %d", name, len(data))
	}
	stride := 3
	if len(data) == 1024 {
		stride = 4
	}
	for i := 0; i < 256; i++ {
		dst[i][0] = data[i*stride]
		dst[i][1] = data[i*stride+1]
		dst[i][2] = data[i*stride+2]
		dst[i][3] = 255 // opaque; file pad byte is zero [fmt pal]
	}
	return nil
}

func loadRaw(fs vfs.FSOps, name string, dst []byte, want int) error {
	data, err := fs.ReadFileLimit(name, 1<<20)
	if err != nil {
		return fmt.Errorf("palette: %s: %w", name, err)
	}
	if len(data) != want {
		return fmt.Errorf("palette: %s: expected %d bytes, got %d", name, want, len(data))
	}
	copy(dst, data)
	return nil
}

func loadRawTable(fs vfs.FSOps, name string, want int) ([]byte, error) {
	data, err := fs.ReadFileLimit(name, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("palette: %s: %w", name, err)
	}
	if len(data) != want {
		return nil, fmt.Errorf("palette: %s: expected %d bytes, got %d", name, want, len(data))
	}
	out := make([]byte, want)
	copy(out, data)
	return out, nil
}
