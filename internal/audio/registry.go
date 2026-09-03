package audio

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

// AliasID is the identity used by authored sound references.  Zero is the
// null/full-registration result and 0xffff is the lookup-miss sentinel [03
// §8.3].
type AliasID uint16

const (
	NullAlias     AliasID = 0
	MissingAlias  AliasID = 0xffff
	aliasCapacity         = 256
	maxAliases            = aliasCapacity - 1
)

// Alias is one ordered sound registration.  Names are retained at 32 bytes
// and paths at 64 bytes, matching the authored identity rather than the Go
// map key [03 §8.3].
type Alias struct {
	ID     AliasID
	Name   string
	Path   string
	Probed bool
}

// Registry owns alias identity and the session-lifetime decoded samples.
// Registration order is observable: duplicate names return the first ID and
// failed probes still consume a slot.  There is no runtime eviction [03
// §8.2–§8.3].
type Registry struct {
	fs    vfs.FSOps
	cache *SampleCache
	slots [aliasCapacity]Alias
	count int
}

func NewRegistry(fs vfs.FSOps) *Registry {
	return &Registry{fs: fs, cache: NewCache(fs)}
}

func (r *Registry) Cache() *SampleCache {
	if r == nil {
		return nil
	}
	return r.cache
}

// SetFS updates the resolver for an existing registry without replacing its
// ordered alias identities or decoded samples.
func (r *Registry) SetFS(fs vfs.FSOps) {
	if r == nil || fs == nil {
		return
	}
	r.fs = fs
	if r.cache != nil {
		r.cache.SetFS(fs)
	}
}

// SetCache retains an externally supplied cache while keeping registry and
// cache ownership in the audio package.
func (r *Registry) SetCache(cache *SampleCache) {
	if r != nil && cache != nil {
		r.cache = cache
		if r.fs != nil {
			cache.SetFS(r.fs)
		}
	}
}

func aliasName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

func aliasPath(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

func aliasEqual(a, b string) bool { return strings.EqualFold(aliasName(a), aliasName(b)) }

// Register adds an alias and probes canonical sounds/ candidates immediately.
// The probe result is retained even when the VFS has no matching file.
func (r *Registry) Register(name string) AliasID {
	return r.register(name, "")
}

func (r *Registry) register(name, soundPath string) AliasID {
	if r == nil {
		return NullAlias
	}
	name = aliasName(name)
	if name == "" {
		return NullAlias
	}
	for i := 1; i <= r.count; i++ {
		if aliasEqual(r.slots[i].Name, name) {
			return r.slots[i].ID
		}
	}
	if r.count >= maxAliases {
		return NullAlias
	}
	r.count++
	id := AliasID(r.count)
	// Retain a canonical candidate even when probing fails; path and probe
	// status are separate parts of the registered identity [03 §8.3].
	a := Alias{ID: id, Name: name, Path: aliasPath("sounds/" + name)}
	// Probe loading is part of registration identity.  A missing sample is
	// intentionally not an error to the caller: later playback degrades to
	// silence while the slot remains occupied.
	if r.fs != nil {
		candidates := canonicalAliasPaths(name)
		if soundPath != "" {
			a.Path = aliasPath(soundPath)
			candidates = append(authoredSoundPaths(soundPath), candidates...)
		}
		if data, p, err := r.cache.resolveCandidates(candidates); err == nil {
			a.Path = aliasPath(p.LogicalPath)
			if sample, decodeErr := Decode(name, data); decodeErr == nil {
				sample.Provenance = p
				r.cache.putSample(name, sample)
				a.Probed = true
			}
		}
	}
	r.slots[r.count] = a
	return id
}

// RegisterPath registers an authored alias whose sound value is a separate
// path.  The identity remains the alias name; the path is only a probe hint.
func (r *Registry) RegisterPath(name, soundPath string) AliasID {
	return r.register(name, soundPath)
}

func (r *Registry) Lookup(name string) AliasID {
	if r == nil {
		return MissingAlias
	}
	name = aliasName(name)
	for i := 1; i <= r.count; i++ {
		if aliasEqual(r.slots[i].Name, name) {
			return r.slots[i].ID
		}
	}
	return MissingAlias
}

func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	return r.count
}

func (r *Registry) Entry(id AliasID) (Alias, bool) {
	if r == nil || id == 0 || id >= AliasID(aliasCapacity) || int(id) > r.count {
		return Alias{}, false
	}
	return r.slots[id], true
}

// Load resolves and decodes an alias once for the registry lifetime.
func (r *Registry) Load(id AliasID) (*Sample, error) {
	if r == nil || id == NullAlias || id == MissingAlias {
		return nil, fmt.Errorf("audio: missing alias %d", id)
	}
	a, ok := r.Entry(id)
	if !ok {
		return nil, fmt.Errorf("audio: missing alias %d", id)
	}
	if s, ok := r.cache.Get(a.Name); ok {
		return s, nil
	}
	paths := canonicalAliasPaths(a.Name)
	if a.Path != "" {
		paths = append(authoredSoundPaths(a.Path), paths...)
	}
	return r.cache.loadCandidates(a.Name, paths)
}

func canonicalAliasPaths(name string) []string {
	clean := strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	hasWav := strings.HasSuffix(strings.ToLower(clean), ".wav")
	paths := []string{"sounds/" + clean}
	if !hasWav {
		paths = append(paths, "sounds/"+clean+".wav")
	}
	paths = append(paths, clean)
	if !hasWav {
		paths = append(paths, clean+".wav")
	}
	return paths
}

// authoredSoundPaths is the candidate list for an alias's authored `sound`
// value. Registration probes that value "through the VFS and the WAV decode
// path with the `sounds/` prefix and the canonical candidate tries"
// [03 §8.3 "Alias registration"], and stock `allsound.tdf` authors bare stems
// (`sound=butmain1`) whose files live under `sounds/`.
//
// Correction (WU-19-131): this returned only the unprefixed forms, so every
// alias registered from `allsound.tdf` probed a path no install carries, kept
// the failed path, and stayed silent for the session — which muted every
// interface cue (button clicks, `oktobuild`, `addbuild`, the panel detents) and
// every feature/weapon sound that resolves through an authored alias rather
// than through the alias NAME. The prefixed forms come first because that is
// the order the registrar tries them in; a value that already carries the
// prefix is not prefixed twice.
func authoredSoundPaths(path string) []string {
	clean := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	if clean == "" {
		return nil
	}
	hasWav := strings.HasSuffix(strings.ToLower(clean), ".wav")
	var paths []string
	if !strings.HasPrefix(strings.ToLower(clean), "sounds/") {
		paths = append(paths, "sounds/"+clean)
		if !hasWav {
			paths = append(paths, "sounds/"+clean+".wav")
		}
	}
	paths = append(paths, clean)
	if !hasWav {
		paths = append(paths, clean+".wav")
	}
	return paths
}

func (r *Registry) Aliases() []Alias {
	if r == nil || r.count == 0 {
		return nil
	}
	out := make([]Alias, r.count)
	copy(out, r.slots[1:r.count+1])
	return out
}
