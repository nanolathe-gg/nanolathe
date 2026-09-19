package vfs

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// recordingFS answers from a fixed path set and records the paths it was
// asked for, so a test can assert what the wrapper handed down as well as
// what it handed back.
type recordingFS struct {
	files map[string]bool
	asked []string
}

func (r *recordingFS) note(name string) string {
	r.asked = append(r.asked, name)
	return strings.ToLower(name)
}

func (r *recordingFS) info(path string) EntryInfo {
	return EntryInfo{Name: path[strings.LastIndexByte(path, '/')+1:], Path: path, Size: 1}
}

func (r *recordingFS) Open(name string) (File, error) {
	if !r.files[r.note(name)] {
		return nil, os.ErrNotExist
	}
	return nil, nil
}

func (r *recordingFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if !r.files[r.note(name)] {
		return nil, os.ErrNotExist
	}
	return []byte("payload"), nil
}

func (r *recordingFS) ReadDir(name string) ([]EntryInfo, error) {
	prefix := r.note(name)
	if prefix != "" {
		prefix += "/"
	}
	var out []EntryInfo
	for path := range r.files {
		if strings.HasPrefix(path, prefix) && !strings.Contains(path[len(prefix):], "/") {
			out = append(out, r.info(path))
		}
	}
	if len(out) == 0 {
		return nil, os.ErrNotExist
	}
	return out, nil
}

func (r *recordingFS) Stat(name string) (EntryInfo, error) {
	path := r.note(name)
	if !r.files[path] {
		return EntryInfo{}, os.ErrNotExist
	}
	return r.info(path), nil
}

func (r *recordingFS) CacheStamp(name string) (string, error) {
	path := r.note(name)
	if !r.files[path] {
		return "", os.ErrNotExist
	}
	return "stamp:" + path, nil
}

// richFS adds the overlay's two optional views to recordingFS, so a test can
// compare a wrapper over a view that has them with one over a view that does
// not.
type richFS struct{ *recordingFS }

func (r richFS) Sources(name string) []EntryInfo {
	info, err := r.Stat(name)
	if err != nil {
		return nil
	}
	return []EntryInfo{info}
}

func (r richFS) ManifestHash() (string, error) { return "manifest", nil }

// rangedFS adds the optional byte-range capability, so a test can tell a
// wrapper over a view that has it from one over a view that does not.
type rangedFS struct{ richFS }

func (r rangedFS) ReadFileRange(name string, offset int64, length int) ([]byte, error) {
	if !r.files[r.note(name)] {
		return nil, os.ErrNotExist
	}
	payload := []byte("payload")
	if offset > int64(len(payload)) {
		return nil, os.ErrNotExist
	}
	payload = payload[offset:]
	if length >= 0 && length < len(payload) {
		payload = payload[:length]
	}
	return payload, nil
}

func escalationLayout() Layout {
	return NewLayout(map[string]string{"units": "unitsE", "weapons": "weaponE", "gamedata": "gamedatE"})
}

func newRecordingFS(paths ...string) *recordingFS {
	files := make(map[string]bool, len(paths))
	for _, path := range paths {
		files[path] = true
	}
	return &recordingFS{files: files}
}

// TestLayoutRedirectsTheFirstSegmentBothWays is the wrapper's whole contract:
// the request goes down renamed, the result comes back retail-named.
func TestLayoutRedirectsTheFirstSegmentBothWays(t *testing.T) {
	inner := newRecordingFS("unitse/armcom.fbi", "weapone/guns.tdf", "objects3d/armcom.3do")
	view := escalationLayout().Apply(richFS{inner})

	info, err := view.Stat("units/ARMCOM.FBI")
	if err != nil {
		t.Fatalf("Stat through the layout: %v", err)
	}
	if info.Path != "units/armcom.fbi" {
		t.Fatalf("Stat path = %q, want the retail-named units/armcom.fbi", info.Path)
	}
	if inner.asked[len(inner.asked)-1] != "unitsE/ARMCOM.FBI" {
		t.Fatalf("inner view was asked for %q, want unitsE/ARMCOM.FBI", inner.asked[len(inner.asked)-1])
	}

	if _, err := view.ReadFileLimit("weapons/guns.tdf", 1<<10); err != nil {
		t.Fatalf("ReadFileLimit through the layout: %v", err)
	}
	if stamp, err := view.CacheStamp("units/armcom.fbi"); err != nil || stamp != "stamp:unitse/armcom.fbi" {
		t.Fatalf("CacheStamp = %q (%v), want the inner view's stamp", stamp, err)
	}

	entries, err := view.ReadDir("units")
	if err != nil {
		t.Fatalf("ReadDir through the layout: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "units/armcom.fbi" {
		t.Fatalf("ReadDir entries = %+v, want one retail-named units/armcom.fbi", entries)
	}

	// A path whose first segment is not in the table is untouched, and a
	// segment that only looks like one is not rewritten in the middle.
	if _, err := view.Stat("objects3d/armcom.3do"); err != nil {
		t.Fatalf("unmapped directory was redirected: %v", err)
	}
	if _, err := view.Stat("objects3d/units/armcom.3do"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the layout rewrote a segment that was not the first")
	}

	// Sources carries the same reversal, because a missing-file diagnostic
	// names the providers it searched for a retail-named path.
	sources := view.(interface{ Sources(string) []EntryInfo }).Sources("units/armcom.fbi")
	if len(sources) != 1 || sources[0].Path != "units/armcom.fbi" {
		t.Fatalf("Sources = %+v, want one retail-named entry", sources)
	}
	hash, err := view.(interface{ ManifestHash() (string, error) }).ManifestHash()
	if err != nil || hash != "manifest" {
		t.Fatalf("ManifestHash = %q (%v), want the inner view's identity", hash, err)
	}
}

// TestLayoutWithoutOptionalViewsAnswersAsThoughUnwrapped keeps the wrapper
// from pretending a plain FSOps has the overlay's two optional reports.
func TestLayoutWithoutOptionalViewsAnswersAsThoughUnwrapped(t *testing.T) {
	view := escalationLayout().Apply(newRecordingFS("unitse/armcom.fbi"))
	if sources := view.(interface{ Sources(string) []EntryInfo }).Sources("units/armcom.fbi"); sources != nil {
		t.Fatalf("Sources = %+v, want nil over a view that has no such report", sources)
	}
	hash, err := view.(interface{ ManifestHash() (string, error) }).ManifestHash()
	if err != nil || hash != "" {
		t.Fatalf("ManifestHash = %q (%v), want the empty identity", hash, err)
	}
}

// TestEmptyLayoutReturnsTheViewUnchanged is why a retail install keeps its
// concrete overlay: the callers that type-assert for the overlay's optional
// reports must still find them.
func TestEmptyLayoutReturnsTheViewUnchanged(t *testing.T) {
	inner := newRecordingFS("units/armcom.fbi")
	for _, layout := range []Layout{{}, NewLayout(nil), NewLayout(map[string]string{}),
		NewLayout(map[string]string{"units": "UNITS"}), NewLayout(map[string]string{"": "x", "y": ""})} {
		if len(layout.Names()) != 0 {
			t.Fatalf("layout %+v should have no rows", layout.Names())
		}
		if applied := layout.Apply(inner); applied != FSOps(inner) {
			t.Fatal("an empty layout wrapped the view")
		}
	}
	if layout := (Layout{}); layout.Apply(nil) != nil {
		t.Fatal("applying a layout to no view produced one")
	}
}

// TestLayoutRowsAreOrderedAndReversible locks the diagnostic order and the
// round trip every provenance path depends on.
func TestLayoutRowsAreOrderedAndReversible(t *testing.T) {
	layout := escalationLayout()
	rows := layout.Names()
	want := [][2]string{{"gamedata", "gamedatE"}, {"units", "unitsE"}, {"weapons", "weaponE"}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d = %v, want %v", i, rows[i], want[i])
		}
	}
	for _, path := range []string{"units/armcom.fbi", "weapons", "gamedata/sidedata.tdf", "maps/x.ota", ""} {
		if got := layout.restore(layout.rewrite(path)); got != path {
			t.Fatalf("restore(rewrite(%q)) = %q", path, got)
		}
	}
	// An authored backslash path splits on the same first separator.
	if got := layout.rewrite(`units\armcom.fbi`); got != `unitsE\armcom.fbi` {
		t.Fatalf("backslash rewrite = %q", got)
	}
}

// TestLayoutLookupAllocatesOnlyTheRewrittenPath holds the wrapper to one short
// concatenation per redirected lookup: the catalog compile performs thousands
// of them.
func TestLayoutLookupAllocatesOnlyTheRewrittenPath(t *testing.T) {
	layout := escalationLayout()
	if got := allocsOf(func() { _ = layout.rewrite("objects3d/armcom.3do") }); got != 0 {
		t.Fatalf("unmapped rewrite allocated %.0f times, want 0", got)
	}
	if got := allocsOf(func() { _ = layout.rewrite("units/armcom.fbi") }); got > 1 {
		t.Fatalf("mapped rewrite allocated %.0f times, want at most the one path", got)
	}
	if got := allocsOf(func() { _ = layout.restore("unitse/armcom.fbi") }); got > 1 {
		t.Fatalf("mapped restore allocated %.0f times, want at most the one path", got)
	}
}

func allocsOf(f func()) float64 { return testing.AllocsPerRun(200, f) }

// TestLayoutForwardsRangeReadsUnderTheRewrittenPath covers the capability the
// map census probes for: a profiled content set must keep the header-range
// fast path, or every TNT is decompressed whole to read its first sixty-four
// bytes. A wrapper over a view without the capability must not advertise one,
// because the probe is what selects the fallback.
func TestLayoutForwardsRangeReadsUnderTheRewrittenPath(t *testing.T) {
	inner := newRecordingFS("unitse/armcom.fbi")
	view := escalationLayout().Apply(rangedFS{richFS{inner}})

	ranged, ok := view.(RangeReader)
	if !ok {
		t.Fatalf("view over a range reader does not offer ReadFileRange")
	}
	data, err := ranged.ReadFileRange("units/ARMCOM.FBI", 2, 3)
	if err != nil {
		t.Fatalf("ReadFileRange through the layout: %v", err)
	}
	if string(data) != "ylo" {
		t.Fatalf("ReadFileRange = %q, want the inner view's range", data)
	}
	if len(inner.asked) != 1 || inner.asked[0] != "unitsE/ARMCOM.FBI" {
		t.Fatalf("inner view was asked for %v, want the rewritten path", inner.asked)
	}

	// The wrapper must not turn a view that cannot answer ranges into one
	// that claims it can; the caller's probe then chooses the whole-file
	// fallback exactly as it would without a layout.
	plain := escalationLayout().Apply(richFS{newRecordingFS("unitse/armcom.fbi")})
	if _, ok := plain.(RangeReader); ok {
		t.Fatalf("view over a range-less FS advertises ReadFileRange")
	}
	// The other optional reports survive the extra wrapper.
	if sources := plain.(interface{ Sources(string) []EntryInfo }).Sources("units/armcom.fbi"); len(sources) != 1 {
		t.Fatalf("Sources = %+v, want the inner view's single row", sources)
	}
	if sources := view.(interface{ Sources(string) []EntryInfo }).Sources("units/armcom.fbi"); len(sources) != 1 || sources[0].Path != "units/armcom.fbi" {
		t.Fatalf("Sources through the range view = %+v, want one retail-named row", sources)
	}
	if hash, err := view.(interface{ ManifestHash() (string, error) }).ManifestHash(); err != nil || hash != "manifest" {
		t.Fatalf("ManifestHash through the range view = %q (%v), want the inner identity", hash, err)
	}
}
