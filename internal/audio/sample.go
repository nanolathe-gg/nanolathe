package audio

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// Sample holds decoded PCM for one alias [fmt wav] [03 §8.2] (I13).
// Byte layout is the contract; Go uses named fields (I13). A Sample is
// immutable after decode or registry admission. Its optional converted PCM is
// presentation-only, belongs to this sample's lifetime, and is not copied
// into replacement aliases or retained by a process-global backend cache.
type Sample struct {
	Alias         string
	Container     string // "RIFF", "DIGI", "raw" [fmt wav]
	AudioFormat   uint16 // 1 PCM [fmt wav]
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	Data          []byte // PCM bytes, owned copy
	Provenance    vfs.Provenance

	registeredMu  sync.Mutex
	registeredPCM []registeredPCM
}

// registeredPCM is one output-rate version of unity-volume, centred stereo
// float32 PCM. Four rates cover normal device changes without making every
// formerly used session rate permanent.
type registeredPCM struct {
	rate int
	data []byte
}

const maxRegisteredPCMRates = 4

// RegisteredPCM returns canonical device PCM for a mode-0 registered sample.
// It is intentionally sample-owned: active playback readers retain only their
// byte slice, and replacing an alias with a new Sample cannot reuse old PCM.
func (s *Sample) RegisteredPCM(rate int) []byte {
	if s == nil || rate <= 0 {
		return nil
	}
	s.registeredMu.Lock()
	defer s.registeredMu.Unlock()
	for _, cached := range s.registeredPCM {
		if cached.rate == rate {
			return cached.data
		}
	}

	data := ConvertSample(s, 1, 0, rate)
	if len(data) == 0 {
		return nil
	}

	if len(s.registeredPCM) == maxRegisteredPCMRates {
		copy(s.registeredPCM, s.registeredPCM[1:])
		s.registeredPCM[len(s.registeredPCM)-1] = registeredPCM{rate: rate, data: data}
		return data
	}
	s.registeredPCM = append(s.registeredPCM, registeredPCM{rate: rate, data: data})
	return data
}

// SampleFormat describes PCM parameters for callers that only need the header.
type SampleFormat struct {
	Channels      uint16
	SampleRate    uint32
	BitsPerSample uint16
	ByteRate      uint32
	BlockAlign    uint16
}

// Format returns the sample's format.
func (s *Sample) Format() SampleFormat {
	if s == nil {
		return SampleFormat{}
	}
	return SampleFormat{
		Channels:      s.Channels,
		SampleRate:    s.SampleRate,
		BitsPerSample: s.BitsPerSample,
		ByteRate:      s.ByteRate,
		BlockAlign:    s.BlockAlign,
	}
}

// Samples returns the number of sample frames (not bytes).
func (s *Sample) Samples() int {
	if s == nil || s.BlockAlign == 0 {
		return 0
	}
	return len(s.Data) / int(s.BlockAlign)
}

// Decode owns a PCM copy from the canonical WAV-family parser [fmt wav].
// Unsupported codecs/widths and unsafe metadata return errors as a deliberate
// host policy; retail does not inspect the format tag [02 R-MALF-01 §10].
func Decode(alias string, data []byte) (*Sample, error) {
	return decodeSample(alias, data, vfs.Provenance{})
}

func decodeSample(alias string, data []byte, prov vfs.Provenance) (*Sample, error) {
	logical := prov.LogicalPath
	if logical == "" {
		logical = alias
	}
	fail := func(cause error) (*Sample, error) {
		return nil, fmt.Errorf("nanolathe: decode sample: logical path %s, providers searched [%s], expected supported PCM: %w", logical, prov.ProviderID(), cause)
	}
	if len(data) == 0 || len(data) > 1<<24 {
		// TODO(question): settle zero-length buffer creation at the retail
		// backend [02 R-MALF-01 §10]. Reject empty input until then; the upper
		// bound is a separate host-safety allocation limit.
		return fail(fmt.Errorf("empty or oversized sample (%d bytes)", len(data)))
	}
	w, err := formats.LoadAudio(data)
	if err != nil {
		return fail(err)
	}
	if w.DataSize == 0 {
		return fail(fmt.Errorf("empty PCM payload"))
	}
	if w.AudioFormat != 1 || (w.BitsPerSample != 8 && w.BitsPerSample != 16) || w.Channels == 0 || w.SampleRate == 0 {
		return fail(fmt.Errorf("unsupported PCM metadata: format %d, channels %d, rate %d, bits %d", w.AudioFormat, w.Channels, w.SampleRate, w.BitsPerSample))
	}
	// Retail derives these values instead of reading the authored fmt words.
	// Keep the parser lossless and apply that playback rule here [02 §7].
	align := uint64(w.BitsPerSample>>3) * uint64(w.Channels)
	rate := align * uint64(w.SampleRate)
	if align > uint64(^uint16(0)) || rate > uint64(^uint32(0)) {
		return fail(fmt.Errorf("PCM frame size or byte rate is too large"))
	}
	pcm := data[uint64(w.DataOffset) : uint64(w.DataOffset)+uint64(w.DataSize)]
	// The format parser retains every declared byte. Playback exposes complete
	// frames, leaving a partial final frame unused [03 §8.2].
	pcm = pcm[:len(pcm)-len(pcm)%int(align)]
	return &Sample{
		Alias: alias, Container: w.Container, AudioFormat: w.AudioFormat,
		Channels: w.Channels, SampleRate: w.SampleRate, ByteRate: uint32(rate),
		BlockAlign: uint16(align), BitsPerSample: w.BitsPerSample,
		Data: append([]byte(nil), pcm...), Provenance: prov,
	}, nil
}

// SampleCache is the alias→Sample cache [03 §8.2] C20.
//
// Retail has no eviction at all: "samples are cached at the alias level: one
// decoded PCM blob per alias, retained for the life of the session, with no
// eviction beyond the alias cap of §8.3" [03 §8.2 "Caching"]. That is the only
// behavior here. A bounded FIFO-255 variant used to stand beside it, reachable
// only from a test constructor, and the same paragraph named it "a documented
// divergence, not retail secondary-buffer eviction"; it is gone. The alias cap
// itself is the registry's, from [02 "Sound aliases"], not a cache size.
//
// Insertion order is preserved so the alias census is deterministic [I1].
type SampleCache struct {
	fs    vfs.FSOps
	order []string // canonical aliases oldest→newest, stable iteration (I1)
	index map[string]*Sample
}

// SetFS updates VFS resolution for an existing cache without discarding
// already decoded samples or their stable eviction order.
func (c *SampleCache) SetFS(fs vfs.FSOps) {
	if c != nil && fs != nil {
		c.fs = fs
	}
}

// NewCache creates the session cache, resolving through fs.
func NewCache(fs vfs.FSOps) *SampleCache {
	return &SampleCache{fs: fs, index: make(map[string]*Sample)}
}

// Len returns the number of cached samples.
func (c *SampleCache) Len() int {
	if c == nil {
		return 0
	}
	return len(c.index)
}

// canonical normalizes an alias for map lookup [02 §2] case-insensitive.
func canonical(alias string) string {
	return strings.ToLower(strings.TrimSpace(alias))
}

// Get returns a cached sample by alias (case-insensitive) without VFS access.
func (c *SampleCache) Get(alias string) (*Sample, bool) {
	if c == nil {
		return nil, false
	}
	k := canonical(alias)
	s, ok := c.index[k]
	return s, ok
}

// Put decodes data and inserts it under alias.
// It is deterministic and stable-ordered (I1).
func (c *SampleCache) Put(alias string, data []byte) (*Sample, error) {
	if c == nil {
		return nil, fmt.Errorf("audio: nil cache")
	}
	if strings.TrimSpace(alias) == "" {
		return nil, fmt.Errorf("audio: empty alias")
	}
	s, err := Decode(alias, data)
	if err != nil {
		return nil, err
	}
	return c.putSample(alias, s), nil
}

func (c *SampleCache) putSample(alias string, s *Sample) *Sample {
	k := canonical(alias)
	if existing, ok := c.index[k]; ok {
		// update in place, preserve order position
		c.index[k] = s
		return existing
	}
	c.index[k] = s
	c.order = append(c.order, k)
	return nil
}

// Load resolves alias through VFS, decodes, caches and returns the sample.
// It hits the cache first (hit), otherwise tries VFS candidate paths in
// deterministic order and caches the decoded bytes. It uses VFS provenance
// for diagnostics (PLAN_01 manifest concept).
func (c *SampleCache) Load(alias string) (*Sample, error) {
	if c == nil {
		return nil, fmt.Errorf("audio: nil cache")
	}
	if strings.TrimSpace(alias) == "" {
		return nil, fmt.Errorf("audio: empty alias")
	}
	if s, ok := c.Get(alias); ok {
		return s, nil
	}
	if c.fs == nil {
		return nil, fmt.Errorf("audio: no VFS for alias %q", alias)
	}
	data, prov, err := c.resolve(alias)
	if err != nil {
		return nil, err
	}
	s, err := decodeSample(alias, data, prov)
	if err != nil {
		return nil, err
	}
	c.putSample(alias, s)
	return s, nil
}

// LoadPath resolves one authored path without prepending the conventional
// sounds/ alias candidates. Stream media carries a path rather than an alias;
// keeping this boundary explicit preserves the authored resource name while
// retaining the session cache and optional-media silence policy.
func (c *SampleCache) LoadPath(path string) (*Sample, error) {
	if c == nil {
		return nil, fmt.Errorf("audio: nil cache")
	}
	if path == "" {
		return nil, fmt.Errorf("audio: empty path")
	}
	if s, ok := c.Get(path); ok {
		return s, nil
	}
	if c.fs == nil {
		return nil, fmt.Errorf("audio: no VFS for path %q", path)
	}
	data, prov, err := c.resolveCandidates([]string{path})
	if err != nil {
		return nil, err
	}
	s, err := decodeSample(path, data, prov)
	if err != nil {
		return nil, err
	}
	c.putSample(path, s)
	return s, nil
}

// loadCandidates resolves an already-registered identity using its authored
// path candidates. The cache key remains the alias, preserving one sample per
// registered identity.
func (c *SampleCache) loadCandidates(alias string, candidates []string) (*Sample, error) {
	if c == nil {
		return nil, fmt.Errorf("audio: nil cache")
	}
	if s, ok := c.Get(alias); ok {
		return s, nil
	}
	if c.fs == nil {
		return nil, fmt.Errorf("audio: no VFS for alias %q", alias)
	}
	data, prov, err := c.resolveCandidates(candidates)
	if err != nil {
		return nil, err
	}
	s, err := decodeSample(alias, data, prov)
	if err != nil {
		return nil, err
	}
	c.putSample(alias, s)
	return s, nil
}

// Purge removes an alias from the cache.
func (c *SampleCache) Purge(alias string) {
	if c == nil {
		return
	}
	k := canonical(alias)
	if _, ok := c.index[k]; !ok {
		return
	}
	delete(c.index, k)
	for i, kk := range c.order {
		if kk == k {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// Clear empties the cache deterministically.
func (c *SampleCache) Clear() {
	if c == nil {
		return
	}
	c.order = nil
	c.index = make(map[string]*Sample)
}

// Aliases returns cached aliases in insertion order (oldest→newest) for tests (I1 stable).
func (c *SampleCache) Aliases() []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// resolve tries VFS candidate paths for alias in deterministic order [02 §2] [03 §8.2] C20.
// Candidate order is stable and case-insensitive (VFS itself is case-insensitive).
func (c *SampleCache) resolve(alias string) ([]byte, vfs.Provenance, error) {
	clean := strings.TrimSpace(alias)
	clean = strings.ReplaceAll(clean, "\\", "/")
	hasWav := strings.HasSuffix(strings.ToLower(clean), ".wav")
	candidates := make([]string, 0, 4)
	candidates = append(candidates, "sounds/"+clean)
	if !hasWav {
		candidates = append(candidates, "sounds/"+clean+".wav")
	}
	candidates = append(candidates, clean)
	if !hasWav {
		candidates = append(candidates, clean+".wav")
	}
	return c.resolveCandidates(candidates)
}

func (c *SampleCache) resolveCandidates(candidates []string) ([]byte, vfs.Provenance, error) {
	var lastErr error
	for _, cand := range candidates {
		if strings.TrimSpace(cand) == "" {
			continue
		}
		// use Open to capture provenance
		f, err := c.fs.Open(cand)
		if err != nil {
			lastErr = err
			continue
		}
		info := f.Info()
		// read with limit 8 MB (retail WAVs far smaller; largest stereo ~8 MB)
		const limit = 8 << 20
		data := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		var total int64
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if total+int64(n) > limit {
					f.Close()
					return nil, vfs.Provenance{}, fmt.Errorf("audio: candidate %q too large", cand)
				}
				data = append(data, buf[:n]...)
				total += int64(n)
			}
			if rerr != nil {
				break
			}
		}
		f.Close()
		if len(data) == 0 {
			lastErr = fmt.Errorf("audio: empty file %q", cand)
			continue
		}
		return data, info.Source, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("not found")
	}
	return nil, vfs.Provenance{}, fmt.Errorf("audio: paths %q not found: %w", candidates, lastErr)
}
