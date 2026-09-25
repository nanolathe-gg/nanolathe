package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// megamapGeneratedIconSize is the edge, in pixels, that a generated strategic
// icon is quantised to for the megamap. The shipped ProTA pictures are 10 to
// 18 pixels; this is a host presentation choice, not a patch value
// (DESIGN_INTERFACE_HUD_INPUT §3.15).
const megamapGeneratedIconSize = 16

// MegamapIconBank holds the megamap's indexed icon pictures. With a community
// icon configuration it holds that file's PCX art under its authored rows;
// otherwise it quantises the generated strategic vocabulary (DESIGN_GPU_RENDERER
// §18) to indexed pictures. It is immutable after loading and presentation-only
// [I6].
type MegamapIconBank struct {
	rows      []megamapIconRow
	unknown   *render.MegamapIcon
	nothing   *render.MegamapIcon
	nuke      *render.MegamapIcon
	generated *StrategicIconCatalog
	quantised map[drawlist.Rect]*render.MegamapIcon
	fallback  *render.MegamapIcon
	// Configured reports whether the pictures came from an icon configuration.
	Configured bool
	// MaxW/MaxH bound every unit picture; the hover search box uses the
	// shipped build's own 22×22 default instead.
	MaxW, MaxH int
}

type megamapIconRow struct {
	mask content.CategoryMask
	icon *render.MegamapIcon
}

// LoadMegamapIconBank builds the bank from the same resolved configuration the
// strategic icons use (§18.7 of DESIGN_GPU_RENDERER). An empty path, or a
// configuration asking for the default icons, uses the generated vocabulary.
// Any read or decode failure also falls back to it, and the error is returned
// for the host's diagnostic.
func LoadMegamapIconBank(cat *content.Catalog, configPath string, pal *palette.Tables) (*MegamapIconBank, error) {
	generated := func() *MegamapIconBank {
		return newGeneratedMegamapIconBank(NewStrategicIconCatalog(cat), pal)
	}
	configPath = strings.TrimSpace(strings.SplitN(configPath, ";", 2)[0])
	if configPath == "" {
		return generated(), nil
	}
	abs, err := filepath.Abs(filepath.FromSlash(strings.ReplaceAll(configPath, `\`, "/")))
	if err != nil {
		return generated(), megamapIconError(configPath, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return generated(), megamapIconError(abs, err)
	}
	cfg, err := parseCommunityIconConfig(abs, data, cat)
	if err != nil {
		return generated(), megamapIconError(abs, err)
	}
	if cfg.options.useDefault {
		return generated(), nil
	}
	bank := &MegamapIconBank{Configured: true}
	dir := filepath.Dir(abs)
	loaded := make(map[string]*render.MegamapIcon)
	for _, row := range cfg.rows {
		path, err := resolveCommunityIconPath(dir, row.path)
		if err != nil {
			return generated(), megamapIconError(abs, fmt.Errorf("icon %q from %s: %w", row.name, row.path, err))
		}
		icon, ok := loaded[path]
		if !ok {
			data, err := os.ReadFile(path)
			if err != nil {
				return generated(), megamapIconError(abs, fmt.Errorf("icon %q from %s: %w", row.name, path, err))
			}
			pcx, err := formats.LoadPCX(data)
			if err != nil {
				return generated(), megamapIconError(abs, fmt.Errorf("icon %q from %s: %w", row.name, path, err))
			}
			icon = megamapIconFromPCX(pcx, cfg.options)
			loaded[path] = icon
		}
		bank.MaxW, bank.MaxH = max(bank.MaxW, icon.W), max(bank.MaxH, icon.H)
		// The reserved names never take part in the category walk
		// [draw-engine-interface "Custom load and colour order"].
		switch strings.ToLower(row.name) {
		case "unknow":
			bank.unknown = icon
		case "nothing":
			bank.nothing = icon
		case "nukeicon":
			bank.nuke = icon
		default:
			bank.rows = append(bank.rows, megamapIconRow{mask: row.mask, icon: icon})
		}
	}
	return bank, nil
}

func megamapIconError(path string, err error) error {
	return fmt.Errorf("nanolathe: megamap icon configuration failed: logical path %s, providers searched [filesystem], expected a readable icon-config INI and PCX art: %w", path, err)
}

// megamapIconFromPCX classifies each authored pixel once. `TransparentColor`
// is never drawn, `FillColor` becomes the player's dot colour and
// `SelectedColor` is selection ink; every other index is drawn as authored
// [draw-engine-interface "Icon configuration"].
func megamapIconFromPCX(pcx *formats.PCX, o communityIconOptions) *render.MegamapIcon {
	w, h := int(pcx.Width), int(pcx.Height)
	icon := &render.MegamapIcon{W: w, H: h, Pix: append([]byte(nil), pcx.Pixels[:w*h]...), Role: make([]render.MegamapPixelRole, w*h), Hover: byte(o.hover), Circle: o.circleHover}
	for i, p := range icon.Pix {
		switch int(p) {
		case o.transparent:
			icon.Role[i] = render.MegamapPixelEmpty
		case o.fill:
			icon.Role[i] = render.MegamapPixelFill
		case o.selected:
			icon.Role[i] = render.MegamapPixelSelected
		default:
			icon.Role[i] = render.MegamapPixelInk
		}
	}
	return icon
}

// Unit returns the picture for an identified unit: the first authored row
// whose category contains the definition, else `unknow`. Without a
// configuration it is the generated descriptor, which keeps §18.4's disguise
// protection for commander appearances.
func (b *MegamapIconBank) Unit(defName string, defID uint16) *render.MegamapIcon {
	if b == nil {
		return nil
	}
	if b.Configured {
		for _, row := range b.rows {
			if defID != 0 && row.mask.Contains(uint32(defID)) {
				return row.icon
			}
		}
		return b.unknown
	}
	d, _ := b.generated.Lookup(defName, defID)
	if icon := b.quantised[d.Rect]; icon != nil {
		return icon
	}
	return b.fallback
}

// Nothing is the configured picture for an unidentified contact, or nil.
func (b *MegamapIconBank) Nothing() *render.MegamapIcon {
	if b == nil {
		return nil
	}
	return b.nothing
}

// Nuke is the configured `nukeicon` projectile picture, or nil.
func (b *MegamapIconBank) Nuke() *render.MegamapIcon {
	if b == nil {
		return nil
	}
	return b.nuke
}

func newGeneratedMegamapIconBank(icons *StrategicIconCatalog, pal *palette.Tables) *MegamapIconBank {
	b := &MegamapIconBank{generated: icons, quantised: make(map[drawlist.Rect]*render.MegamapIcon)}
	if icons == nil {
		return b
	}
	white, black := nearestPaletteIndex(pal, 255, 255, 255), nearestPaletteIndex(pal, 0, 0, 0)
	halo := byte(selectionQuadLogicalColor)
	if pal != nil {
		halo = pal.Logical[selectionQuadLogicalColor]
	}
	add := func(d StrategicIconDescriptor) *render.MegamapIcon {
		if d.Atlas == nil || d.Rect.W <= 0 || d.Rect.H <= 0 {
			return nil
		}
		if icon := b.quantised[d.Rect]; icon != nil {
			return icon
		}
		icon := quantiseStrategicIcon(d.Atlas, d.Rect, megamapGeneratedIconSize, white, black, halo)
		b.quantised[d.Rect] = icon
		b.MaxW, b.MaxH = max(b.MaxW, icon.W), max(b.MaxH, icon.H)
		return icon
	}
	b.fallback = add(icons.fallback)
	for _, e := range icons.entries {
		add(e.Descriptor)
	}
	return b
}

// quantiseStrategicIcon box-averages one generated mask tile to size×size and
// gives each pixel one lane: the halo lane becomes selection ink, the team
// lane the dot colour, and the white and black lanes fixed ink. Hover reuses the selection ink's
// pixels in the configuration's default hover colour.
func quantiseStrategicIcon(atlas *drawlist.MarkerAtlas, r drawlist.Rect, size int, white, black, halo byte) *render.MegamapIcon {
	icon := &render.MegamapIcon{W: size, H: size, Pix: make([]byte, size*size), Role: make([]render.MegamapPixelRole, size*size), Hover: 84}
	sw, sh := int(r.W), int(r.H)
	for y := 0; y < size; y++ {
		y0, y1 := y*sh/size, (y+1)*sh/size
		for x := 0; x < size; x++ {
			x0, x1 := x*sw/size, (x+1)*sw/size
			var sum [4]int
			n := 0
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					o := ((int(r.Y)+sy)*atlas.Width + int(r.X) + sx) * 4
					for k := 0; k < 4; k++ {
						sum[k] += int(atlas.Pixels[o+k])
					}
					n++
				}
			}
			if n == 0 {
				continue
			}
			// A lane that covers enough of the pixel claims it, in priority
			// order: black needs half the pixel, and the thin halo, team contour
			// and white glyph a quarter, so a one-pixel source stroke survives
			// the reduction.
			lane := -1
			switch {
			case sum[2]*4 >= n*255:
				lane = 2
			case sum[0]*4 >= n*255:
				lane = 0
			case sum[1]*4 >= n*255:
				lane = 1
			case sum[3]*2 >= n*255:
				lane = 3
			default:
				continue
			}
			i := y*size + x
			switch lane {
			case 0:
				icon.Role[i] = render.MegamapPixelFill
			case 1:
				icon.Role[i], icon.Pix[i] = render.MegamapPixelInk, white
			case 2:
				icon.Role[i], icon.Pix[i] = render.MegamapPixelSelected, halo
			case 3:
				icon.Role[i], icon.Pix[i] = render.MegamapPixelInk, black
			}
		}
	}
	return icon
}

func nearestPaletteIndex(pal *palette.Tables, r, g, b int) byte {
	if pal == nil {
		return 0
	}
	best, bestD := 0, 1<<30
	for i := 0; i < 256; i++ {
		c := pal.Base[i]
		dr, dg, db := int(c[0])-r, int(c[1])-g, int(c[2])-b
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	return byte(best)
}
