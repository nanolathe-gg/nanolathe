package client

import (
	"container/list"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// Nanolathe host storage policy, DESIGN_PRESENTATION_CLIENT "On-demand effect
// art". The observed largest Escalation root is below 24 Mi pixels. A 32 Mi
// root allowance includes its parent canvas and children; source-bank geometry
// is validated separately. These are cache bounds, not gameplay/art limits.
const (
	effectSourceBytes       = 256 << 20
	effectFrameBytes        = 96 << 20
	effectVariantBytes      = 256 << 20
	effectRootPixels        = 32 << 20
	effectDurableBytes      = 16 << 20
	effectDurableFrameBytes = 1 << 20
)

type effectCacheItem[K comparable, V any] struct {
	key   K
	value V
	bytes uint64
}
type effectLRU[K comparable, V any] struct {
	items map[K]*list.Element
	order list.List
	bytes uint64
}

func (c *effectLRU[K, V]) get(key K) (V, bool) {
	if e := c.items[key]; e != nil {
		c.order.MoveToFront(e)
		return e.Value.(effectCacheItem[K, V]).value, true
	}
	var zero V
	return zero, false
}
func (c *effectLRU[K, V]) put(key K, value V, size, limit uint64) {
	if size > limit {
		return
	}
	if c.items == nil {
		c.items = make(map[K]*list.Element)
	}
	for c.bytes+size > limit || len(c.items) >= 256 {
		e := c.order.Back()
		old := e.Value.(effectCacheItem[K, V])
		delete(c.items, old.key)
		c.bytes -= old.bytes
		c.order.Remove(e)
	}
	c.items[key] = c.order.PushFront(effectCacheItem[K, V]{key, value, size})
	c.bytes += size
}

type effectArtCache struct {
	sources      effectLRU[string, *formats.GAFSource]
	frames       effectLRU[*formats.GAFFrame, *formats.GAFFrame]
	variants     effectLRU[*formats.GAFFrame, *formats.GAFFrame]
	digests      map[string][32]byte
	errors       map[string]error
	durable      map[*formats.GAFFrame]*formats.GAFFrame
	durableBytes uint64
}

func (c *Client) effectCacheLocked() *effectArtCache {
	if c.effectArt == nil {
		c.effectArt = &effectArtCache{digests: make(map[string][32]byte), errors: make(map[string]error), durable: make(map[*formats.GAFFrame]*formats.GAFFrame)}
	}
	return c.effectArt
}
func (c *Client) effectSourceLocked(key string) (*formats.GAFSource, error) {
	cache := c.effectCacheLocked()
	if err := cache.errors[key]; err != nil {
		return nil, err
	}
	if source, ok := cache.sources.get(key); ok {
		return source, nil
	}
	limits := formats.DefaultGAFLimits()
	limits.MaxDecodedPixels, limits.MaxExpandedPixels = 512<<20, 512<<20
	source, err := formats.LoadGAFSourceFile(c.modelFS, "anims/"+key+".gaf", effectSourceBytes, limits)
	if err != nil {
		cache.errors[key] = err
		return nil, err
	}
	if digest, loaded := cache.digests[key]; loaded && digest != source.Digest() {
		err := fmt.Errorf("animation source changed after metadata binding")
		cache.errors[key] = err
		return nil, err
	}
	cache.digests[key] = source.Digest()
	cache.sources.put(key, source, source.EncodedBytes(), effectSourceBytes)
	return source, nil
}

// effectBankMetadata retains entry timing and root geometry independently of
// the encoded-source cache. It intentionally retains no child graph or pixels.
// The public bank is immutable; transient placeholders resolve through
// effectFrame, never by writing decoded pointers into the durable entry table.
func effectBankMetadata(source *formats.GAFSource) *formats.GAF {
	m := source.Metadata()
	bank := &formats.GAF{Version: m.Version, EntryCount: m.EntryCount, Unknown: m.Unknown, Entries: make([]formats.GAFEntry, len(m.Entries))}
	frames := make(map[*formats.GAFMetadataFrame]*formats.GAFFrame)
	refs := make(map[*formats.GAFMetadataFrameRef][]formats.GAFFrameRef)
	for i, e := range m.Entries {
		out := &bank.Entries[i]
		*out = formats.GAFEntry{Name: e.Name, FrameCount: e.FrameCount, Unknown1: e.Unknown1, Unknown2: e.Unknown2, Frames: nil}
		if len(e.Frames) == 0 {
			continue
		}
		if shared, ok := refs[&e.Frames[0]]; ok {
			out.Frames = shared
			continue
		}
		out.Frames = make([]formats.GAFFrameRef, len(e.Frames))
		refs[&e.Frames[0]] = out.Frames
		for j, ref := range e.Frames {
			f := frames[ref.Frame]
			if f == nil {
				f = &formats.GAFFrame{Transient: true, Width: ref.Frame.Width, Height: ref.Frame.Height, XOffset: ref.Frame.XOffset, YOffset: ref.Frame.YOffset}
				frames[ref.Frame] = f
			}
			out.Frames[j] = formats.GAFFrameRef{Offset: ref.Offset, Value: ref.Value, Frame: f}
		}
	}
	return bank
}

func (c *Client) effectFrame(bankName, entryName string, index int32) (*formats.GAFFrame, bool) {
	entry, ok := c.effectEntry(bankName, entryName)
	if !ok {
		return nil, false
	}
	index = max(0, min(index, int32(len(entry.Frames)-1)))
	identity := entry.Frames[index].Frame
	if identity == nil {
		return nil, false
	}
	// Authored fixtures and other explicitly supplied eager banks remain valid.
	if !identity.Transient {
		return identity, true
	}
	if mu := c.artMu; mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	cache := c.effectCacheLocked()
	if f := cache.durable[identity]; f != nil {
		return f, true
	}
	if f, ok := cache.frames.get(identity); ok {
		return f, true
	}
	key := effectBankKey(bankName)
	source, err := c.effectSourceLocked(key)
	if err == nil {
		var f *formats.GAFFrame
		f, err = source.Frame(entryName, int(index), effectRootPixels)
		if err == nil {
			size := formats.GAFFrameBytes(f)
			if size <= effectDurableFrameBytes && size <= effectDurableBytes-cache.durableBytes && len(cache.durable) < 256 && effectFrameIsTree(f) {
				makeEffectFrameDurable(f)
				cache.durable[identity] = f
				cache.durableBytes += size
			} else {
				cache.frames.put(identity, f, size, effectFrameBytes)
			}
			return f, true
		}
	}
	c.recordArtDiagnosticLocked("anims/"+key+".gaf", entryName, err.Error())
	return nil, false
}

func (c *Client) transientVariant(f *formats.GAFFrame) *formats.GAFFrame {
	if mu := c.artMu; mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	cache := c.effectCacheLocked()
	if out, ok := cache.variants.get(f); ok {
		return out
	}
	out := f.Doubled()
	cache.variants.put(f, out, formats.GAFFrameBytes(out)+formats.GAFFrameBytes(f), effectVariantBytes)
	return out
}

// Small, byte-bounded roots keep stable identities so stock smoke/fire art
// continues to share the ordinary GPU atlas instead of splitting every blit.
func makeEffectFrameDurable(f *formats.GAFFrame) {
	if f == nil || !f.Transient {
		return
	}
	f.Transient = false
	if view, ok := f.DirectRaster(); ok {
		view.Transient = false
	}
	for _, child := range f.Subframes {
		makeEffectFrameDurable(child)
	}
}

// Detached cache-residency counters exclude encoded metadata bookkeeping and
// in-flight recorded lists, which own their references independently.
func (c *Client) effectCacheSnapshot() map[string]uint64 {
	if mu := c.artMu; mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	if c.effectArt == nil {
		return nil
	}
	a := c.effectArt
	return map[string]uint64{"source_bytes": a.sources.bytes, "source_count": uint64(len(a.sources.items)), "frame_bytes": a.frames.bytes, "frame_count": uint64(len(a.frames.items)), "variant_bytes": a.variants.bytes, "variant_count": uint64(len(a.variants.items)), "durable_bytes": a.durableBytes, "durable_count": uint64(len(a.durable))}
}

// Doubled expands repeated child references. Only trees enter the durable tier,
// keeping its independently retained doubled variants within four times its
// charged source size. Aliased graphs use the byte-bounded transient tier.
func effectFrameIsTree(root *formats.GAFFrame) bool {
	if len(root.Subframes) == 0 {
		return true
	}
	seen := make(map[*formats.GAFFrame]bool)
	var visit func(*formats.GAFFrame) bool
	visit = func(f *formats.GAFFrame) bool {
		if seen[f] {
			return false
		}
		seen[f] = true
		for _, child := range f.Subframes {
			if !visit(child) {
				return false
			}
		}
		return true
	}
	return visit(root)
}
