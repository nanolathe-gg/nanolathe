package vfs

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	hpiHeaderSize        = 20
	hpiDirectoryNodeSize = 8
	hpiEntrySize         = 9
	hpiFileRecordSize    = 9
	hpiChunkHeaderSize   = 19
	hpiChunkSize         = 64 * 1024
)

var ErrMalformedArchive = errors.New("vfs: malformed HPI archive")

// ArchiveOptions controls defensive limits while indexing and decoding an
// archive. The defaults are deliberately generous for retail data while
// preventing malformed input from requesting unbounded allocations.
type ArchiveOptions struct {
	MaxDirectoryBytes int64
	MaxFileBytes      int64
	VerifyChecksums   bool
	SkipChecksums     bool
	AllowBank         bool
}

func (o ArchiveOptions) withDefaults() ArchiveOptions {
	if o.MaxDirectoryBytes <= 0 {
		o.MaxDirectoryBytes = 128 << 20
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = 512 << 20
	}
	if !o.SkipChecksums {
		o.VerifyChecksums = true
	}
	return o
}

// Archive is an indexed HPI-family provider. It supports .hpi, .ufo, .ccx,
// .gp3 and other files with the same HAPI container layout.
type Archive struct {
	name           string
	reader         io.ReaderAt
	size           int64
	closer         io.Closer
	options        ArchiveOptions
	key            uint32
	entries        map[string]*providerEntry
	indexedEntries []EntryInfo
}

type hpiRecord struct {
	dataOffset  uint64
	size        uint64
	compression byte
}

// OpenArchive opens an archive from a filesystem path. Header and directory
// bytes are read now; file data is not touched until Open/ReadFile is called.
func OpenArchive(filename string, options ArchiveOptions) (*Archive, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	a, err := NewArchive(filepath.Clean(filename), file, stat.Size(), options)
	if err != nil {
		file.Close()
		return nil, err
	}
	a.closer = file
	return a, nil
}

// NewArchive indexes an archive from any ReaderAt, which also supports
// archives extracted from another archive without coupling the VFS to files.
func NewArchive(name string, reader io.ReaderAt, size int64, options ArchiveOptions) (*Archive, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrMalformedArchive)
	}
	if size < hpiHeaderSize {
		return nil, fmt.Errorf("%w: archive is too small", ErrMalformedArchive)
	}
	a := &Archive{name: name, reader: reader, size: size, options: options.withDefaults(), entries: make(map[string]*providerEntry)}
	if err := a.index(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Archive) Close() error {
	if a.closer == nil {
		return nil
	}
	err := a.closer.Close()
	a.closer = nil
	return err
}

func (a *Archive) lookup(name string) (*providerEntry, bool) {
	entry, ok := a.entries[name]
	return entry, ok
}

func (a *Archive) children(parent string) []*providerEntry {
	result := make([]*providerEntry, 0)
	prefix := parent
	if prefix != "" {
		prefix += "/"
	}
	for name, entry := range a.entries {
		if name == "" || !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if rest != "" && !strings.Contains(rest, "/") {
			result = append(result, entry)
		}
	}
	return result
}

func (a *Archive) close() error { return a.Close() }

func (a *Archive) setMountInfo(priority, order int) {
	for _, entry := range a.entries {
		entry.info.Source.Priority = priority
		entry.info.Source.MountOrder = order
	}
	for i := range a.indexedEntries {
		a.indexedEntries[i].Source.Priority = priority
		a.indexedEntries[i].Source.MountOrder = order
	}
}

func (a *Archive) index() error {
	header := make([]byte, hpiHeaderSize)
	if err := readAtFull(a.reader, 0, header); err != nil {
		return fmt.Errorf("%w: header: %v", ErrMalformedArchive, err)
	}
	if string(header[0:4]) != "HAPI" {
		return fmt.Errorf("%w: marker %q", ErrMalformedArchive, header[0:4])
	}
	version := binary.LittleEndian.Uint32(header[4:8])
	if version != 0x00010000 && !(a.options.AllowBank && version == 0x4B4E4142) {
		return fmt.Errorf("%w: unsupported version 0x%08x", ErrMalformedArchive, version)
	}
	// Footer: retail seeks to end and requires trailing "Copyright ... Cavedog Entertainment".
	// Real archives use 1997/1998 but GAP-ANALYSIS normalizes to 0000. Validate suffix/prefix tolerant to year.
	if version == 0x00010000 {
		const footerSuffix = "Cavedog Entertainment"
		const footerPrefix = "Copyright"
		footerCheckLen := 64
		if int64(footerCheckLen) > a.size {
			footerCheckLen = int(a.size)
		}
		tail := make([]byte, footerCheckLen)
		if err := readAtFull(a.reader, a.size-int64(footerCheckLen), tail); err != nil {
			return fmt.Errorf("%w: footer: %v", ErrMalformedArchive, err)
		}
		tailStr := string(tail)
		if !strings.HasSuffix(strings.TrimRight(tailStr, "\x00"), footerSuffix) {
			// Fallback: check any suffix match within tail window
			if !strings.Contains(tailStr, footerSuffix) {
				return fmt.Errorf("%w: missing footer %q", ErrMalformedArchive, footerSuffix)
			}
		}
		if !strings.Contains(tailStr, footerPrefix) {
			return fmt.Errorf("%w: missing footer %q", ErrMalformedArchive, footerPrefix)
		}
	}
	blobSize := uint64(binary.LittleEndian.Uint32(header[8:12]))
	headerKeyWord := binary.LittleEndian.Uint32(header[12:16])
	rootOffset := uint64(binary.LittleEndian.Uint32(header[16:20]))
	// Retail validates tag/version/footer and reads directory-blob size bytes
	// from offset 0 so the blob contains the header (02:128). The version word
	// is the byte pattern 00 00 01 00, checked as LE 0x00010000.
	if blobSize < hpiHeaderSize || blobSize > uint64(a.size) {
		return fmt.Errorf("%w: invalid directory blob size 0x%x", ErrMalformedArchive, blobSize)
	}
	if rootOffset < hpiHeaderSize || rootOffset >= blobSize {
		return fmt.Errorf("%w: invalid root offset 0x%x", ErrMalformedArchive, rootOffset)
	}
	if blobSize > uint64(a.options.MaxDirectoryBytes) || blobSize > uint64(math.MaxInt) {
		return fmt.Errorf("%w: directory is too large", ErrMalformedArchive)
	}
	blob := make([]byte, int(blobSize))
	if err := readAtFull(a.reader, 0, blob); err != nil {
		return fmt.Errorf("%w: directory: %v", ErrMalformedArchive, err)
	}
	// Key derivation uses only the low byte (02:132-135). Stored 0 means plain.
	if headerKeyWord != 0 {
		keyByte := byte(headerKeyWord & 0xFF)
		derived := byte((keyByte >> 6) | (keyByte << 2))
		workingKeyByte := ^derived
		workingKey := uint32(workingKeyByte)
		// Transform every byte from offset 0x14 onward: plain = ((pos)&FF) XOR key XOR NOT cipher (02:140).
		if len(blob) > hpiHeaderSize {
			decrypt(blob[hpiHeaderSize:], hpiHeaderSize, workingKey)
		}
		a.key = workingKey
		// Keep header key byte in blob consistent with retail (overwrites stored byte).
		blob[12] = byte(workingKey)
		blob[13] = 0
		blob[14] = 0
		blob[15] = 0
	}

	view := hpiDirectoryView{archive: a, bytes: blob, start: 0, end: blobSize}
	root := EntryInfo{Path: "", Name: "", IsDir: true, OriginalPath: "", Source: Provenance{ProviderType: "hpi", SourcePath: a.name, MountOrder: 0}}
	a.entries[""] = &providerEntry{info: root}
	stack := make(map[uint64]bool)
	if err := view.walkDirectory(rootOffset, "", "", stack); err != nil {
		return err
	}
	return nil
}

type hpiDirectoryView struct {
	archive *Archive
	bytes   []byte
	start   uint64
	end     uint64
}

func (v hpiDirectoryView) offset(pos uint64, length uint64) (int, error) {
	if pos < v.start || length > v.end-pos {
		return 0, fmt.Errorf("%w: directory pointer 0x%x length %d", ErrMalformedArchive, pos, length)
	}
	index := pos - v.start
	if index > uint64(math.MaxInt) {
		return 0, fmt.Errorf("%w: pointer overflow", ErrMalformedArchive)
	}
	return int(index), nil
}

func (v hpiDirectoryView) u32(pos uint64) (uint32, error) {
	index, err := v.offset(pos, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(v.bytes[index : index+4]), nil
}

func (v hpiDirectoryView) cstring(pos uint64) (string, error) {
	index, err := v.offset(pos, 1)
	if err != nil {
		return "", err
	}
	end := bytes.IndexByte(v.bytes[index:], 0)
	if end < 0 {
		return "", fmt.Errorf("%w: unterminated name at 0x%x", ErrMalformedArchive, pos)
	}
	return string(v.bytes[index : index+end]), nil
}

func (v hpiDirectoryView) walkDirectory(nodeOffset uint64, parent, originalParent string, stack map[uint64]bool) error {
	if stack[nodeOffset] {
		return fmt.Errorf("%w: directory cycle at 0x%x", ErrMalformedArchive, nodeOffset)
	}
	stack[nodeOffset] = true
	defer delete(stack, nodeOffset)
	count, err := v.u32(nodeOffset)
	if err != nil {
		return err
	}
	listOffset, err := v.u32(nodeOffset + 4)
	if err != nil {
		return err
	}
	if uint64(listOffset) < v.start || uint64(listOffset) > v.end || count > uint32((v.end-uint64(listOffset))/hpiEntrySize) {
		return fmt.Errorf("%w: entry list overflows directory", ErrMalformedArchive)
	}
	for i := uint32(0); i < count; i++ {
		entryOffset := uint64(listOffset) + uint64(i)*hpiEntrySize
		nameOffset, err := v.u32(entryOffset)
		if err != nil {
			return err
		}
		dataOffset, err := v.u32(entryOffset + 4)
		if err != nil {
			return err
		}
		flagIndex, err := v.offset(entryOffset+8, 1)
		if err != nil {
			return err
		}
		name, err := v.cstring(uint64(nameOffset))
		if err != nil {
			return err
		}
		logical, err := joinPath(parent, name)
		if err != nil {
			return fmt.Errorf("%w: entry %q: %v", ErrMalformedArchive, name, err)
		}
		original := originalJoin(originalParent, name)
		if v.bytes[flagIndex] == 1 {
			info := EntryInfo{Path: logical, Name: name, IsDir: true, OriginalPath: original,
				Source: Provenance{LogicalPath: logical, OriginalPath: original, ProviderType: "hpi", SourcePath: v.archive.name}}
			v.archive.entries[logical] = &providerEntry{info: info}
			v.archive.indexedEntries = append(v.archive.indexedEntries, info)
			if err := v.walkDirectory(uint64(dataOffset), logical, original, stack); err != nil {
				return err
			}
			continue
		}
		if v.bytes[flagIndex] != 0 {
			return fmt.Errorf("%w: entry %q has invalid flag %d", ErrMalformedArchive, name, v.bytes[flagIndex])
		}
		recordOffset := uint64(dataOffset)
		data, err := v.u32(recordOffset)
		if err != nil {
			return err
		}
		size, err := v.u32(recordOffset + 4)
		if err != nil {
			return err
		}
		compressionIndex, err := v.offset(recordOffset+8, 1)
		if err != nil {
			return err
		}
		compression := v.bytes[compressionIndex]
		if compression > 2 {
			return fmt.Errorf("%w: file %q has compression %d", ErrMalformedArchive, logical, compression)
		}
		if uint64(size) > uint64(v.archive.options.MaxFileBytes) {
			return fmt.Errorf("%w: file %q exceeds size limit", ErrMalformedArchive, logical)
		}
		record := hpiRecord{dataOffset: uint64(data), size: uint64(size), compression: compression}
		if compression == 0 && (record.dataOffset > uint64(v.archive.size) || record.size > uint64(v.archive.size)-record.dataOffset) {
			return fmt.Errorf("%w: stored file %q is outside archive", ErrMalformedArchive, logical)
		}
		info := EntryInfo{Path: logical, Name: name, Size: int64(size), OriginalPath: original,
			Source: Provenance{LogicalPath: logical, OriginalPath: original, ProviderType: "hpi", SourcePath: v.archive.name, Compression: compressionName(compression)}}
		entry := &providerEntry{info: info}
		entry.open = func() (File, error) { return v.archive.openRecord(record, info) }
		// Retail data does not define duplicate-path behavior within one archive.
		// Keep the last directory entry, matching the deterministic overlay rule.
		v.archive.entries[logical] = entry
		v.archive.indexedEntries = append(v.archive.indexedEntries, info)
	}
	return nil
}

func (a *Archive) auditEntries() []EntryInfo {
	return append([]EntryInfo(nil), a.indexedEntries...)
}

func compressionName(compression byte) string {
	switch compression {
	case 0:
		return "stored"
	case 1:
		return "lz77"
	case 2:
		return "zlib"
	default:
		return "unknown"
	}
}

func (a *Archive) openRecord(record hpiRecord, info EntryInfo) (File, error) {
	data, err := a.readRecord(record)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(data)
	return &fileHandle{reader: reader, readerAt: reader, closeFn: func() error { return nil }, info: info}, nil
}

func (a *Archive) readRecord(record hpiRecord) ([]byte, error) {
	if record.size > uint64(a.options.MaxFileBytes) || record.size > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w: file exceeds size limit", ErrMalformedArchive)
	}
	if record.compression == 0 {
		data := make([]byte, int(record.size))
		if err := a.readArchiveBytes(record.dataOffset, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	chunkCount := (record.size + hpiChunkSize - 1) / hpiChunkSize
	if chunkCount > uint64(math.MaxInt/4) {
		return nil, fmt.Errorf("%w: chunk table too large", ErrMalformedArchive)
	}
	table := make([]byte, int(chunkCount*4))
	if err := a.readArchiveBytes(record.dataOffset, table); err != nil {
		return nil, err
	}
	result := make([]byte, int(record.size))
	position := record.dataOffset + uint64(len(table))
	output := 0
	for chunk := uint64(0); chunk < chunkCount; chunk++ {
		storedSize := uint64(binary.LittleEndian.Uint32(table[chunk*4 : chunk*4+4]))
		if storedSize < hpiChunkHeaderSize || storedSize > uint64(a.size)-position {
			return nil, fmt.Errorf("%w: invalid chunk %d size", ErrMalformedArchive, chunk)
		}
		encoded := make([]byte, int(storedSize))
		if err := a.readArchiveBytes(position, encoded); err != nil {
			return nil, err
		}
		position += storedSize
		if string(encoded[0:4]) != "SQSH" {
			return nil, fmt.Errorf("%w: chunk %d marker", ErrMalformedArchive, chunk)
		}
		method := encoded[5]
		if method != record.compression {
			return nil, fmt.Errorf("%w: chunk %d compression mismatch", ErrMalformedArchive, chunk)
		}
		payloadSize := uint64(binary.LittleEndian.Uint32(encoded[7:11]))
		decompressedSize := uint64(binary.LittleEndian.Uint32(encoded[11:15]))
		checksum := binary.LittleEndian.Uint32(encoded[15:19])
		if payloadSize+hpiChunkHeaderSize != storedSize || decompressedSize > record.size-uint64(output) {
			return nil, fmt.Errorf("%w: chunk %d size fields", ErrMalformedArchive, chunk)
		}
		payload := encoded[hpiChunkHeaderSize:]
		var sum uint32
		for _, value := range payload {
			sum += uint32(value)
		}
		if a.options.VerifyChecksums && sum != checksum {
			return nil, fmt.Errorf("%w: chunk %d checksum", ErrMalformedArchive, chunk)
		}
		if encoded[6] != 0 {
			for i := range payload {
				payload[i] = byte(uint16(payload[i])-uint16(i)) ^ byte(i)
			}
		}
		var decoded []byte
		var err error
		switch method {
		case 1:
			decoded, err = decodeLZ77(payload, decompressedSize)
		case 2:
			decoded, err = decodeZlib(payload, decompressedSize)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: chunk %d: %v", ErrMalformedArchive, chunk, err)
		}
		if uint64(len(decoded)) != decompressedSize {
			return nil, fmt.Errorf("%w: chunk %d output size", ErrMalformedArchive, chunk)
		}
		copy(result[output:], decoded)
		output += len(decoded)
	}
	if output != len(result) {
		return nil, fmt.Errorf("%w: decompressed size mismatch", ErrMalformedArchive)
	}
	return result, nil
}

func (a *Archive) readArchiveBytes(offset uint64, data []byte) error {
	if offset > uint64(a.size) || uint64(len(data)) > uint64(a.size)-offset {
		return fmt.Errorf("%w: file data outside archive", ErrMalformedArchive)
	}
	if err := readAtFull(a.reader, int64(offset), data); err != nil {
		return err
	}
	if a.key != 0 {
		decrypt(data, offset, a.key)
	}
	return nil
}

func readAtFull(reader io.ReaderAt, offset int64, data []byte) error {
	read, err := reader.ReadAt(data, offset)
	if err != nil && !(err == io.EOF && read == len(data)) {
		return err
	}
	if read != len(data) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func decrypt(data []byte, absoluteOffset uint64, key uint32) {
	for i := range data {
		position := uint32(absoluteOffset + uint64(i))
		data[i] = byte(position^key) ^ ^data[i]
	}
}

func decodeZlib(payload []byte, expected uint64) ([]byte, error) {
	if expected > uint64(math.MaxInt) {
		return nil, errors.New("zlib output is too large")
	}
	reader, err := zlib.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	limited := io.LimitReader(reader, int64(expected)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) != expected {
		return nil, fmt.Errorf("zlib produced %d bytes, expected %d", len(data), expected)
	}
	return data, nil
}

func decodeLZ77(payload []byte, expected uint64) ([]byte, error) {
	if expected > uint64(math.MaxInt) {
		return nil, errors.New("LZ77 output is too large")
	}
	output := make([]byte, 0, int(expected))
	var window [4096]byte
	write := 1
	position := 0
	for uint64(len(output)) < expected {
		if position >= len(payload) {
			return nil, errors.New("LZ77 tag is truncated")
		}
		tag := payload[position]
		position++
		for bit := 0; bit < 8 && uint64(len(output)) < expected; bit++ {
			if tag&(1<<bit) == 0 {
				if position >= len(payload) {
					return nil, errors.New("LZ77 literal is truncated")
				}
				if err := lzAppend(&output, &window, &write, payload[position], expected); err != nil {
					return nil, err
				}
				position++
				continue
			}
			if position+1 >= len(payload) {
				return nil, errors.New("LZ77 match is truncated")
			}
			word := binary.LittleEndian.Uint16(payload[position : position+2])
			position += 2
			match := int(word >> 4)
			length := int(word&0x0f) + 2
			if match == 0 {
				if uint64(len(output)) != expected {
					return nil, errors.New("LZ77 terminator before expected output")
				}
				return output, nil
			}
			for i := 0; i < length; i++ {
				if err := lzAppend(&output, &window, &write, window[match], expected); err != nil {
					return nil, err
				}
				match = (match + 1) & 0x0fff
			}
		}
	}
	return output, nil
}

func lzAppend(output *[]byte, window *[4096]byte, write *int, value byte, expected uint64) error {
	if uint64(len(*output)) >= expected {
		return errors.New("LZ77 output exceeds expected size")
	}
	*output = append(*output, value)
	window[*write] = value
	*write = (*write + 1) & 0x0fff
	return nil
}
