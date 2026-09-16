package vfs

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
)

const (
	hpiHeaderSize        = 20
	hpiDirectoryNodeSize = 8
	hpiEntrySize         = 9
	hpiFileRecordSize    = 9
	hpiChunkHeaderSize   = 19
	// defaultMaxFileBytes is the ceiling for a single decoded file.
	defaultMaxFileBytes = 512 << 20
	hpiChunkSize        = 64 * 1024
)

// ErrMalformedArchive reports an archive whose header, directory blob or
// chunk stream does not decode.
var ErrMalformedArchive = errors.New("vfs: malformed HPI archive")

// ErrRejectedArchive identifies a failed container gate, including safely
// rejected short header/footer reads [02 §2]. Directory and payload failures
// remain ErrMalformedArchive without this classification.
var ErrRejectedArchive = fmt.Errorf("%w: rejected container", ErrMalformedArchive)

// ArchiveOptions controls defensive limits while indexing and decoding an
// archive. The defaults are deliberately generous for retail data while
// preventing malformed input from requesting unbounded allocations.
type ArchiveOptions struct {
	MaxDirectoryBytes int64
	MaxFileBytes      int64
	VerifyChecksums   bool
	SkipChecksums     bool
	AllowBank         bool
	// Directory expansion limits are host safety policy, not retail limits.
	// Nonpositive values select defaults. Entries include repeated references
	// and hidden duplicates; depth includes the root and empty directories.
	MaxDirectoryEntries int64
	MaxDirectoryDepth   int
	// MaxDirectoryStringBytes counts name bytes scanned (including each NUL)
	// and both joined path lengths before normalization or materialization.
	MaxDirectoryStringBytes int64
}

func (o ArchiveOptions) withDefaults() ArchiveOptions {
	if o.MaxDirectoryBytes <= 0 {
		o.MaxDirectoryBytes = 128 << 20
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = defaultMaxFileBytes
	}
	// These defaults leave at least 33x entry, 16x depth and 128x string-work
	// headroom over the reference install; see DESIGN_CONTENT_VFS §5.
	if o.MaxDirectoryEntries <= 0 {
		o.MaxDirectoryEntries = 1 << 16
	}
	if o.MaxDirectoryDepth <= 0 {
		o.MaxDirectoryDepth = 64
	}
	if o.MaxDirectoryStringBytes <= 0 {
		o.MaxDirectoryStringBytes = 16 << 20
	}
	if !o.SkipChecksums {
		o.VerifyChecksums = true
	}
	return o
}

// Archive is an indexed HPI-family provider. It supports .hpi, .ufo, .ccx,
// .gp3 and other files with the same HAPI container layout.
type Archive struct {
	name    string
	reader  io.ReaderAt
	size    int64
	closer  io.Closer
	options ArchiveOptions
	key     uint32
	providerIndex
	indexedEntries []EntryInfo
	// Enumeration retains source order, but only beneath reachable directories.
	retailEntryIndices []int
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
		return nil, fmt.Errorf("%w: archive is too small", ErrRejectedArchive)
	}
	a := &Archive{name: name, reader: reader, size: size, options: options.withDefaults(), providerIndex: newProviderIndex()}
	if err := a.index(); err != nil {
		return nil, err
	}
	return a, nil
}

// Close releases the archive's file handle when the archive opened one.
// An archive built over a caller-supplied ReaderAt owns nothing to close.
func (a *Archive) Close() error {
	if a.closer == nil {
		return nil
	}
	err := a.closer.Close()
	a.closer = nil
	return err
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
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: header: %v", ErrRejectedArchive, err)
		}
		return fmt.Errorf("%w: header: %v", ErrMalformedArchive, err)
	}
	if string(header[0:4]) != "HAPI" {
		return fmt.Errorf("%w: marker %q", ErrRejectedArchive, header[0:4])
	}
	version := binary.LittleEndian.Uint32(header[4:8])
	if version != 0x00010000 && !(a.options.AllowBank && version == 0x4B4E4142) {
		return fmt.Errorf("%w: unsupported version 0x%08x", ErrRejectedArchive, version)
	}
	// Footer: retail seeks to the end of the file, reads the 36 trailing
	// bytes, overwrites the four edition bytes with literal "0000", and
	// requires the normalized footer to equal the template. The accepted shape
	// is therefore "Copyright <any four bytes> Cavedog Entertainment" with no
	// digit check, and a mismatch rejects the archive before it enters the
	// mount list [02 §2].
	if version == 0x00010000 {
		const footerTemplate = "Copyright 0000 Cavedog Entertainment"
		const editionStart, editionEnd = 10, 14
		if a.size < int64(len(footerTemplate)) {
			return fmt.Errorf("%w: missing footer %q", ErrRejectedArchive, footerTemplate)
		}
		tail := make([]byte, len(footerTemplate))
		if err := readAtFull(a.reader, a.size-int64(len(footerTemplate)), tail); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("%w: footer: %v", ErrRejectedArchive, err)
			}
			return fmt.Errorf("%w: footer: %v", ErrMalformedArchive, err)
		}
		copy(tail[editionStart:editionEnd], "0000")
		if string(tail) != footerTemplate {
			return fmt.Errorf("%w: missing footer %q", ErrRejectedArchive, footerTemplate)
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
	// Only the low stored byte participates. Stored 0 and 255 both select
	// plain data: the latter derives a zero working key [02 §2].
	if keyByte := byte(headerKeyWord); keyByte != 0 {
		a.key = uint32(^(keyByte>>6 | keyByte<<2))
	}
	if a.key != 0 {
		decrypt(blob[hpiHeaderSize:], hpiHeaderSize, a.key)
	}

	view := hpiDirectoryView{archive: a, bytes: blob, start: 0, end: blobSize,
		entriesLeft: uint64(a.options.MaxDirectoryEntries), stringsLeft: uint64(a.options.MaxDirectoryStringBytes)}
	root := EntryInfo{Path: "", Name: "", IsDir: true, OriginalPath: "", Source: Provenance{ProviderType: "hpi", SourcePath: a.name, MountOrder: 0}}
	a.entries[""] = &providerEntry{info: root}
	stack := make(map[uint64]bool)
	if err := view.walkDirectory(rootOffset, "", "", stack, true); err != nil {
		return err
	}
	return nil
}

type hpiDirectoryView struct {
	archive     *Archive
	bytes       []byte
	start       uint64
	end         uint64
	entriesLeft uint64
	stringsLeft uint64
}

func (v hpiDirectoryView) offset(pos uint64, length uint64) (int, error) {
	// Check the upper endpoint before subtracting it. Otherwise a pointer past
	// the directory wraps the unsigned subtraction and can reach a slice below.
	if pos < v.start || pos > v.end || length > v.end-pos {
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

func (v *hpiDirectoryView) cstring(pos uint64) (string, error) {
	index, err := v.offset(pos, 1)
	if err != nil {
		return "", err
	}
	// Limit the scan itself, rather than charging only after a long shared
	// name has already consumed work. Each visit pays again [I11].
	scan := v.bytes[index:]
	if uint64(len(scan)) > v.stringsLeft {
		scan = scan[:int(v.stringsLeft)]
	}
	end := bytes.IndexByte(scan, 0)
	if end < 0 {
		if len(scan) < len(v.bytes)-index {
			return "", fmt.Errorf("%w: directory string work limit", ErrMalformedArchive)
		}
		return "", fmt.Errorf("%w: unterminated name at 0x%x", ErrMalformedArchive, pos)
	}
	v.stringsLeft -= uint64(end) + 1
	return string(v.bytes[index : index+end]), nil
}

func (v *hpiDirectoryView) walkDirectory(nodeOffset uint64, parent, originalParent string, stack map[uint64]bool, visible bool) error {
	// TODO(question): establish retail acceptance of acyclic shared directories
	// by tracing repeated-node relocation [02 R-MALF-01 §3]. Host limits bound
	// expansion independently of that unknown (DESIGN_CONTENT_VFS §5).
	if len(stack) >= v.archive.options.MaxDirectoryDepth {
		return fmt.Errorf("%w: directory depth limit", ErrMalformedArchive)
	}
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
	// Reserve the entire list before allocating its lookup map or scanning
	// names. Revisited and shadowed subtrees consume the same global budget.
	if uint64(count) > v.entriesLeft {
		return fmt.Errorf("%w: directory entry limit", ErrMalformedArchive)
	}
	v.entriesLeft -= uint64(count)
	// Lookup chooses each component from the directory's last matching entry;
	// earlier duplicate directories contribute no reachable descendants [02 §2].
	// Still validate every subtree and retain every authored entry for diagnostics.
	lastEntry := make(map[string]uint32)
	for i := uint32(0); i < count; i++ {
		nameOffset, err := v.u32(uint64(listOffset) + uint64(i)*hpiEntrySize)
		if err != nil {
			return err
		}
		name, err := v.cstring(uint64(nameOffset))
		if err != nil {
			return err
		}
		lastEntry[foldLogicalName(name)] = i
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
		// Charge both joined inputs before joinPath can allocate or normalize
		// them. Use widened lengths so hostile names cannot overflow an int.
		pathBytes := uint64(len(parent)) + uint64(len(originalParent)) + 2*uint64(len(name))
		if parent != "" {
			pathBytes++
		}
		if originalParent != "" {
			pathBytes++
		}
		if pathBytes > v.stringsLeft {
			return fmt.Errorf("%w: directory string work limit", ErrMalformedArchive)
		}
		v.stringsLeft -= pathBytes
		logical, err := joinPath(parent, name)
		if err != nil {
			return fmt.Errorf("%w: entry %q: %v", ErrMalformedArchive, name, err)
		}
		original := originalJoin(originalParent, name)
		lookupVisible := visible && lastEntry[foldLogicalName(name)] == i
		if v.bytes[flagIndex]&1 != 0 {
			info := EntryInfo{Path: logical, Name: name, IsDir: true, OriginalPath: original,
				Source: Provenance{LogicalPath: logical, OriginalPath: original, ProviderType: "hpi", SourcePath: v.archive.name}}
			if lookupVisible {
				v.archive.entries[logical] = &providerEntry{info: info}
			}
			v.archive.appendIndexedEntry(info, visible)
			if err := v.walkDirectory(uint64(dataOffset), logical, original, stack, lookupVisible); err != nil {
				return err
			}
			continue
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
		entry.readRange = func(offset int64, length int) ([]byte, error) {
			return v.archive.readRecordRange(record, offset, length)
		}
		if lookupVisible {
			v.archive.entries[logical] = entry
		}
		v.archive.appendIndexedEntry(info, visible)
	}
	return nil
}

func (a *Archive) appendIndexedEntry(info EntryInfo, visible bool) {
	if visible {
		a.retailEntryIndices = append(a.retailEntryIndices, len(a.indexedEntries))
	}
	a.indexedEntries = append(a.indexedEntries, info)
}

// retailEntries follows forward entry order inside the directory selected by
// the backward component lookup [02 R-CAT-01 §1]. auditEntries keeps shadowed
// subtrees available to diagnostics without making them enumerable content.
func (a *Archive) retailEntries() []EntryInfo {
	entries := make([]EntryInfo, len(a.retailEntryIndices))
	for i, index := range a.retailEntryIndices {
		entries[i] = a.indexedEntries[index]
	}
	return entries
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
	// data was decoded for this open alone, so the handle can hand it to a
	// whole-file reader without a second copy [vfs.WholeFile].
	return &fileHandle{reader: reader, readerAt: reader, closeFn: func() error { return nil }, info: info, whole: data}, nil
}

func (a *Archive) readRecord(record hpiRecord) ([]byte, error) {
	return a.readRecordRange(record, 0, -1)
}

// readRecordRange decodes only the part of a record that the requested byte
// range needs. length < 0 means "to the end".
//
// A compressed record is a table of chunk sizes followed by that many SQSH
// chunks, and every chunk but the last decodes to exactly hpiChunkSize bytes
// [02 §2]. The decompressed offset of a chunk is therefore its index times the
// chunk size, and the table gives every chunk's stored size, so the chunks
// before the range can be stepped over with arithmetic alone — no read and no
// decode. That is what makes a header probe on a multi-megabyte record cost
// one chunk instead of the whole file.
func (a *Archive) readRecordRange(record hpiRecord, offset int64, length int) ([]byte, error) {
	if record.size > uint64(a.options.MaxFileBytes) || record.size > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w: file exceeds size limit", ErrMalformedArchive)
	}
	if offset < 0 || uint64(offset) > record.size {
		return nil, fmt.Errorf("%w: range offset %d outside %d-byte file", ErrMalformedArchive, offset, record.size)
	}
	remaining := int64(record.size) - offset
	if length < 0 || int64(length) > remaining {
		length = int(remaining)
	}
	if record.compression == 0 {
		data := make([]byte, length)
		if err := a.readArchiveBytes(record.dataOffset+uint64(offset), data); err != nil {
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
	if length == 0 {
		return []byte{}, nil
	}
	first := uint64(offset) / hpiChunkSize
	last := (uint64(offset) + uint64(length) - 1) / hpiChunkSize
	result := make([]byte, length)
	position := record.dataOffset + uint64(len(table))
	for chunk := uint64(0); chunk < first; chunk++ {
		storedSize := uint64(binary.LittleEndian.Uint32(table[chunk*4 : chunk*4+4]))
		if storedSize < hpiChunkHeaderSize || storedSize > uint64(a.size)-position {
			return nil, fmt.Errorf("%w: invalid chunk %d size", ErrMalformedArchive, chunk)
		}
		position += storedSize
	}
	produced := 0
	for chunk := first; chunk <= last && chunk < chunkCount; chunk++ {
		storedSize := uint64(binary.LittleEndian.Uint32(table[chunk*4 : chunk*4+4]))
		if storedSize < hpiChunkHeaderSize || storedSize > uint64(a.size)-position {
			return nil, fmt.Errorf("%w: invalid chunk %d size", ErrMalformedArchive, chunk)
		}
		chunkStart := chunk * hpiChunkSize
		expectedSize := uint64(hpiChunkSize)
		if remaining := record.size - chunkStart; remaining < expectedSize {
			expectedSize = remaining
		}
		decoded, err := a.decodeChunk(chunk, position, storedSize, expectedSize)
		if err != nil {
			return nil, err
		}
		position += storedSize
		// Copy the part of this chunk that falls inside the requested range.
		from := int64(0)
		if int64(chunkStart) < offset {
			from = offset - int64(chunkStart)
		}
		if from > int64(len(decoded)) {
			return nil, fmt.Errorf("%w: chunk %d output size", ErrMalformedArchive, chunk)
		}
		produced += copy(result[produced:], decoded[from:])
	}
	if produced != length {
		return nil, fmt.Errorf("%w: decompressed size mismatch", ErrMalformedArchive)
	}
	return result, nil
}

// decodeChunk reads and decodes one SQSH chunk. expectedOutput is the record
// chunk span: 64 KiB for every non-final chunk and the record remainder for
// the final chunk [02 §2]. Reading the fixed header first validates the
// payload metadata before its file-backed bytes are streamed.
func (a *Archive) decodeChunk(index, position, storedSize, expectedOutput uint64) ([]byte, error) {
	if storedSize < hpiChunkHeaderSize {
		return nil, fmt.Errorf("%w: chunk %d size", ErrMalformedArchive, index)
	}
	header := make([]byte, hpiChunkHeaderSize)
	if err := a.readArchiveBytes(position, header); err != nil {
		return nil, err
	}
	if string(header[0:4]) != "SQSH" {
		return nil, fmt.Errorf("%w: chunk %d marker", ErrMalformedArchive, index)
	}
	method := header[5]
	// [02 §2]: the chunk header selects the actual decoder and retail does
	// not require the two method numbers to match, so no equality check
	// against the record's compression byte — dispatch on the chunk alone.
	payloadSize := uint64(binary.LittleEndian.Uint32(header[7:11]))
	decompressedSize := uint64(binary.LittleEndian.Uint32(header[11:15]))
	checksum := binary.LittleEndian.Uint32(header[15:19])
	if payloadSize != storedSize-hpiChunkHeaderSize || decompressedSize != expectedOutput {
		return nil, fmt.Errorf("%w: chunk %d size fields", ErrMalformedArchive, index)
	}
	payloadOffset := position + hpiChunkHeaderSize
	sum, err := a.checksumPayload(payloadOffset, payloadSize)
	if err != nil {
		return nil, fmt.Errorf("%w: chunk %d checksum payload: %v", ErrMalformedArchive, index, err)
	}
	if a.options.VerifyChecksums && sum != checksum {
		return nil, fmt.Errorf("%w: chunk %d checksum", ErrMalformedArchive, index)
	}
	payload := &archivePayloadReader{archive: a, offset: payloadOffset, remaining: payloadSize, encoded: header[6] != 0}
	var decoded []byte
	switch method {
	case 1:
		decoded, err = decodeLZ77(bufio.NewReader(payload), decompressedSize)
	case 2:
		decoded, err = decodeZlib(payload, decompressedSize)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: chunk %d: %v", ErrMalformedArchive, index, err)
	}
	if uint64(len(decoded)) != decompressedSize {
		return nil, fmt.Errorf("%w: chunk %d output size", ErrMalformedArchive, index)
	}
	return decoded, nil
}

// archivePayloadReader reads a chunk payload in bounded pieces. The checksum
// is deliberately taken before the encoded-payload transform, matching the
// SQSH wire ordering [02 §2].
type archivePayloadReader struct {
	archive   *Archive
	offset    uint64
	remaining uint64
	index     uint64
	encoded   bool
}

func (r *archivePayloadReader) Read(data []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if uint64(len(data)) > r.remaining {
		data = data[:int(r.remaining)]
	}
	if err := r.archive.readArchiveBytes(r.offset, data); err != nil {
		return 0, err
	}
	if r.encoded {
		for i := range data {
			position := r.index + uint64(i)
			data[i] = byte(uint16(data[i])-uint16(position)) ^ byte(position)
		}
	}
	r.offset += uint64(len(data))
	r.remaining -= uint64(len(data))
	r.index += uint64(len(data))
	if r.remaining == 0 {
		return len(data), io.EOF
	}
	return len(data), nil
}

func (a *Archive) checksumPayload(offset, size uint64) (uint32, error) {
	reader := archivePayloadReader{archive: a, offset: offset, remaining: size}
	var buffer [32 << 10]byte
	var sum uint32
	for {
		count, err := reader.Read(buffer[:])
		for _, value := range buffer[:count] {
			sum += uint32(value)
		}
		if err == io.EOF {
			return sum, nil
		}
		if err != nil {
			return 0, err
		}
	}
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

// resettableReader is what compress/zlib's reader satisfies: it can be
// pointed at a new stream without reallocating its history window.
type resettableReader interface {
	io.ReadCloser
	zlib.Resetter
}

// zlibReaders keeps decoded-out readers for reuse. Each fresh zlib reader
// allocates a 32 KiB flate history window, and a compressed record is decoded
// one chunk at a time, so a reader per chunk was the single largest
// allocation in a whole-install catalog compile.
var zlibReaders sync.Pool

func decodeZlib(source io.Reader, expected uint64) ([]byte, error) {
	if expected > uint64(math.MaxInt) {
		return nil, errors.New("zlib output is too large")
	}
	reader, _ := zlibReaders.Get().(resettableReader)
	if reader == nil {
		fresh, err := zlib.NewReader(source)
		if err != nil {
			return nil, err
		}
		var ok bool
		if reader, ok = fresh.(resettableReader); !ok {
			return nil, errors.New("zlib reader is not resettable")
		}
	} else if err := reader.Reset(source, nil); err != nil {
		return nil, err
	}
	defer zlibReaders.Put(reader)
	// The chunk header states the decompressed size, so the output buffer is
	// exact [02 §2] — io.ReadAll would allocate roughly twice this while
	// doubling its way there.
	data := make([]byte, int(expected))
	if _, err := io.ReadFull(reader, data); err != nil {
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			return nil, fmt.Errorf("zlib produced fewer than %d bytes", expected)
		}
		return nil, err
	}
	// The stream must be exhausted: more bytes than the header declared is a
	// malformed chunk, not a truncation this reader may silently accept.
	var extra [1]byte
	switch n, err := reader.Read(extra[:]); {
	case n != 0:
		return nil, fmt.Errorf("zlib produced more than %d bytes", expected)
	case err != nil && err != io.EOF:
		return nil, err
	}
	return data, nil
}

func decodeLZ77(source io.ByteReader, expected uint64) ([]byte, error) {
	if expected > uint64(math.MaxInt) {
		return nil, errors.New("LZ77 output is too large")
	}
	output := make([]byte, 0, int(expected))
	var window [4096]byte
	write := 1
	// Retail terminates on a zero match position, then checks the output
	// size. Reaching the declared size alone is not a terminator [02 §2].
	for {
		tag, err := source.ReadByte()
		if err != nil {
			return nil, errors.New("LZ77 tag is truncated")
		}
		for bit := 0; bit < 8; bit++ {
			if tag&(1<<bit) == 0 {
				value, err := source.ReadByte()
				if err != nil {
					return nil, errors.New("LZ77 literal is truncated")
				}
				if err := lzAppend(&output, &window, &write, value, expected); err != nil {
					return nil, err
				}
				continue
			}
			low, err := source.ReadByte()
			if err != nil {
				return nil, errors.New("LZ77 match is truncated")
			}
			high, err := source.ReadByte()
			if err != nil {
				return nil, errors.New("LZ77 match is truncated")
			}
			word := uint16(low) | uint16(high)<<8
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
