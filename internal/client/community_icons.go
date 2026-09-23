package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

const maxCommunityIconAtlasPixels = 16 << 20

type communityIconOptions struct {
	fill, transparent, selected, hover int
	useDefault, circleHover            bool
}

type communityIconRow struct {
	name, path string
	mask       content.CategoryMask
	art        communityIconArt
	reserved   bool
}

type communityIconArt struct {
	w, h                  int
	masks, hoverCircle    []byte
	rect, hoverCircleRect drawlist.Rect
	atlas                 *drawlist.MarkerAtlas
}

type communityIconConfig struct {
	source  string
	options communityIconOptions
	rows    []communityIconRow
}

// LoadStrategicIconCatalog optionally replaces generated strategic icon art
// with the ordered PCX mapping from a community icon-config INI. The returned
// catalog is always usable: an empty path, UseDefaultIcon=true, or any error
// retains NewStrategicIconCatalog's generated art. This is host presentation
// state and never mutates the content catalog or its digest
// [draw-engine-interface "Icon configuration"] [I6].
func LoadStrategicIconCatalog(cat *content.Catalog, configPath string) (*StrategicIconCatalog, error) {
	builtIn := NewStrategicIconCatalog(cat)
	configPath = strings.TrimSpace(strings.SplitN(configPath, ";", 2)[0])
	if configPath == "" {
		return builtIn, nil
	}
	abs, err := filepath.Abs(filepath.FromSlash(strings.ReplaceAll(configPath, `\`, "/")))
	if err != nil {
		return builtIn, communityIconError(configPath, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return builtIn, communityIconError(abs, err)
	}
	cfg, err := parseCommunityIconConfig(abs, data, cat)
	if err != nil {
		return builtIn, communityIconError(abs, err)
	}
	if cfg.options.useDefault {
		return builtIn, nil
	}
	if err := cfg.loadArt(); err != nil {
		return builtIn, communityIconError(abs, err)
	}
	return applyCommunityIconConfig(builtIn, cfg), nil
}

func communityIconError(path string, err error) error {
	return fmt.Errorf("nanolathe: strategic icon configuration failed: logical path %s, providers searched [filesystem], expected a readable icon-config INI and PCX art: %w", path, err)
}

func parseCommunityIconConfig(path string, data []byte, cat *content.Catalog) (*communityIconConfig, error) {
	cfg := &communityIconConfig{source: path, options: communityIconOptions{transparent: 9, selected: 89, hover: 84, useDefault: true}}
	lines := strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n")
	section := ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if parsed, ok := communityIconSection(line); ok {
			section = parsed
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
		if section == "option" {
			setCommunityIconOption(&cfg.options, key, value)
		}
	}
	// This return precedes [Icon] enumeration and every PCX open in the
	// source. In particular, arbitrary custom rows cannot fault default mode.
	if cfg.options.useDefault {
		return cfg, nil
	}
	section = ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if parsed, ok := communityIconSection(line); ok {
			section = parsed
			continue
		}
		if section != "icon" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
		if key == "" || value == "" {
			continue
		}
		row := communityIconRow{name: key, path: value}
		switch strings.ToLower(key) {
		case "nothing", "unknow", "nukeicon":
			row.reserved = true
		default:
			if cat != nil {
				row.mask, _ = cat.Category(key)
			}
		}
		cfg.rows = append(cfg.rows, row)
	}
	if len(cfg.rows) == 0 {
		return nil, fmt.Errorf("custom icon mode has no [Icon] rows")
	}
	return cfg, nil
}

func communityIconSection(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") {
		return "", false
	}
	end := strings.IndexByte(line, ']')
	if end < 1 {
		return "", false
	}
	trailing := strings.TrimSpace(line[end+1:])
	if trailing != "" && !strings.HasPrefix(trailing, ";") && !strings.HasPrefix(trailing, "#") {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(line[1:end])), true
}

func setCommunityIconOption(options *communityIconOptions, key, value string) {
	parseInt := func(dst *int, fallback int) {
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			*dst = fallback
			return
		}
		*dst = n
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "fillcolor":
		parseInt(&options.fill, 0)
	case "transparentcolor":
		parseInt(&options.transparent, 9)
	case "selectedcolor":
		parseInt(&options.selected, 89)
	case "hovercolor":
		parseInt(&options.hover, 84)
	case "usedefaulticon":
		// The source lowercases the text and searches for the word rather
		// than using the preference parser's Boolean conversion.
		options.useDefault = strings.Contains(strings.ToLower(value), "true")
	case "usecirclehover":
		options.circleHover = strings.Contains(strings.ToLower(value), "true")
	}
}

func (cfg *communityIconConfig) loadArt() error {
	dir := filepath.Dir(cfg.source)
	loaded := make(map[string]communityIconArt)
	for i := range cfg.rows {
		row := &cfg.rows[i]
		path, err := resolveCommunityIconPath(dir, row.path)
		if err != nil {
			return fmt.Errorf("icon %q from %s: %w", row.name, row.path, err)
		}
		art, ok := loaded[path]
		if !ok {
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("icon %q from %s: %w", row.name, path, err)
			}
			pcx, err := formats.LoadPCX(data)
			if err != nil {
				return fmt.Errorf("icon %q from %s: %w", row.name, path, err)
			}
			art = makeCommunityIconArt(pcx, cfg.options)
			loaded[path] = art
		}
		row.art = art
	}
	return cfg.packArt()
}

func resolveCommunityIconPath(dir, authored string) (string, error) {
	name := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(authored, `\`, "/")))
	current := filepath.Clean(dir)
	parts := strings.Split(name, string(filepath.Separator))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			current = filepath.Dir(current)
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return "", fmt.Errorf("resolve authored path %q at %s: %w", authored, current, err)
		}
		exact := ""
		var folded []string
		for _, entry := range entries {
			if entry.Name() == part {
				exact = entry.Name()
				break
			}
			if strings.EqualFold(entry.Name(), part) {
				folded = append(folded, entry.Name())
			}
		}
		if exact != "" {
			current = filepath.Join(current, exact)
			continue
		}
		switch len(folded) {
		case 0:
			return "", fmt.Errorf("resolve authored path %q at %s: component %q: %w", authored, current, part, os.ErrNotExist)
		case 1:
			current = filepath.Join(current, folded[0])
		default:
			return "", fmt.Errorf("resolve authored path %q at %s: component %q has ambiguous case-insensitive matches %v", authored, current, part, folded)
		}
	}
	return current, nil
}

func makeCommunityIconArt(pcx *formats.PCX, options communityIconOptions) communityIconArt {
	w, h := int(pcx.Width), int(pcx.Height)
	art := communityIconArt{w: w, h: h, masks: make([]byte, w*h*4)}
	if options.circleHover {
		art.hoverCircle = make([]byte, w*h*4)
	}
	for i, index := range pcx.Pixels {
		writeCommunityIconPixel(art.masks[i*4:i*4+4], index, pcx, options, false)
		if art.hoverCircle != nil {
			writeCommunityIconPixel(art.hoverCircle[i*4:i*4+4], index, pcx, options, true)
		}
	}
	if art.hoverCircle != nil {
		// Source draws a separate radius-to-corners circle. The modern marker
		// contract has a fixed 24-pixel footprint, so the host mapping keeps a
		// one-pixel ring inside that footprint rather than changing picking.
		cx, cy := float64(w-1)/2, float64(h-1)/2
		radius := max(float64(min(w, h))/2-1, .5)
		inner := max(radius-.75, 0.0)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dx, dy := float64(x)-cx, float64(y)-cy
				d2 := dx*dx + dy*dy
				if d2 >= inner*inner && d2 <= (radius+.75)*(radius+.75) {
					o := (y*w + x) * 4
					if art.hoverCircle[o] == 0 && art.hoverCircle[o+1] == 0 && art.hoverCircle[o+3] == 0 {
						art.hoverCircle[o+2] = 255
					}
				}
			}
		}
	}
	return art
}

func writeCommunityIconPixel(dst []byte, index byte, pcx *formats.PCX, options communityIconOptions, circleHover bool) {
	i := int(index)
	switch {
	case i == options.transparent:
		return
	case i == options.fill:
		dst[0] = 255
	case i == options.selected:
		if !circleHover {
			dst[2] = 255
		}
	default:
		// The existing strategic-icon shader has team, white, halo and black
		// lanes rather than a fixed-colour palette lane. Preserve the PCX
		// pixel's luminance between the white and black lanes; configured fill,
		// selection, hover and transparency retain their source substitutions.
		c := pcx.Palette[index]
		light := uint8((77*uint32(c.R) + 150*uint32(c.G) + 29*uint32(c.B) + 128) >> 8)
		dst[1], dst[3] = light, 255-light
	}
}

func (cfg *communityIconConfig) packArt() error {
	width, height := 0, 0
	for i := range cfg.rows {
		art := &cfg.rows[i].art
		width = max(width, art.w)
		height += art.h
		if art.hoverCircle != nil {
			height += art.h
		}
	}
	if width <= 0 || height <= 0 || uint64(width)*uint64(height) > maxCommunityIconAtlasPixels {
		return fmt.Errorf("custom icon atlas dimensions %dx%d exceed host limit", width, height)
	}
	atlas := &drawlist.MarkerAtlas{Width: width, Height: height, Pixels: make([]byte, width*height*4)}
	y := 0
	for i := range cfg.rows {
		art := &cfg.rows[i].art
		art.rect = drawlist.Rect{Y: int32(y), W: int32(art.w), H: int32(art.h)}
		copyCommunityIconTile(atlas, y, art.w, art.h, art.masks)
		y += art.h
		if art.hoverCircle != nil {
			art.hoverCircleRect = drawlist.Rect{Y: int32(y), W: int32(art.w), H: int32(art.h)}
			copyCommunityIconTile(atlas, y, art.w, art.h, art.hoverCircle)
			y += art.h
		}
		art.masks, art.hoverCircle = nil, nil
	}
	for i := range cfg.rows {
		art := &cfg.rows[i].art
		art.rect.X = 0
		art.hoverCircleRect.X = 0
		art.atlas = atlas
		art.masks, art.hoverCircle = nil, nil
	}
	return nil
}

func copyCommunityIconTile(atlas *drawlist.MarkerAtlas, y, w, h int, pixels []byte) {
	for row := 0; row < h; row++ {
		dst := ((y + row) * atlas.Width) * 4
		copy(atlas.Pixels[dst:dst+w*4], pixels[row*w*4:(row+1)*w*4])
	}
}

func applyCommunityIconConfig(c *StrategicIconCatalog, cfg *communityIconConfig) *StrategicIconCatalog {
	if c == nil || cfg == nil || len(cfg.rows) == 0 {
		return c
	}
	atlas := cfg.rows[0].art.atlas
	if atlas == nil {
		return c
	}
	unknown := -1
	for i := range cfg.rows {
		if strings.EqualFold(cfg.rows[i].name, "unknow") {
			unknown = i
			break
		}
	}
	apply := func(d StrategicIconDescriptor, row int) StrategicIconDescriptor {
		if row < 0 {
			return d
		}
		r := &cfg.rows[row]
		d.Atlas, d.Rect = atlas, r.art.rect
		d.communityConfigured = true
		d.communitySelected = uint8(cfg.options.selected)
		d.communityHover = uint8(cfg.options.hover)
		d.communityCircle = cfg.options.circleHover
		d.communityHoverRect = r.art.hoverCircleRect
		d.Evidence = append(d.Evidence, fmt.Sprintf("host icon config: %s=%s from %s", r.name, r.path, cfg.source))
		return d
	}
	for i := range c.entries {
		row := unknown
		id := c.entries[i].DefinitionID
		for j := range cfg.rows {
			r := &cfg.rows[j]
			if !r.reserved && r.mask.Contains(id) {
				row = j
				break
			}
		}
		c.entries[i].Descriptor = apply(c.entries[i].Descriptor, row)
	}
	c.fallback = apply(c.fallback, unknown)
	c.byName = make(map[string]StrategicIconDescriptor, len(c.entries))
	c.byRecord = make(map[uint16]StrategicIconDescriptor, len(c.entries))
	for i := range c.entries {
		e := &c.entries[i]
		if _, exists := c.byName[e.Definition]; !exists {
			c.byName[e.Definition] = e.Descriptor
		}
		if e.DefinitionID > 0 && e.DefinitionID <= 65535 {
			c.byRecord[uint16(e.DefinitionID)] = e.Descriptor
		}
	}
	return c
}
