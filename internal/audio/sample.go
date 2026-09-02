package audio

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

// Sample holds decoded PCM for one alias [fmt wav] [03 §8.2] (I13).
// Byte layout is the contract; Go uses named fields (I13).
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

// Decode decodes a WAV-family blob for the given alias [fmt wav] [03 §8.2].
// It accepts RIFF/WAVE PCM, the retail DIGI/HSHD/SDAT container, and raw 8-bit
// mono 11,025 Hz PCM. The ten-byte DIGI wrapper is trimmed when present per
// C20; the 11,000→11,025 Hz remap is applied. RIFF fmt/data chunks honor
// observed odd-byte padding. Raw input defaults to 11,025 Hz mono 8-bit.
func Decode(alias string, data []byte) (*Sample, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("audio: empty sample %q", alias)
	}
	if len(data) > 1<<24 {
		// guard against absurd allocations; retail files are far smaller.
		// This is a divergence for safety; retail would attempt the alloc.
		return nil, fmt.Errorf("audio: sample too large %q (%d bytes)", alias, len(data))
	}
	kind := detectContainer(data)
	switch kind {
	case containerDIGI:
		return decodeDIGI(alias, data)
	case containerRIFF:
		return decodeRIFF(alias, data)
	default:
		return decodeRaw(alias, data)
	}
}

const (
	containerRaw  = 0
	containerDIGI = 1
	containerRIFF = 2
)

// detectContainer checks the fixed legacy markers before RIFF/WAVE [fmt wav]
// [03 §8.2]. 0 raw, 1 DIGI, 2 RIFF/WAVE.
func detectContainer(data []byte) int {
	// The legacy detector is deliberately positional.  In particular, a file
	// beginning with DIGI but carrying a damaged HSHD/SDAT header is classified
	// as raw only when one of these fixed markers is absent; this keeps malformed
	// recognized containers on their decode/error path.
	if len(data) >= 36 && string(data[0:4]) == "DIGI" &&
		string(data[8:12]) == "HSHD" && string(data[32:36]) == "SDAT" {
		return containerDIGI
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return containerRIFF
	}
	return containerRaw
}

// decodeRaw treats the blob as unsigned 8-bit mono 11,025 Hz [fmt wav] [03 §8.2] C20.
func decodeRaw(alias string, data []byte) (*Sample, error) {
	dup := make([]byte, len(data))
	copy(dup, data)
	return &Sample{
		Alias:         alias,
		Container:     "raw",
		AudioFormat:   1,
		Channels:      1,
		SampleRate:    11025, // [fmt wav] raw default [03 §8.2] C20
		ByteRate:      11025,
		BlockAlign:    1,
		BitsPerSample: 8,
		Data:          dup,
	}, nil
}

// decodeRIFF parses RIFF/WAVE PCM with odd padding [fmt wav] [03 §8.2] C20.
// It walks chunks from offset 12, handling size+8 stride plus pad when size is odd.
func decodeRIFF(alias string, data []byte) (*Sample, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("audio: riff too small %q", alias)
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("audio: missing RIFF/WAVE %q", alias)
	}
	var (
		haveFmt       bool
		haveData      bool
		audioFormat   uint16
		channels      uint16
		sampleRate    uint32
		byteRate      uint32
		blockAlign    uint16
		bitsPerSample uint16
		dataOff       int
		dataSize      int
	)
	// Walk chunks starting at 12 [fmt wav] odd pad.
	for off := 12; off+8 <= len(data); {
		chunkID := string(data[off : off+4])
		chunkSize := binary.LittleEndian.Uint32(data[off+4 : off+8])
		payload := off + 8
		if uint64(chunkSize) > uint64(len(data))-uint64(payload) {
			return nil, fmt.Errorf("audio: %s chunk truncated %q", chunkID, alias)
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("audio: fmt too small %q", alias)
			}
			audioFormat = binary.LittleEndian.Uint16(data[payload : payload+2])
			channels = binary.LittleEndian.Uint16(data[payload+2 : payload+4])
			sampleRate = binary.LittleEndian.Uint32(data[payload+4 : payload+8])
			byteRate = binary.LittleEndian.Uint32(data[payload+8 : payload+12])
			blockAlign = binary.LittleEndian.Uint16(data[payload+12 : payload+14])
			bitsPerSample = binary.LittleEndian.Uint16(data[payload+14 : payload+16])
			haveFmt = true
		case "data":
			dataOff = payload
			dataSize = int(chunkSize)
			haveData = true
		}
		// odd size padded to even [fmt wav] [03 §8.2] C20
		size := chunkSize
		if size&1 != 0 {
			size++
		}
		next := payload + int(size)
		if next > len(data) {
			return nil, fmt.Errorf("audio: %s chunk padding truncated %q", chunkID, alias)
		}
		off = next
	}
	if !haveFmt || !haveData || dataSize == 0 {
		return nil, fmt.Errorf("audio: missing fmt or data %q", alias)
	}
	if audioFormat == 0 || channels == 0 || sampleRate == 0 || blockAlign == 0 {
		return nil, fmt.Errorf("audio: invalid fmt %q", alias)
	}
	if audioFormat != 1 {
		// retail corpus is PCM only; reject compressed codecs [fmt wav] unknown
		return nil, fmt.Errorf("audio: unsupported format %d %q", audioFormat, alias)
	}
	if dataOff+dataSize > len(data) {
		return nil, fmt.Errorf("audio: data out of range %q", alias)
	}
	// Validate block align / byte rate for known retail profiles but do not reject
	// mismatched authoring; preserve header as authored.
	if bitsPerSample != 8 && bitsPerSample != 16 {
		return nil, fmt.Errorf("audio: unsupported bits %d %q", bitsPerSample, alias)
	}
	expectedAlign := uint16(channels) * bitsPerSample / 8
	if expectedAlign != blockAlign {
		// divergences from retail authoring are not fatal; keep header value
		// but ensure payload alignment will be checked below
	}
	dup := make([]byte, dataSize)
	copy(dup, data[dataOff:dataOff+dataSize])
	// Retail submits the declared data bytes to the device and truncates a
	// partial final frame.  A misaligned payload is therefore playable rather
	// than a malformed-container failure.
	if rem := len(dup) % int(blockAlign); rem != 0 {
		dup = dup[:len(dup)-rem]
	}
	return &Sample{
		Alias:         alias,
		Container:     "RIFF",
		AudioFormat:   audioFormat,
		Channels:      channels,
		SampleRate:    sampleRate,
		ByteRate:      byteRate,
		BlockAlign:    blockAlign,
		BitsPerSample: bitsPerSample,
		Data:          dup,
	}, nil
}

// decodeDIGI parses the big-endian HSHD+SDAT wrapper [fmt wav] [03 §8.2] C20.
// It remaps 11,000→11,025 Hz and trims the established ten-byte wrapper.
// Provenance of the wrapper/rate location is [00_fnt_pcx_wav.md §3.4] and C20.
func decodeDIGI(alias string, data []byte) (*Sample, error) {
	if len(data) < 32 {
		return nil, fmt.Errorf("audio: digi too small %q", alias)
	}
	if string(data[8:12]) != "HSHD" {
		return nil, fmt.Errorf("audio: digi missing HSHD %q", alias)
	}
	hsz := int(binary.BigEndian.Uint32(data[12:16]))
	if hsz < 8 || 8+hsz > len(data) {
		return nil, fmt.Errorf("audio: digi HSHD out of range %q", alias)
	}
	sdatOff := 8 + hsz
	if sdatOff+8 > len(data) || string(data[sdatOff:sdatOff+4]) != "SDAT" {
		return nil, fmt.Errorf("audio: digi missing SDAT %q", alias)
	}
	sdatSize := int(binary.BigEndian.Uint32(data[sdatOff+4 : sdatOff+8]))
	if sdatSize < 8 || sdatOff+8+sdatSize-8 > len(data) {
		return nil, fmt.Errorf("audio: digi SDAT out of range %q", alias)
	}
	payloadOff := sdatOff + 8
	payloadSize := sdatSize - 8

	// The fixed rate word is at file offset 22 [03 §8.2]. It is little-endian.
	var rate uint32 = 11025 // default per retail
	if len(data) >= 26 {
		rate = binary.LittleEndian.Uint32(data[22:26])
	}
	// [03 §8.2] C20 remap 11,000→11,025 [fmt wav] [GAP T14]
	if rate == 11000 {
		rate = 11025
	}
	// The SDAT payload includes a fixed ten-byte wrapper [03 §8.2].
	if payloadSize < 10 {
		return nil, fmt.Errorf("audio: digi payload negative %q", alias)
	}
	payloadOff += 10
	payloadSize -= 10
	if payloadOff+payloadSize > len(data) {
		return nil, fmt.Errorf("audio: digi payload out of range %q", alias)
	}
	dup := make([]byte, payloadSize)
	copy(dup, data[payloadOff:payloadOff+payloadSize])
	return &Sample{
		Alias:         alias,
		Container:     "DIGI",
		AudioFormat:   1,
		Channels:      1,
		SampleRate:    rate,
		ByteRate:      rate, // mono 8-bit: byteRate == sampleRate [fmt wav]
		BlockAlign:    1,
		BitsPerSample: 8,
		Data:          dup,
	}, nil
}

// SampleCache is the alias→Sample cache [03 §8.2] C20.
//
// Retail has no eviction at all: "samples are cached at the alias level: one
// decoded PCM blob per alias, retained for the life of the session, with no
// eviction beyond the alias cap of §8.3", and the same paragraph names the
// FIFO-255 cache below as "a documented divergence, not retail
// secondary-buffer eviction" [03 §8.2 "Caching"]. So the retained session
// cache is the retail behavior and the bounded FIFO one exists only for
// explicitly bounded test caches; the 255 comes from the [02 "Sound aliases"]
// alias cap, not from a cache size retail authors.

type SampleCache struct {
	fs  vfs.FSOps
	cap int
	// retained is true for the registry-owned session cache.  Explicitly
	// bounded test caches retain the historical eviction helper, while live
	// aliases remain resident until teardown [03 §8.2].
	retained bool
	order    []string // canonical aliases oldest→newest, stable iteration (I1)
	index    map[string]*Sample
}

// SetFS updates VFS resolution for an existing cache without discarding
// already decoded samples or their stable eviction order.
func (c *SampleCache) SetFS(fs vfs.FSOps) {
	if c != nil && fs != nil {
		c.fs = fs
	}
}

// NewCache creates a cache resolving through fs and capped at 255 [GAP T14] C20.
func NewCache(fs vfs.FSOps) *SampleCache {
	return &SampleCache{fs: fs, cap: 255, retained: true, index: make(map[string]*Sample)}
}

// NewCacheWithCap creates a cache with explicit cap for tests. Cap <=0 means 255.
func NewCacheWithCap(fs vfs.FSOps, cap int) *SampleCache {
	if cap <= 0 {
		cap = 255
	}
	return &SampleCache{
		fs:    fs,
		cap:   cap,
		index: make(map[string]*Sample),
	}
}

// NewEvictingCache is a diagnostic/test helper for callers that explicitly
// need a bounded cache. The live alias registry never uses this mode.
func NewEvictingCache(fs vfs.FSOps, cap int) *SampleCache {
	if cap <= 0 {
		cap = 255
	}
	return &SampleCache{fs: fs, cap: cap, index: make(map[string]*Sample)}
}

// Cap returns the capacity.
func (c *SampleCache) Cap() int {
	if c == nil {
		return 0
	}
	return c.cap
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

// Put decodes data and inserts it under alias, evicting oldest if at capacity.
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
	if !c.retained && len(c.order) >= c.cap {
		// deterministic FIFO eviction oldest-first (I1)
		oldest := c.order[0]
		delete(c.index, oldest)
		c.order = c.order[1:]
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
	s, err := Decode(alias, data)
	if err != nil {
		return nil, err
	}
	s.Provenance = prov
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
	s, err := Decode(path, data)
	if err != nil {
		return nil, err
	}
	s.Provenance = prov
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
	s, err := Decode(alias, data)
	if err != nil {
		return nil, err
	}
	s.Provenance = prov
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
