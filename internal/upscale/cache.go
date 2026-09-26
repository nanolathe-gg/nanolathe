package upscale

// The on-disk result cache of DESIGN_GPU_RENDERER §14.4. Synthesis costs
// seconds per map and a fraction of a second per bank; the result is a pure
// function of its inputs, so the second load of a map is a file read.
//
// The cache holds derived retail art. It lives in the XDG cache directory,
// is never committed and is never shipped.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/formats"
)

const (
	// cacheFormatVersion names the directory. Bump it when the file layout
	// changes, which retires every earlier file without deleting anything.
	cacheFormatVersion = "1"
	// cacheMagic opens every cache file, so a truncated or foreign file is
	// recognised as unusable rather than decoded as art.
	cacheMagic = "NLUPSCALE"
	// cacheFileVersion is the payload version inside a file.
	cacheFileVersion = uint32(1)

	cacheKindTiles = uint32(1)
	cacheKindBank  = uint32(2)

	// The algorithm versions enter the key, so changing what the
	// synthesizers compute retires the results that depended on the old
	// behaviour instead of returning them.
	terrainAlgorithmVersion = "nanolathe.upscale.terrain.1"
	bankAlgorithmVersion    = "nanolathe.upscale.bank.2" // 2: skip no longer removes example entries
)

// Cache is a directory of synthesis results keyed by their inputs. The zero
// value is unusable; build one with DefaultCache or set Dir yourself.
type Cache struct{ Dir string }

// DefaultCache uses the same cross-platform XDG layout as settings and mods:
// $XDG_CACHE_HOME/nanolathe/upscale/<format version>, else ~/.cache/nanolathe/...
func DefaultCache() (*Cache, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("nanolathe: upscale cache: no home directory: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return &Cache{Dir: filepath.Join(base, "nanolathe", "upscale", cacheFormatVersion)}, nil
}

// Tiles2x returns the detail tile set for in, from the cache when a result for
// exactly these inputs is stored and by synthesizing it otherwise. cached says
// which happened. A stored file that will not parse is recomputed and
// rewritten; a cache that cannot be written is reported on stderr and the
// computed result is still returned.
func (c *Cache) Tiles2x(in TerrainInput, opts Options) (tiles [][4096]byte, cached bool, err error) {
	key := terrainKey(in)
	if payload, ok := c.load(key, cacheKindTiles); ok {
		if tiles, ok := decodeTiles(payload); ok {
			return tiles, true, nil
		}
	}
	tiles, err = Tiles2x(in, opts)
	if err != nil {
		return nil, false, err
	}
	c.store(key, cacheKindTiles, encodeTiles(tiles))
	return tiles, false, nil
}

// Bank2x returns the 2x sprite bank for query, from the cache when a result
// for exactly these inputs is stored and by synthesizing it otherwise. The
// arguments and the returned bank are those of the package-level Bank2x.
func (c *Cache) Bank2x(query *formats.GAF, examples []*formats.GAF, pal [256][3]uint8, alp []byte,
	skip func(string) bool, opts Options) (bank *formats.GAF, cached bool, err error) {
	key := bankKey(query, examples, pal, alp, skip)
	if payload, ok := c.load(key, cacheKindBank); ok {
		if bank, ok := decodeBank(payload); ok {
			return bank, true, nil
		}
	}
	bank, err = Bank2x(query, examples, pal, alp, skip, opts)
	if err != nil {
		return nil, false, err
	}
	c.store(key, cacheKindBank, encodeBank(bank))
	return bank, false, nil
}

func (c *Cache) path(key string) (string, error) {
	if c == nil || c.Dir == "" {
		return "", errors.New("nanolathe: upscale cache: no directory configured")
	}
	// key is hex from a hash, so it can never escape Dir.
	return filepath.Join(c.Dir, key), nil
}

func (c *Cache) load(key string, kind uint32) ([]byte, bool) {
	path, err := c.path(key)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	header := len(cacheMagic) + 8
	if len(data) < header || string(data[:len(cacheMagic)]) != cacheMagic {
		return nil, false
	}
	if binary.LittleEndian.Uint32(data[len(cacheMagic):]) != kind {
		return nil, false
	}
	if binary.LittleEndian.Uint32(data[len(cacheMagic)+4:]) != cacheFileVersion {
		return nil, false
	}
	return data[header:], true
}

// store writes one result. A failure here costs a recomputation next time and
// nothing else, so it is reported rather than returned.
func (c *Cache) store(key string, kind uint32, payload []byte) {
	path, err := c.path(key)
	if err == nil {
		err = writeCacheFile(path, kind, payload)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: upscale cache write failed: logical path %s, providers searched [%s], expected a stored synthesis: %v\n",
			key, c.Dir, err)
	}
}

func writeCacheFile(path string, kind uint32, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	header := make([]byte, len(cacheMagic)+8)
	copy(header, cacheMagic)
	binary.LittleEndian.PutUint32(header[len(cacheMagic):], kind)
	binary.LittleEndian.PutUint32(header[len(cacheMagic)+4:], cacheFileVersion)
	_, writeErr := file.Write(header)
	if writeErr == nil {
		_, writeErr = file.Write(payload)
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(file.Name())
		return errors.Join(writeErr, closeErr)
	}
	// The rename is atomic, so a concurrent reader sees either no file or a
	// whole one.
	if err := os.Rename(file.Name(), path); err != nil {
		os.Remove(file.Name())
		return err
	}
	return nil
}

// The keys below frame every field with its length, so two different input
// sets cannot produce the same byte stream.

func terrainKey(in TerrainInput) string {
	digest := sha256.New()
	writeChunk := func(b []byte) {
		var length [8]byte
		binary.LittleEndian.PutUint64(length[:], uint64(len(b)))
		digest.Write(length[:])
		digest.Write(b)
	}
	writeChunk([]byte(terrainAlgorithmVersion))
	var dims [16]byte
	binary.LittleEndian.PutUint64(dims[0:], uint64(in.TilesW))
	binary.LittleEndian.PutUint64(dims[8:], uint64(in.TilesH))
	writeChunk(dims[:])
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(len(in.Tiles)))
	writeChunk(count[:])
	for index := range in.Tiles {
		digest.Write(in.Tiles[index][:])
	}
	placements := make([]byte, 2*len(in.TileMap))
	for index, id := range in.TileMap {
		binary.LittleEndian.PutUint16(placements[2*index:], id)
	}
	writeChunk(placements)
	writeChunk(flatPalette(in.Palette))
	writeChunk(in.ALP)
	return hex.EncodeToString(digest.Sum(nil))
}

func bankKey(query *formats.GAF, examples []*formats.GAF, pal [256][3]uint8, alp []byte,
	skip func(string) bool) string {
	digest := sha256.New()
	var scratch [16]byte
	writeChunk := func(b []byte) {
		binary.LittleEndian.PutUint64(scratch[:8], uint64(len(b)))
		digest.Write(scratch[:8])
		digest.Write(b)
	}
	writeNumber := func(value uint64) {
		binary.LittleEndian.PutUint64(scratch[:8], value)
		digest.Write(scratch[:8])
	}
	hashBank := func(bank *formats.GAF) {
		if bank == nil {
			writeNumber(0)
			return
		}
		writeNumber(uint64(len(bank.Entries)) + 1)
		for entryIndex := range bank.Entries {
			entry := &bank.Entries[entryIndex]
			writeChunk([]byte(entry.Name))
			if skip != nil && skip(entry.Name) {
				writeNumber(1)
			} else {
				writeNumber(0)
			}
			writeNumber(uint64(len(entry.Frames)))
			for _, ref := range entry.Frames {
				frame := ref.Frame
				if frame == nil {
					writeNumber(0)
					continue
				}
				writeNumber(1)
				binary.LittleEndian.PutUint16(scratch[0:], frame.Width)
				binary.LittleEndian.PutUint16(scratch[2:], frame.Height)
				binary.LittleEndian.PutUint16(scratch[4:], uint16(frame.XOffset))
				binary.LittleEndian.PutUint16(scratch[6:], uint16(frame.YOffset))
				scratch[8] = frame.ColorKey
				scratch[9] = frame.SubframeCount
				scratch[10] = frame.AlternateBlitter
				digest.Write(scratch[:11])
				writeChunk(frame.Pixels)
				writeChunk(packBits(frame.Transparent))
			}
		}
	}
	writeChunk([]byte(bankAlgorithmVersion))
	hashBank(query)
	writeNumber(uint64(len(examples)))
	for _, bank := range examples {
		hashBank(bank)
	}
	writeChunk(flatPalette(pal))
	writeChunk(alp)
	return hex.EncodeToString(digest.Sum(nil))
}

func packBits(values []bool) []byte {
	packed := make([]byte, (len(values)+7)/8)
	for index, value := range values {
		if value {
			packed[index/8] |= 1 << (index % 8)
		}
	}
	return packed
}

func unpackBits(packed []byte, count int) ([]bool, bool) {
	if len(packed) != (count+7)/8 {
		return nil, false
	}
	values := make([]bool, count)
	for index := range values {
		values[index] = packed[index/8]&(1<<(index%8)) != 0
	}
	return values, true
}

func encodeTiles(tiles [][4096]byte) []byte {
	out := make([]byte, 4, 4+4096*len(tiles))
	binary.LittleEndian.PutUint32(out, uint32(len(tiles)))
	for index := range tiles {
		out = append(out, tiles[index][:]...)
	}
	return out
}

func decodeTiles(payload []byte) ([][4096]byte, bool) {
	if len(payload) < 4 {
		return nil, false
	}
	count := int(binary.LittleEndian.Uint32(payload))
	body := payload[4:]
	if count < 0 || len(body) != count*4096 {
		return nil, false
	}
	tiles := make([][4096]byte, count)
	for index := range tiles {
		copy(tiles[index][:], body[index*4096:])
	}
	return tiles, true
}

// encodeBank writes the parallel bank: entry names and frame counts, then each
// present frame's geometry, pixels and packed transparency. Nothing else of a
// GAF survives a synthesis, so nothing else is stored.
func encodeBank(bank *formats.GAF) []byte {
	var out []byte
	var scratch [16]byte
	number := func(value uint32) {
		binary.LittleEndian.PutUint32(scratch[:4], value)
		out = append(out, scratch[:4]...)
	}
	chunk := func(b []byte) {
		number(uint32(len(b)))
		out = append(out, b...)
	}
	number(bank.Version)
	number(bank.EntryCount)
	number(bank.Unknown)
	number(uint32(len(bank.Entries)))
	for entryIndex := range bank.Entries {
		entry := &bank.Entries[entryIndex]
		chunk([]byte(entry.Name))
		number(uint32(entry.FrameCount))
		number(uint32(entry.Unknown1))
		number(entry.Unknown2)
		number(uint32(len(entry.Frames)))
		for _, ref := range entry.Frames {
			if ref.Frame == nil {
				number(0)
				continue
			}
			number(1)
			number(uint32(ref.Frame.Width))
			number(uint32(ref.Frame.Height))
			number(uint32(uint16(ref.Frame.XOffset)))
			number(uint32(uint16(ref.Frame.YOffset)))
			number(uint32(ref.Frame.ColorKey))
			chunk(ref.Frame.Pixels)
			chunk(packBits(ref.Frame.Transparent))
		}
	}
	return out
}

const (
	// The fewest bytes encodeBank can spend on one record, used to reject a
	// count the rest of the file cannot possibly satisfy. An entry is an empty
	// name chunk plus its four words; a frame reference is at least its
	// one-word present flag.
	minEncodedEntryBytes = 4 + 4*4
	minEncodedFrameBytes = 4
)

func decodeBank(payload []byte) (*formats.GAF, bool) {
	cursor := 0
	number := func() (uint32, bool) {
		if cursor+4 > len(payload) {
			return 0, false
		}
		value := binary.LittleEndian.Uint32(payload[cursor:])
		cursor += 4
		return value, true
	}
	chunk := func() ([]byte, bool) {
		length, ok := number()
		if !ok || cursor+int(length) > len(payload) {
			return nil, false
		}
		value := payload[cursor : cursor+int(length)]
		cursor += int(length)
		return value, true
	}
	// A count read from the file is only ever used to size a slice after the
	// remaining bytes are shown to be able to hold that many records, the way
	// decodeTiles checks its length first. The cache is a file under the user's
	// cache directory: a corrupt or tampered count must land on the caller's
	// recompute path, not on a multi-gigabyte reservation.
	fits := func(count uint32, minBytesEach int) bool {
		return int64(count)*int64(minBytesEach) <= int64(len(payload)-cursor)
	}
	version, ok1 := number()
	entryCount, ok2 := number()
	unknown, ok3 := number()
	entries, ok4 := number()
	if !ok1 || !ok2 || !ok3 || !ok4 || !fits(entries, minEncodedEntryBytes) {
		return nil, false
	}
	bank := &formats.GAF{Version: version, EntryCount: entryCount, Unknown: unknown,
		Entries: make([]formats.GAFEntry, entries)}
	for entryIndex := range bank.Entries {
		name, ok := chunk()
		if !ok {
			return nil, false
		}
		frameCount, ok1 := number()
		unknown1, ok2 := number()
		unknown2, ok3 := number()
		frames, ok4 := number()
		if !ok1 || !ok2 || !ok3 || !ok4 || !fits(frames, minEncodedFrameBytes) {
			return nil, false
		}
		entry := &bank.Entries[entryIndex]
		entry.Name = string(name)
		entry.FrameCount = uint16(frameCount)
		entry.Unknown1 = uint16(unknown1)
		entry.Unknown2 = unknown2
		entry.Frames = make([]formats.GAFFrameRef, frames)
		for frameIndex := range entry.Frames {
			present, ok := number()
			if !ok {
				return nil, false
			}
			if present == 0 {
				continue
			}
			width, ok1 := number()
			height, ok2 := number()
			x, ok3 := number()
			y, ok4 := number()
			key, ok5 := number()
			if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
				return nil, false
			}
			pixels, ok := chunk()
			if !ok {
				return nil, false
			}
			packed, ok := chunk()
			if !ok {
				return nil, false
			}
			if len(pixels) != int(width)*int(height) {
				return nil, false
			}
			transparent, ok := unpackBits(packed, len(pixels))
			if !ok {
				return nil, false
			}
			frame := &formats.GAFFrame{
				Width: uint16(width), Height: uint16(height),
				XOffset: int16(uint16(x)), YOffset: int16(uint16(y)),
				ColorKey: uint8(key), Compressed: 0,
				Pixels: append([]byte(nil), pixels...), Transparent: transparent,
			}
			frame.PlainPixels = frame.Pixels
			frame.PlainTransparent = frame.Transparent
			entry.Frames[frameIndex].Frame = frame
		}
	}
	return bank, cursor == len(payload)
}
