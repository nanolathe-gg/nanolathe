package vfs

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ArchiveFile is one file to store in an archive. Path is a logical path with
// forward slashes; the directory tree is derived from it.
type ArchiveFile struct {
	Path string
	Data []byte
}

// ArchiveWriteOptions selects the stored encoding.
type ArchiveWriteOptions struct {
	// Compress writes every file as zlib SQSH chunks (compression method 2);
	// otherwise files are stored verbatim (method 0).
	Compress bool
	// Year fills the four edition bytes of the trailing copyright string the
	// retail mount validator requires [fmt hpi]. Zero writes 1997.
	Year int
}

// WriteArchive serialises files into the HAPI layout [fmt hpi]: a 20-byte
// plaintext header, the directory tree immediately after it (nodes, packed
// 9-byte entries, file-data records and name strings), file data in directory
// order, and the 36-byte copyright trailer. The header key is zero, so nothing
// is obfuscated; retail and third-party readers accept that.
func WriteArchive(w io.Writer, files []ArchiveFile, opts ArchiveWriteOptions) error {
	root := &writeDir{children: map[string]*writeDir{}, files: map[string]*writeFile{}}
	for _, file := range files {
		clean := strings.Trim(strings.ReplaceAll(file.Path, `\`, "/"), "/")
		if clean == "" {
			return fmt.Errorf("hpi write: empty path")
		}
		parts := strings.Split(clean, "/")
		dir := root
		for _, part := range parts[:len(parts)-1] {
			if part == "" || part == "." || part == ".." {
				return fmt.Errorf("hpi write: invalid path %q", file.Path)
			}
			key := strings.ToLower(part)
			next, ok := dir.children[key]
			if !ok {
				next = &writeDir{name: part, children: map[string]*writeDir{}, files: map[string]*writeFile{}}
				dir.children[key] = next
			}
			dir = next
		}
		leaf := parts[len(parts)-1]
		key := strings.ToLower(leaf)
		if _, dup := dir.files[key]; dup || dir.children[key] != nil {
			return fmt.Errorf("hpi write: duplicate entry %q", file.Path)
		}
		dir.files[key] = &writeFile{name: leaf, data: file.Data}
	}

	const headerSize = 20
	blob := make([]byte, headerSize)
	var ordered []*writeFile
	var patches []int // offsets of file-data-record data pointers
	var layout func(dir *writeDir) (nodeOffset int)
	layout = func(dir *writeDir) int {
		names := make([]string, 0, len(dir.children)+len(dir.files))
		for key := range dir.children {
			names = append(names, key)
		}
		for key := range dir.files {
			names = append(names, key)
		}
		sort.Strings(names)
		node := len(blob)
		blob = binary.LittleEndian.AppendUint32(blob, uint32(len(names)))
		blob = binary.LittleEndian.AppendUint32(blob, 0)
		entries := len(blob)
		blob = append(blob, make([]byte, 9*len(names))...)
		binary.LittleEndian.PutUint32(blob[node+4:], uint32(entries))
		for i, key := range names {
			entry := entries + 9*i
			if child, ok := dir.children[key]; ok {
				nameAt := len(blob)
				blob = append(blob, child.name...)
				blob = append(blob, 0)
				childNode := layout(child)
				binary.LittleEndian.PutUint32(blob[entry:], uint32(nameAt))
				binary.LittleEndian.PutUint32(blob[entry+4:], uint32(childNode))
				blob[entry+8] = 1
				continue
			}
			file := dir.files[key]
			nameAt := len(blob)
			blob = append(blob, file.name...)
			blob = append(blob, 0)
			record := len(blob)
			blob = append(blob, make([]byte, 9)...)
			binary.LittleEndian.PutUint32(blob[record+4:], uint32(len(file.data)))
			if opts.Compress && len(file.data) > 0 {
				blob[record+8] = 2
			}
			binary.LittleEndian.PutUint32(blob[entry:], uint32(nameAt))
			binary.LittleEndian.PutUint32(blob[entry+4:], uint32(record))
			patches = append(patches, record)
			ordered = append(ordered, file)
		}
		return node
	}
	rootNode := layout(root)
	directoryEnd := len(blob)

	copy(blob[0:4], "HAPI")
	binary.LittleEndian.PutUint32(blob[4:8], 0x00010000)
	binary.LittleEndian.PutUint32(blob[8:12], uint32(directoryEnd))
	binary.LittleEndian.PutUint32(blob[12:16], 0)
	binary.LittleEndian.PutUint32(blob[16:20], uint32(rootNode))

	out := blob
	for i, file := range ordered {
		binary.LittleEndian.PutUint32(out[patches[i]:], uint32(len(out)))
		if !opts.Compress || len(file.data) == 0 {
			out = append(out, file.data...)
			continue
		}
		chunks, err := zlibChunks(file.data)
		if err != nil {
			return fmt.Errorf("hpi write: %s: %w", file.name, err)
		}
		for _, chunk := range chunks {
			out = binary.LittleEndian.AppendUint32(out, uint32(len(chunk)))
		}
		for _, chunk := range chunks {
			out = append(out, chunk...)
		}
	}
	year := opts.Year
	if year == 0 {
		year = 1997
	}
	out = append(out, fmt.Sprintf("Copyright %04d Cavedog Entertainment", year%10000)...)
	_, err := w.Write(out)
	return err
}

type writeDir struct {
	name     string
	children map[string]*writeDir
	files    map[string]*writeFile
}

type writeFile struct {
	name string
	data []byte
}

// zlibChunks splits data into 65536-byte logical chunks and wraps each in a
// SQSH header [fmt hpi]: marker, 2, method 2 (zlib), encoded 0, compressed
// size, decompressed size, and the wrapping byte sum of the payload.
func zlibChunks(data []byte) ([][]byte, error) {
	const chunkSize = 65536
	var chunks [][]byte
	for start := 0; start < len(data); start += chunkSize {
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		var payload bytes.Buffer
		z, err := zlib.NewWriterLevel(&payload, zlib.BestCompression)
		if err != nil {
			return nil, err
		}
		if _, err := z.Write(data[start:end]); err != nil {
			return nil, err
		}
		if err := z.Close(); err != nil {
			return nil, err
		}
		var sum uint32
		for _, b := range payload.Bytes() {
			sum += uint32(b)
		}
		chunk := make([]byte, 0, 19+payload.Len())
		chunk = append(chunk, "SQSH"...)
		chunk = append(chunk, 2, 2, 0)
		chunk = binary.LittleEndian.AppendUint32(chunk, uint32(payload.Len()))
		chunk = binary.LittleEndian.AppendUint32(chunk, uint32(end-start))
		chunk = binary.LittleEndian.AppendUint32(chunk, sum)
		chunk = append(chunk, payload.Bytes()...)
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}
