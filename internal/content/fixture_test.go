package content

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// Shared fixture helpers for the content compiler tests. Asset-guarded tests
// live in catalog_retail_test.go; everything here is fixture-only and always
// runs.

// piOver180 is the composed constant of [02 "Weapon record"]'s minbarrelangle
// conversion; used by tests to assert the exact composition.
func piOver180() float64 { return math.Pi / 180.0 }

// mustParseTDF parses TDF bytes or fails the test.
func mustParseTDF(t *testing.T, body string) *formats.Document {
	t.Helper()
	doc, err := formats.ParseTDF([]byte(body))
	if err != nil {
		t.Fatalf("ParseTDF: %v", err)
	}
	return doc
}

// fixtureFile is one logical path and its bytes in a fixtureFS.
type fixtureFile struct {
	path string
	data string
}

// fixtureFS is a minimal vfs.FSOps over in-memory files. ReadDir returns
// paths sorted by name so compilers see stable order (I1).
type fixtureFS struct {
	files map[string]string
}

func newFixtureFS(t *testing.T, files ...fixtureFile) *fixtureFS {
	t.Helper()
	fs := &fixtureFS{files: make(map[string]string, len(files))}
	for _, f := range files {
		fs.files[f.path] = f.data
	}
	return fs
}

func (f *fixtureFS) Open(name string) (vfs.File, error) {
	data, ok := f.files[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("fixture: %s: not found", name)
	}
	return newBytesFile([]byte(data), vfs.EntryInfo{Path: strings.ToLower(name), Size: int64(len(data))}), nil
}

func (f *fixtureFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	data, ok := f.files[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("fixture: %s: not found", name)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("fixture: %s: too large", name)
	}
	return []byte(data), nil
}

func (f *fixtureFS) ReadDir(name string) ([]vfs.EntryInfo, error) {
	name = strings.ToLower(strings.TrimSuffix(name, "/"))
	var out []vfs.EntryInfo
	seen := map[string]bool{}
	for path := range f.files {
		dir := path[:strings.LastIndex(path, "/")]
		base := path[strings.LastIndex(path, "/")+1:]
		if dir != name || seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, vfs.EntryInfo{
			Path:   path,
			Name:   base,
			Source: vfs.Provenance{LogicalPath: path},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *fixtureFS) Stat(name string) (vfs.EntryInfo, error) {
	data, ok := f.files[strings.ToLower(name)]
	if !ok {
		return vfs.EntryInfo{}, fmt.Errorf("fixture: %s: not found", name)
	}
	return vfs.EntryInfo{Path: strings.ToLower(name), Size: int64(len(data))}, nil
}

func (f *fixtureFS) CacheStamp(name string) (string, error) {
	data, ok := f.files[strings.ToLower(name)]
	if !ok {
		return "", fmt.Errorf("fixture: %s: not found", name)
	}
	return fmt.Sprintf("fixture|%s|%d", name, len(data)), nil
}

// keysOf returns the sorted keys of a weapon map for diagnostics.
func keysOf(m map[string]*WeaponDef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// bytesFile adapts an in-memory byte slice to vfs.File.
type bytesFile struct {
	*bytes.Reader
	info vfs.EntryInfo
}

func newBytesFile(data []byte, info vfs.EntryInfo) *bytesFile {
	return &bytesFile{Reader: bytes.NewReader(data), info: info}
}

func (b *bytesFile) Close() error        { return nil }
func (b *bytesFile) Info() vfs.EntryInfo { return b.info }

var _ vfs.File = (*bytesFile)(nil)
var _ io.ReaderAt = (*bytesFile)(nil)
