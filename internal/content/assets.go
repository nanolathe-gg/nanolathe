package content

// AssetID is a stable, typed identity resolved before the first battle frame.
// An empty ID or a failed lookup means the authored asset is absent; callers
// must leave the corresponding pixels transparent rather than invent art
// [03 §1], [03 §5.1].
type AssetID string

// AssetSequence describes an authored frame sequence and its per-frame
// durations. Slices returned by Assets are detached copies, so the catalog is
// read-only to battle consumers [03 §4.4], [03 §5.5].
type AssetSequence struct {
	ID        AssetID
	Frames    []AssetID
	Durations []uint32
	Loop      bool
}

type FeatureAsset struct {
	ID     AssetID
	Body   AssetSequence
	Shadow AssetSequence
	Model  AssetID
}

// TextureSet keeps side-specific precedence explicit: Side is consulted by
// the renderer before Default; Logos is the exactly-ten-frame team entry
// identified by authored metadata [03 §2.4.1].
type TextureSet struct {
	ID        AssetID
	Side      map[uint8]AssetID
	Default   AssetID
	Logos     AssetID
	Durations []uint32
}

type ModelAsset struct {
	ID       AssetID
	Textures TextureSet
}

type ProjectileAsset struct {
	ID        AssetID
	Graphic   AssetID
	Model     AssetID
	Selectors AssetSequence
	Colors    []uint8
}

type EffectAsset struct {
	ID           AssetID
	Sequence     AssetSequence
	FlashDisc    AssetID
	Wake         AssetID
	Splash       AssetID
	Construction AssetID
}

type FogAsset struct {
	ID          AssetID
	Current     AssetSequence
	History     AssetSequence
	Transparent AssetID
}

type MinimapAsset struct {
	ID        AssetID
	Blip      AssetID
	Commander AssetID
	HOT       AssetID
	Ring      AssetID
	Circle    AssetID
}

type CursorAsset struct {
	ID       AssetID
	Frames   AssetSequence
	HotspotX int16
	HotspotY int16
}

// Assets is the read-only battle-lifetime asset service. Lookup misses are
// represented by ok=false and never trigger diagnostic or synthetic art.
type Assets interface {
	Feature(AssetID) (FeatureAsset, bool)
	Model(AssetID) (ModelAsset, bool)
	Projectile(AssetID) (ProjectileAsset, bool)
	Effect(AssetID) (EffectAsset, bool)
	Fog(AssetID) (FogAsset, bool)
	Minimap(AssetID) (MinimapAsset, bool)
	Cursor(AssetID) (CursorAsset, bool)
}

// AssetCatalog is the immutable input copied by NewAssets. Maps and slices
// are cloned at construction, so a loader may release or reuse its buffers.
type AssetCatalog struct {
	Features    map[AssetID]FeatureAsset
	Models      map[AssetID]ModelAsset
	Projectiles map[AssetID]ProjectileAsset
	Effects     map[AssetID]EffectAsset
	Fog         map[AssetID]FogAsset
	Minimap     map[AssetID]MinimapAsset
	Cursors     map[AssetID]CursorAsset
}

// Catalog is the concrete immutable lookup implementation. Its fields are
// private; callers receive only the Assets interface or value copies.
type AssetCatalogService struct{ data AssetCatalog }

// NewAssets copies an optional catalog specification into a read-only service.
func NewAssets(spec ...AssetCatalog) *AssetCatalogService {
	var src AssetCatalog
	if len(spec) != 0 {
		src = spec[0]
	}
	return &AssetCatalogService{data: cloneAssetCatalog(src)}
}

// NewAssetCatalog is a descriptive alias for NewAssets.
func NewAssetCatalog(spec AssetCatalog) Assets { return NewAssets(spec) }

func (c *AssetCatalogService) Feature(id AssetID) (FeatureAsset, bool) {
	if c == nil {
		return FeatureAsset{}, false
	}
	v, ok := c.data.Features[id]
	return cloneAssetFeature(v), ok
}
func (c *AssetCatalogService) Model(id AssetID) (ModelAsset, bool) {
	if c == nil {
		return ModelAsset{}, false
	}
	v, ok := c.data.Models[id]
	return cloneAssetModel(v), ok
}
func (c *AssetCatalogService) Projectile(id AssetID) (ProjectileAsset, bool) {
	if c == nil {
		return ProjectileAsset{}, false
	}
	v, ok := c.data.Projectiles[id]
	return cloneAssetProjectile(v), ok
}
func (c *AssetCatalogService) Effect(id AssetID) (EffectAsset, bool) {
	if c == nil {
		return EffectAsset{}, false
	}
	v, ok := c.data.Effects[id]
	return cloneAssetEffect(v), ok
}
func (c *AssetCatalogService) Fog(id AssetID) (FogAsset, bool) {
	if c == nil {
		return FogAsset{}, false
	}
	v, ok := c.data.Fog[id]
	return cloneAssetFog(v), ok
}
func (c *AssetCatalogService) Minimap(id AssetID) (MinimapAsset, bool) {
	if c == nil {
		return MinimapAsset{}, false
	}
	v, ok := c.data.Minimap[id]
	return v, ok
}
func (c *AssetCatalogService) Cursor(id AssetID) (CursorAsset, bool) {
	if c == nil {
		return CursorAsset{}, false
	}
	v, ok := c.data.Cursors[id]
	return cloneAssetCursor(v), ok
}

func cloneAssetSequence(s AssetSequence) AssetSequence {
	s.Frames = append([]AssetID(nil), s.Frames...)
	s.Durations = append([]uint32(nil), s.Durations...)
	return s
}
func cloneAssetFeature(v FeatureAsset) FeatureAsset {
	v.Body = cloneAssetSequence(v.Body)
	v.Shadow = cloneAssetSequence(v.Shadow)
	return v
}
func cloneAssetTexture(v TextureSet) TextureSet {
	v.Side = cloneAssetSide(v.Side)
	v.Durations = append([]uint32(nil), v.Durations...)
	return v
}
func cloneAssetModel(v ModelAsset) ModelAsset { v.Textures = cloneAssetTexture(v.Textures); return v }
func cloneAssetProjectile(v ProjectileAsset) ProjectileAsset {
	v.Selectors = cloneAssetSequence(v.Selectors)
	v.Colors = append([]uint8(nil), v.Colors...)
	return v
}
func cloneAssetEffect(v EffectAsset) EffectAsset {
	v.Sequence = cloneAssetSequence(v.Sequence)
	return v
}
func cloneAssetFog(v FogAsset) FogAsset {
	v.Current = cloneAssetSequence(v.Current)
	v.History = cloneAssetSequence(v.History)
	return v
}
func cloneAssetCursor(v CursorAsset) CursorAsset { v.Frames = cloneAssetSequence(v.Frames); return v }
func cloneAssetSide(src map[uint8]AssetID) map[uint8]AssetID {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[uint8]AssetID, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneAssetCatalog(src AssetCatalog) AssetCatalog {
	dst := AssetCatalog{
		Features: make(map[AssetID]FeatureAsset, len(src.Features)), Models: make(map[AssetID]ModelAsset, len(src.Models)),
		Projectiles: make(map[AssetID]ProjectileAsset, len(src.Projectiles)), Effects: make(map[AssetID]EffectAsset, len(src.Effects)),
		Fog: make(map[AssetID]FogAsset, len(src.Fog)), Minimap: make(map[AssetID]MinimapAsset, len(src.Minimap)), Cursors: make(map[AssetID]CursorAsset, len(src.Cursors)),
	}
	for k, v := range src.Features {
		dst.Features[k] = cloneAssetFeature(v)
	}
	for k, v := range src.Models {
		dst.Models[k] = cloneAssetModel(v)
	}
	for k, v := range src.Projectiles {
		dst.Projectiles[k] = cloneAssetProjectile(v)
	}
	for k, v := range src.Effects {
		dst.Effects[k] = cloneAssetEffect(v)
	}
	for k, v := range src.Fog {
		dst.Fog[k] = cloneAssetFog(v)
	}
	for k, v := range src.Minimap {
		dst.Minimap[k] = v
	}
	for k, v := range src.Cursors {
		dst.Cursors[k] = cloneAssetCursor(v)
	}
	return dst
}
