package content

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Shared fixture helpers for the content compiler tests. Asset-guarded tests
// live in catalog_retail_test.go; everything here is fixture-only and always
// runs.

// piOver180 is the image's degrees-to-radians constant, the multiplier of
// [02 "Weapon record"]'s minbarrelangle conversion; used by tests to assert
// the exact composition. It is deliberately not math.Pi/180.
func piOver180() float64 { return degreesToRadians }

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
	// Unit fixtures carry no compatibility metadata: the catalog admits every
	// unit definition whatever its Version and Copyright say
	// (DESIGN_CONTENT_VFS §5 "Unit admission (Nanolathe policy)").
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
			Path: path,
			Name: base,
			// Content fixtures model archived authored data. Production unit
			// and weapon discovery applies retail's archive-only gate, so tests
			// must state their provider provenance explicitly [02 §2][SC24].
			Source: vfs.Provenance{LogicalPath: path, ProviderType: "hpi", SourcePath: "fixture.hpi"},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// looseUnitFS marks named logical paths as loose-directory winners while every
// other fixture path keeps its archive provenance. It exercises the one retail
// unit drop gate Nanolathe keeps — unit content must come from a mounted
// archive, so a loose FBI winner is parsed and then dropped [02 R-CAT-01 §4] —
// without building a real overlay.
type looseUnitFS struct {
	*fixtureFS
	loose map[string]bool
}

func newLooseUnitFS(fs *fixtureFS, loose ...string) *looseUnitFS {
	set := make(map[string]bool, len(loose))
	for _, path := range loose {
		set[strings.ToLower(path)] = true
	}
	return &looseUnitFS{fixtureFS: fs, loose: set}
}

func (f *looseUnitFS) ReadDir(name string) ([]vfs.EntryInfo, error) {
	entries, err := f.fixtureFS.ReadDir(name)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if f.loose[strings.ToLower(entries[i].Path)] {
			entries[i].Source = vfs.Provenance{LogicalPath: entries[i].Path, ProviderType: "directory", SourcePath: entries[i].Path}
		}
	}
	return entries, nil
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
