package content

// This file is the content/presentation boundary for authored image assets.
// It performs all VFS discovery and decoding before a battle starts. Draw
// code receives stable IDs and copied metadata; it never searches an archive,
// enumerates a directory, or chooses an entry by a first-match heuristic
// [03 §4.3][03 §4.4][03 §5.1][03 §5.4][03 §5.5].

import (
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/vfs"
)

// PresentationFrame is immutable frame metadata identified by a stable
// logical ID. Pixel bytes are copied when returned by Frame, so a caller
// cannot mutate the battle-lifetime catalog.
type PresentationFrame struct {
	ID       AssetID
	GAF      AssetID
	Entry    AssetID
	Index    int
	Width    uint16
	Height   uint16
	XOffset  int16
	YOffset  int16
	ColorKey uint8
	Duration uint32
}

// PresentationCatalog owns the decoded, read-only presentation resources for
// one battle. The Assets service is deliberately the narrow public lookup
// surface used by renderers. FNTs and detached GAF frame data are retained for
// adapters that need authored pixels.
type PresentationCatalog struct {
	assets Assets
	frames map[AssetID]PresentationFrame
	pixels map[AssetID]framePixels
	fonts  map[AssetID]*formats.FNT
	pal    *palette.Tables
}

type framePixels struct {
	pixels      []byte
	transparent []bool
}

// Assets returns the immutable service consumed by presentation code.
func (c *PresentationCatalog) Assets() Assets {
	if c == nil {
		return NewAssets()
	}
	return c.assets
}

// Palette returns the decoded palette tables, or nil when one of the optional
// table files was not present or was malformed. The absence is intentional;
// callers must retain their no-pixels behavior for unresolved art.
func (c *PresentationCatalog) Palette() *palette.Tables {
	if c == nil || c.pal == nil {
		return nil
	}
	// Tables currently contain fixed arrays, but return a value copy so this
	// boundary remains read-only if the table representation gains buffers.
	copy := *c.pal
	return &copy
}

// Frame returns detached authored frame data. A missing ID is an ordinary
// optional-resource miss and returns false.
func (c *PresentationCatalog) Frame(id AssetID) (formats.GAFFrame, bool) {
	if c == nil {
		return formats.GAFFrame{}, false
	}
	meta, ok := c.frames[id]
	if !ok {
		return formats.GAFFrame{}, false
	}
	p := c.pixels[id]
	f := formats.GAFFrame{
		Width: meta.Width, Height: meta.Height, XOffset: meta.XOffset,
		YOffset: meta.YOffset, ColorKey: meta.ColorKey,
		Pixels:      append([]byte(nil), p.pixels...),
		Transparent: append([]bool(nil), p.transparent...),
	}
	return f, true
}

// Font returns a detached FNT value by stable logical ID.
func (c *PresentationCatalog) Font(id AssetID) (*formats.FNT, bool) {
	if c == nil {
		return nil, false
	}
	f, ok := c.fonts[id]
	if !ok || f == nil {
		return nil, false
	}
	out := &formats.FNT{Height: f.Height, Unknown: f.Unknown}
	for i, g := range f.Glyphs {
		if g == nil {
			continue
		}
		out.Glyphs[i] = &formats.FNTGlyph{Width: g.Width, Height: g.Height, Bits: append([]byte(nil), g.Bits...)}
	}
	return out, true
}

// LoadPresentationAssets eagerly builds a battle-lifetime catalog. defs is
// optional so front-end and focused tests can load presentation resources
// without compiling the complete game catalog. Missing optional resources and
// malformed optional files are recorded as absence, not replaced by art.
func LoadPresentationAssets(fs vfs.FSOps, defs ...*Catalog) (*PresentationCatalog, error) {
	if fs == nil {
		return nil, errors.New("content: nil VFS")
	}
	c := &PresentationCatalog{
		assets: NewAssets(),
		frames: make(map[AssetID]PresentationFrame),
		pixels: make(map[AssetID]framePixels),
		fonts:  make(map[AssetID]*formats.FNT),
	}
	loaded := make(map[string]*loadedGAF)
	for _, dir := range []string{"anims", "textures"} {
		files, err := fs.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, info := range files {
			if info.IsDir || !strings.EqualFold(path.Ext(info.Path), ".gaf") {
				continue
			}
			logical := canonicalPath(info.Path)
			gaf, err := formats.LoadGAFFile(fs, logical)
			if err != nil {
				continue // optional malformed art remains absent
			}
			loaded[logical] = &loadedGAF{path: logical, gaf: gaf}
		}
	}
	paths := make([]string, 0, len(loaded))
	for p := range loaded {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	spec := AssetCatalog{}
	for _, logical := range paths {
		addGAF(c, &spec, loaded[logical])
	}
	if len(defs) != 0 && defs[0] != nil {
		linkDefinitionAssets(&spec, defs[0], loaded)
	}
	c.loadFonts(fs)
	c.assets = NewAssets(spec)
	// Palette tables are a single eager operation. Unlike optional image
	// entries, a partial table set cannot produce a coherent palette, so nil is
	// retained and the presentation backend decides its documented no-art path.
	if tables, err := palette.Load(fs); err == nil {
		c.pal = tables
	}
	return c, nil
}

func (c *PresentationCatalog) loadFonts(fs vfs.FSOps) {
	entries, err := fs.ReadDir("fonts")
	if err != nil {
		return
	}
	for _, info := range entries {
		if info.IsDir || !strings.EqualFold(path.Ext(info.Path), ".fnt") {
			continue
		}
		logical := canonicalPath(info.Path)
		font, err := formats.LoadFNTFile(fs, logical)
		if err != nil {
			continue
		}
		c.fonts[AssetID(logical)] = font
	}
}

type loadedGAF struct {
	path string
	gaf  *formats.GAF
}

func addGAF(c *PresentationCatalog, spec *AssetCatalog, loaded *loadedGAF) {
	if loaded == nil || loaded.gaf == nil {
		return
	}
	fileID := AssetID(loaded.path)
	seenEntries := make(map[AssetID]struct{}, len(loaded.gaf.Entries))
	for entryIndex := range loaded.gaf.Entries {
		entry := &loaded.gaf.Entries[entryIndex]
		entryID := entryAssetID(loaded.path, entry.Name)
		if _, exists := seenEntries[entryID]; exists {
			continue
		}
		seenEntries[entryID] = struct{}{}
		seq := sequenceFromEntry(entryID, entry)
		if entry.FrameCount == 0 || len(entry.Frames) == 0 {
			continue
		}
		for i, ref := range entry.Frames {
			if ref.Frame == nil {
				continue
			}
			id := frameAssetID(entryID, i)
			c.frames[id] = PresentationFrame{ID: id, GAF: fileID, Entry: entryID, Index: i,
				Width: ref.Frame.Width, Height: ref.Frame.Height, XOffset: ref.Frame.XOffset,
				YOffset: ref.Frame.YOffset, ColorKey: ref.Frame.ColorKey, Duration: ref.Value}
			c.pixels[id] = framePixels{pixels: append([]byte(nil), ref.Frame.Pixels...), transparent: append([]bool(nil), ref.Frame.Transparent...)}
		}
		// The source path, not an inferred frame count, identifies LOGOS as
		// team art [03 §4.4][fmt gaf].
		if strings.HasPrefix(loaded.path, "textures/") {
			if spec.Models == nil {
				spec.Models = make(map[AssetID]ModelAsset)
			}
			spec.Models[entryID] = ModelAsset{ID: entryID, Textures: TextureSet{
				ID: entryID, Default: entryID, Durations: append([]uint32(nil), seq.Durations...),
			}}
			if loaded.path == "textures/logos.gaf" && len(entry.Frames) == 10 {
				a := spec.Models[entryID]
				a.Textures.Default = ""
				a.Textures.Logos = entryID
				spec.Models[entryID] = a
			}
		}
		if strings.HasPrefix(loaded.path, "anims/") {
			if spec.Effects == nil {
				spec.Effects = make(map[AssetID]EffectAsset)
			}
			// The entry's authored path and name are the only identity here;
			// semantic producer ownership remains with the event catalog.
			spec.Effects[entryID] = EffectAsset{ID: entryID, Sequence: seq}
		}
		if loaded.path == "anims/cursors.gaf" {
			if spec.Cursors == nil {
				spec.Cursors = make(map[AssetID]CursorAsset)
			}
			first := entry.Frames[0].Frame
			spec.Cursors[entryID] = CursorAsset{ID: entryID, Frames: seq, HotspotX: first.XOffset, HotspotY: first.YOffset}
		}
		// Keep explicitly named fog files addressable without claiming that an
		// entry is a different semantic channel than its authored identity.
		if loaded.path == "anims/fog.gaf" || loaded.path == "anims/fogtiles.gaf" {
			if spec.Fog == nil {
				spec.Fog = make(map[AssetID]FogAsset)
			}
			spec.Fog[entryID] = FogAsset{ID: entryID, Current: seq}
		}
	}
}

func linkDefinitionAssets(spec *AssetCatalog, defs *Catalog, loaded map[string]*loadedGAF) {
	if defs == nil {
		return
	}
	if spec.Features == nil {
		spec.Features = make(map[AssetID]FeatureAsset)
	}
	featureKeys := make([]string, 0, len(defs.Features))
	for key := range defs.Features {
		featureKeys = append(featureKeys, key)
	}
	sort.Strings(featureKeys)
	for _, key := range featureKeys {
		f := defs.Features[key]
		if f == nil {
			continue
		}
		id := AssetID("feature:" + CanonicalKey(key))
		modelName := CanonicalKey(f.Object)
		asset := FeatureAsset{ID: id}
		if modelName != "" {
			asset.Model = AssetID("model:" + modelName)
		}
		gaf, ok := loaded[explicitGAFPath(f.Filename)]
		if ok {
			asset.Body = sequenceForName(gaf, f.SeqName)
			asset.Shadow = sequenceForName(gaf, f.SeqNameShad)
		}
		spec.Features[id] = asset
	}
	if spec.Projectiles == nil {
		spec.Projectiles = make(map[AssetID]ProjectileAsset)
	}
	weaponKeys := make([]string, 0, len(defs.Weapons))
	for key := range defs.Weapons {
		weaponKeys = append(weaponKeys, key)
	}
	sort.Strings(weaponKeys)
	for _, key := range weaponKeys {
		w := defs.Weapons[key]
		if w == nil {
			continue
		}
		id := AssetID("projectile:" + CanonicalKey(key))
		modelName := CanonicalKey(w.Model)
		asset := ProjectileAsset{ID: id}
		if modelName != "" {
			asset.Model = AssetID("model:" + modelName)
		}
		spec.Projectiles[id] = asset
	}
}

func sequenceForName(g *loadedGAF, name string) AssetSequence {
	if g == nil || strings.TrimSpace(name) == "" {
		return AssetSequence{}
	}
	entry, ok := g.gaf.Find(name)
	if !ok {
		return AssetSequence{}
	}
	return sequenceFromEntry(entryAssetID(g.path, entry.Name), entry)
}

func sequenceFromEntry(id AssetID, entry *formats.GAFEntry) AssetSequence {
	seq := AssetSequence{ID: id}
	if entry == nil {
		return seq
	}
	for i, ref := range entry.Frames {
		if ref.Frame == nil {
			continue
		}
		seq.Frames = append(seq.Frames, frameAssetID(id, i))
		seq.Durations = append(seq.Durations, ref.Value)
	}
	return seq
}

func canonicalPath(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	return strings.ToLower(strings.TrimPrefix(path.Clean(name), "./"))
}

func explicitGAFPath(name string) string {
	name = canonicalPath(name)
	if name == "." || name == "" {
		return ""
	}
	if !strings.HasSuffix(name, ".gaf") {
		name += ".gaf"
	}
	if !strings.Contains(name, "/") {
		name = "anims/" + name
	}
	return name
}

func entryAssetID(file, entry string) AssetID {
	return AssetID(canonicalPath(file) + "#" + CanonicalKey(entry))
}

func frameAssetID(entry AssetID, index int) AssetID {
	return AssetID(string(entry) + "/frame/" + strconv.Itoa(index))
}
