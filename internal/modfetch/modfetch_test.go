package modfetch

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

// fixtureArchive is authored filler; Download never looks inside it.
var fixtureArchive = bytes.Repeat([]byte("nanolathe authored download fixture\n"), 512)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// catalogServer is an httptest catalogue whose handlers a test swaps.
type catalogServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	routes   map[string]http.HandlerFunc
}

func newCatalogServer(t *testing.T) *catalogServer {
	t.Helper()
	s := &catalogServer{routes: map[string]http.HandlerFunc{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Clone(context.Background()))
		handler := s.routes[r.URL.Path]
		s.mu.Unlock()
		if handler == nil {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *catalogServer) route(path string, handler http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = handler
}

func (s *catalogServer) seen() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.requests...)
}

func (s *catalogServer) manifestURL() string { return s.URL + "/mods/manifest.json" }

// client uses the development override pointed at this server, which is how
// the http loopback origin is admitted.
func (s *catalogServer) client(t *testing.T) *Client {
	t.Helper()
	t.Setenv(catalogEnv, s.manifestURL())
	return &Client{CatalogURL: CatalogURL(), CacheDir: t.TempDir()}
}

func (s *catalogServer) entry(archivePath string, data []byte) Entry {
	return Entry{
		Metadata: modlibrary.Metadata{Schema: 1, ID: "sample", Name: "Sample", Version: "1.0"},
		Archive:  Archive{URL: s.URL + archivePath, Size: int64(len(data)), SHA256: digest(data)},
	}
}

func serveJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func serveBytes(data []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "archive.zip", time.Time{}, bytes.NewReader(data))
	}
}

func manifestJSON(entries ...string) string {
	return `{"schema":1,"mods":[` + strings.Join(entries, ",") + `]}`
}

func entryJSON(id, version, url string, size int, sha string) string {
	return fmt.Sprintf(`{"id":%q,"name":"Mod %s","version":%q,"schema":1,"archive":{"url":%q,"size":%d,"sha256":%q}}`, id, id, version, url, size, sha)
}

func TestCatalogURLOverride(t *testing.T) {
	t.Setenv(catalogEnv, "")
	if got := CatalogURL(); got != DefaultCatalogURL {
		t.Fatalf("CatalogURL() = %q, want the default", got)
	}
	t.Setenv(catalogEnv, "http://localhost:8080/manifest.json")
	if got := CatalogURL(); got != "http://localhost:8080/manifest.json" {
		t.Fatalf("CatalogURL() = %q, want the override", got)
	}
}

func TestCatalogOriginPolicy(t *testing.T) {
	t.Setenv(catalogEnv, "")
	for raw, admitted := range map[string]bool{
		"https://nanolathe.gg/mods/manifest.json":      true,
		"https://NANOLATHE.GG:443/mods/manifest.json":  true,
		"https://www.nanolathe.gg/mods/manifest.json":  false,
		"http://nanolathe.gg/mods/manifest.json":       false,
		"https://user@nanolathe.gg/mods/manifest.json": false,
		"http://127.0.0.1:8080/manifest.json":          false, // loopback only through the override
		"/mods/manifest.json":                          false,
	} {
		_, _, err := catalogOrigin(raw)
		if (err == nil) != admitted {
			t.Errorf("catalogOrigin(%q) = %v, admitted want %v", raw, err, admitted)
		}
		if err != nil && !errors.Is(err, ErrOriginRefused) {
			t.Errorf("catalogOrigin(%q) = %v, want ErrOriginRefused", raw, err)
		}
	}
	for override, admitted := range map[string]bool{
		"http://127.0.0.1:8080/manifest.json":   true,
		"http://localhost:8080/manifest.json":   true,
		"https://staging.example/manifest.json": true,
		"http://example.com/manifest.json":      false,
		"http://192.168.1.2/manifest.json":      false,
	} {
		t.Setenv(catalogEnv, override)
		if _, _, err := catalogOrigin(override); (err == nil) != admitted {
			t.Errorf("override %q: %v, admitted want %v", override, err, admitted)
		}
	}
	// A loopback URL that is not the override stays refused.
	t.Setenv(catalogEnv, "http://127.0.0.1:1/manifest.json")
	if _, _, err := catalogOrigin("http://127.0.0.1:2/manifest.json"); !errors.Is(err, ErrOriginRefused) {
		t.Fatalf("a loopback URL other than the override was admitted: %v", err)
	}
}

func TestHTTPRefusedWithoutTheOverride(t *testing.T) {
	server := newCatalogServer(t)
	server.route("/mods/manifest.json", serveJSON(manifestJSON()))
	t.Setenv(catalogEnv, "")
	client := &Client{CatalogURL: server.manifestURL(), CacheDir: t.TempDir()}
	if _, err := client.FetchManifest(context.Background()); !errors.Is(err, ErrOriginRefused) {
		t.Fatalf("FetchManifest over plain http = %v, want ErrOriginRefused", err)
	}
	if len(server.seen()) != 0 {
		t.Fatal("a refused catalogue URL was requested anyway")
	}
}

func TestFetchManifestParsesAndCaches(t *testing.T) {
	server := newCatalogServer(t)
	body := manifestJSON(
		entryJSON("prota", "4.8", "prota/prota-4.8.zip", 100, strings.Repeat("AB", 32)),
		entryJSON("zero", "a5", server.URL+"/mods/zero/zero-a5.zip", 200, strings.Repeat("0", 64)),
	)
	server.route("/mods/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "tracker", Value: "1"})
		serveJSON(body)(w, r)
	})
	client := server.client(t)
	for range 2 {
		result, err := client.FetchManifest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.FromCache || result.Err != nil || result.FetchedAt.IsZero() {
			t.Fatalf("live fetch result %+v", result)
		}
		mods := result.Manifest.Mods
		if len(mods) != 2 || mods[0].ID != "prota" || mods[1].ID != "zero" {
			t.Fatalf("parsed %+v", result.Manifest)
		}
		if want := server.URL + "/mods/prota/prota-4.8.zip"; mods[0].Archive.URL != want {
			t.Fatalf("relative archive URL resolved to %q, want %q", mods[0].Archive.URL, want)
		}
		if mods[0].Archive.SHA256 != strings.Repeat("ab", 32) {
			t.Fatalf("digest not normalized: %q", mods[0].Archive.SHA256)
		}
	}
	for _, req := range server.seen() {
		if got := req.Header.Get("User-Agent"); got != "nanolathe/nanolathe-1.0" {
			t.Errorf("User-Agent %q", got)
		}
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("request carried a cookie: %q", got)
		}
	}
	cached, err := os.ReadFile(filepath.Join(client.CacheDir, manifestCacheName))
	if err != nil || !strings.Contains(string(cached), server.URL+"/mods/prota/prota-4.8.zip") {
		t.Fatalf("cache holds %q, %v; want the catalogue with absolute archive URLs", cached, err)
	}
}

func TestFetchManifestFallsBackToCache(t *testing.T) {
	server := newCatalogServer(t)
	body := manifestJSON(entryJSON("sample", "1.0", "sample.zip", 10, strings.Repeat("1", 64)))
	server.route("/mods/manifest.json", serveJSON(body))
	client := server.client(t)
	live, err := client.FetchManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(client.CacheDir, manifestCacheName), stamp, stamp); err != nil {
		t.Fatal(err)
	}

	server.route("/mods/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
	})
	result, err := client.FetchManifest(context.Background())
	if err != nil {
		t.Fatalf("FetchManifest with a cache = %v, want the cached catalogue", err)
	}
	if !result.FromCache || result.Err == nil || !strings.Contains(result.Err.Error(), "503") {
		t.Fatalf("result %+v, want FromCache with the live error", result)
	}
	if !result.FetchedAt.Equal(stamp) {
		t.Fatalf("FetchedAt %v, want the cache time %v", result.FetchedAt, stamp)
	}
	if !reflect.DeepEqual(result.Manifest, live.Manifest) {
		t.Fatalf("cached %+v, want %+v", result.Manifest, live.Manifest)
	}

	// No cache: the live error is returned.
	bare := &Client{CatalogURL: client.CatalogURL, CacheDir: t.TempDir()}
	if _, err := bare.FetchManifest(context.Background()); err == nil || !strings.HasPrefix(err.Error(), "nanolathe: ") {
		t.Fatalf("FetchManifest without cache = %v, want the diagnostic", err)
	}

	// A cache written for another origin is not shown.
	other := newCatalogServer(t)
	other.route("/mods/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	})
	moved := other.client(t)
	moved.CacheDir = client.CacheDir
	if result, err := moved.FetchManifest(context.Background()); err == nil {
		t.Fatalf("a cache from another origin was shown: %+v", result)
	}
}

func TestManifestValidation(t *testing.T) {
	good := strings.Repeat("a", 64)
	for name, body := range map[string]string{
		"schema 2":         `{"schema":2,"mods":[]}`,
		"not json":         `<html>`,
		"bad id":           manifestJSON(entryJSON("Pro TA", "1", "a.zip", 1, good)),
		"duplicate":        manifestJSON(entryJSON("a", "1", "a.zip", 1, good), entryJSON("a", "1", "b.zip", 1, good)),
		"foreign archive":  manifestJSON(entryJSON("a", "1", "https://mirror.example/a.zip", 1, good)),
		"no size":          manifestJSON(entryJSON("a", "1", "a.zip", 0, good)),
		"short digest":     manifestJSON(entryJSON("a", "1", "a.zip", 1, "abc")),
		"missing archive":  `{"schema":1,"mods":[{"schema":1,"id":"a","name":"A","version":"1"}]}`,
		"bad minimum mode": `{"schema":1,"mods":[{"schema":1,"id":"a","name":"A","version":"1","minimumGameplay":"fast","archive":{"url":"a.zip","size":1,"sha256":"` + good + `"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := newCatalogServer(t)
			server.route("/mods/manifest.json", serveJSON(body))
			client := server.client(t)
			if result, err := client.FetchManifest(context.Background()); err == nil {
				t.Fatalf("accepted %+v", result)
			}
			if _, err := os.Stat(filepath.Join(client.CacheDir, manifestCacheName)); err == nil {
				t.Fatal("an invalid catalogue was cached")
			}
		})
	}
	// Unknown fields are tolerated so the hosted file can grow.
	server := newCatalogServer(t)
	server.route("/mods/manifest.json", serveJSON(`{"schema":1,"notice":"hello","mods":[{"schema":1,"id":"a","name":"A","version":"1","screenshot":"a.png","archive":{"url":"a.zip","size":1,"sha256":"`+good+`","mirror":"x"}}]}`))
	if _, err := server.client(t).FetchManifest(context.Background()); err != nil {
		t.Fatalf("unknown fields refused the catalogue: %v", err)
	}
}

func TestDownloadVerifies(t *testing.T) {
	server := newCatalogServer(t)
	server.route("/mods/sample.zip", serveBytes(fixtureArchive))
	client := server.client(t)
	dir := t.TempDir()

	t.Run("good", func(t *testing.T) {
		dst := filepath.Join(dir, "good.zip")
		var lastDone, lastTotal int64
		if err := client.Download(context.Background(), server.entry("/mods/sample.zip", fixtureArchive), dst, func(done, total int64) { lastDone, lastTotal = done, total }); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dst); !bytes.Equal(got, fixtureArchive) {
			t.Fatal("downloaded bytes differ")
		}
		if _, err := os.Stat(dst + ".part"); err == nil {
			t.Fatal("the part file outlived a finished download")
		}
		if lastTotal != int64(len(fixtureArchive)) || lastDone != lastTotal {
			t.Fatalf("final progress %d/%d", lastDone, lastTotal)
		}
	})
	t.Run("digest mismatch", func(t *testing.T) {
		dst := filepath.Join(dir, "digest.zip")
		entry := server.entry("/mods/sample.zip", fixtureArchive)
		entry.Archive.SHA256 = strings.Repeat("0", 64)
		if err := client.Download(context.Background(), entry, dst, nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
			t.Fatalf("Download = %v, want a digest mismatch", err)
		}
		assertNoFiles(t, dst)
	})
	t.Run("size mismatch", func(t *testing.T) {
		dst := filepath.Join(dir, "size.zip")
		entry := server.entry("/mods/sample.zip", fixtureArchive)
		entry.Archive.Size += 10
		if err := client.Download(context.Background(), entry, dst, nil); err == nil {
			t.Fatal("a size mismatch was accepted")
		}
		assertNoFiles(t, dst)
	})
	t.Run("oversized body", func(t *testing.T) {
		server.route("/mods/chunked.zip", func(w http.ResponseWriter, r *http.Request) {
			// Flushing before the end drops Content-Length, so only the
			// read limit can catch the extra bytes.
			_, _ = w.Write(fixtureArchive[:10])
			w.(http.Flusher).Flush()
			_, _ = w.Write(fixtureArchive)
		})
		dst := filepath.Join(dir, "chunked.zip")
		if err := client.Download(context.Background(), server.entry("/mods/chunked.zip", fixtureArchive), dst, nil); err == nil || !strings.Contains(err.Error(), "larger") {
			t.Fatalf("Download = %v, want an oversized refusal", err)
		}
		assertNoFiles(t, dst)
	})
	t.Run("truncated body", func(t *testing.T) {
		server.route("/mods/short.zip", serveBytes(fixtureArchive[:len(fixtureArchive)/2]))
		dst := filepath.Join(dir, "short.zip")
		if err := client.Download(context.Background(), server.entry("/mods/short.zip", fixtureArchive), dst, nil); err == nil {
			t.Fatal("a truncated archive was accepted")
		}
		assertNoFiles(t, dst)
	})
}

func assertNoFiles(t *testing.T, dst string) {
	t.Helper()
	for _, name := range []string{dst, dst + ".part"} {
		if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists after a refused download (%v)", filepath.Base(name), err)
		}
	}
}

func TestInterruptedDownloadResumes(t *testing.T) {
	server := newCatalogServer(t)
	half := len(fixtureArchive) / 2
	server.route("/mods/sample.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(fixtureArchive)))
		_, _ = w.Write(fixtureArchive[:half])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // the connection drops mid-body
	})
	client := server.client(t)
	dst := filepath.Join(t.TempDir(), "sample.zip")
	entry := server.entry("/mods/sample.zip", fixtureArchive)
	if err := client.Download(context.Background(), entry, dst, nil); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("Download = %v, want an interruption", err)
	}
	if info, err := os.Stat(dst + ".part"); err != nil || info.Size() != int64(half) {
		t.Fatalf("part after the drop = %v, %v; want %d bytes kept", info, err, half)
	}

	server.route("/mods/sample.zip", serveBytes(fixtureArchive))
	var firstDone int64 = -1
	if err := client.Download(context.Background(), entry, dst, func(done, total int64) {
		if firstDone < 0 {
			firstDone = done
		}
	}); err != nil {
		t.Fatal(err)
	}
	requests := server.seen()
	if got := requests[len(requests)-1].Header.Get("Range"); got != "bytes="+strconv.Itoa(half)+"-" {
		t.Fatalf("resume sent Range %q", got)
	}
	if firstDone != int64(half) {
		t.Fatalf("resumed progress started at %d, want %d", firstDone, half)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, fixtureArchive) {
		t.Fatal("resumed archive differs")
	}
}

func TestResumeRestartsWhenTheServerIgnoresRange(t *testing.T) {
	server := newCatalogServer(t)
	server.route("/mods/sample.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixtureArchive) // 200 whatever the Range says
	})
	client := server.client(t)
	dst := filepath.Join(t.TempDir(), "sample.zip")
	if err := os.WriteFile(dst+".part", []byte("stale prefix from another attempt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := client.Download(context.Background(), server.entry("/mods/sample.zip", fixtureArchive), dst, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, fixtureArchive) {
		t.Fatal("a restarted download kept the stale prefix")
	}
}

func TestRedirectsStayOnTheOrigin(t *testing.T) {
	elsewhere := newCatalogServer(t)
	elsewhere.route("/sample.zip", serveBytes(fixtureArchive))
	server := newCatalogServer(t)
	server.route("/mods/sample.zip", serveBytes(fixtureArchive))
	server.route("/mods/moved.zip", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mods/sample.zip", http.StatusFound)
	})
	server.route("/mods/away.zip", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/sample.zip", http.StatusFound)
	})
	client := server.client(t)
	dir := t.TempDir()

	if err := client.Download(context.Background(), server.entry("/mods/moved.zip", fixtureArchive), filepath.Join(dir, "moved.zip"), nil); err != nil {
		t.Fatalf("same-origin redirect refused: %v", err)
	}
	if err := client.Download(context.Background(), server.entry("/mods/away.zip", fixtureArchive), filepath.Join(dir, "away.zip"), nil); !errors.Is(err, ErrOriginRefused) {
		t.Fatalf("off-origin redirect = %v, want ErrOriginRefused", err)
	}
	if len(elsewhere.seen()) != 0 {
		t.Fatal("the other origin was contacted")
	}
	// A caller-supplied client gets the same policy.
	supplied := &Client{CatalogURL: client.CatalogURL, HTTP: &http.Client{}}
	if err := supplied.Download(context.Background(), server.entry("/mods/away.zip", fixtureArchive), filepath.Join(dir, "away2.zip"), nil); !errors.Is(err, ErrOriginRefused) {
		t.Fatalf("off-origin redirect with a supplied client = %v, want ErrOriginRefused", err)
	}
	// An entry naming another origin outright is refused before any request.
	if err := client.Download(context.Background(), elsewhere.entry("/sample.zip", fixtureArchive), filepath.Join(dir, "direct.zip"), nil); !errors.Is(err, ErrOriginRefused) {
		t.Fatalf("off-origin archive URL = %v, want ErrOriginRefused", err)
	}
	if len(elsewhere.seen()) != 0 {
		t.Fatal("the other origin was contacted")
	}
}

func TestStalledDownloadKeepsItsPart(t *testing.T) {
	server := newCatalogServer(t)
	server.route("/mods/sample.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(fixtureArchive)))
		_, _ = w.Write(fixtureArchive[:100])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	client := server.client(t)
	client.idle = 50 * time.Millisecond
	dst := filepath.Join(t.TempDir(), "sample.zip")
	if err := client.Download(context.Background(), server.entry("/mods/sample.zip", fixtureArchive), dst, nil); err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("Download = %v, want a stall", err)
	}
	if info, err := os.Stat(dst + ".part"); err != nil || info.Size() != 100 {
		t.Fatalf("part after a stall = %v, %v; want 100 bytes kept", info, err)
	}
}

// TestDownloadThenInstall runs the whole catalogue path against an authored
// zip: fetch, download to staging, and install with the entry's options.
func TestDownloadThenInstall(t *testing.T) {
	meta := modlibrary.Metadata{Schema: 1, ID: "sample", Name: "Sample", Version: "1.0", Controls: "community"}
	metaJSON, _ := json.Marshal(meta)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{modlibrary.MetadataFile: string(metaJSON), "sample.ufo": "authored"} {
		w, _ := writer.Create(name)
		_, _ = w.Write([]byte(body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buffer.Bytes()

	server := newCatalogServer(t)
	server.route("/mods/sample/sample-1.0.zip", serveBytes(archive))
	server.route("/mods/manifest.json", serveJSON(manifestJSON(
		`{"schema":1,"id":"sample","name":"Sample","version":"1.0","controls":"community","archive":{"url":"sample/sample-1.0.zip","size":`+strconv.Itoa(len(archive))+`,"sha256":"`+digest(archive)+`"}}`)))

	lib, err := modlibrary.Open(filepath.Join(t.TempDir(), "mods"))
	if err != nil {
		t.Fatal(err)
	}
	client := server.client(t)
	client.CacheDir = lib.Root
	result, err := client.FetchManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry := result.Manifest.Mods[0]
	if entry.ArchiveName() != "sample-1.0.zip" {
		t.Fatalf("ArchiveName = %q", entry.ArchiveName())
	}
	dst := filepath.Join(lib.StagingDir(), entry.ArchiveName())
	if err := client.Download(context.Background(), entry, dst, nil); err != nil {
		t.Fatal(err)
	}
	opts := entry.InstallOptions()
	if opts.Expect == nil || opts.Expect.ID != "sample" || opts.SHA256 != entry.Archive.SHA256 || opts.Size != entry.Archive.Size || opts.Source != entry.Archive.URL {
		t.Fatalf("InstallOptions = %+v", opts)
	}
	mod, err := lib.InstallArchive(dst, opts)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Receipt.SHA256 != entry.Archive.SHA256 || mod.Receipt.Source != entry.Archive.URL {
		t.Fatalf("receipt %+v", mod.Receipt)
	}
	mods, err := lib.Installed()
	if err != nil || len(mods) != 1 {
		t.Fatalf("Installed = %+v, %v", mods, err)
	}
}

// fakeHosts routes requests by host to in-process handlers, so a test can
// stand in for nanolathe.gg, github.com and GitHub's asset host without a
// network.
type fakeHosts map[string]http.Handler

func (f fakeHosts) RoundTrip(r *http.Request) (*http.Response, error) {
	handler, ok := f[r.URL.Host]
	if !ok {
		return nil, fmt.Errorf("fake network: no route to %s", r.URL.Host)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)
	resp := recorder.Result()
	resp.Request = r
	return resp, nil
}

const releaseURL = "https://github.com/nanolathe-gg/nanolathe-gg.github.io/releases/download/mods/sample-1.0.zip"

func TestReleaseAssetsAreArchiveURLs(t *testing.T) {
	for raw, want := range map[string]bool{
		releaseURL: true,
		"https://github.com/Nanolathe-GG/site/releases/download/mods/a.zip":      true,
		"https://github.com/someone-else/site/releases/download/mods/a.zip":      false,
		"http://github.com/nanolathe-gg/site/releases/download/mods/a.zip":       false,
		"https://github.com/nanolathe-gg/site/archive/refs/heads/main.zip":       false,
		"https://github.com/nanolathe-gg/site/releases/download/mods/../a.zip":   false,
		"https://github.com/nanolathe-gg/site/releases/download/mods/a.zip?x=1":  false,
		"https://user@github.com/nanolathe-gg/site/releases/download/mods/a.zip": false,
		"https://api.github.com/nanolathe-gg/site/releases/download/mods/a.zip":  false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := releaseAsset(u); got != want {
			t.Errorf("releaseAsset(%s) = %v, want %v", raw, got, want)
		}
	}
}

// TestReleaseAssetDownload follows a nanolathe.gg catalogue entry to a
// release asset: github.com redirects to its asset host, which is on another
// origin, and an interrupted download resumes through the same redirect.
func TestReleaseAssetDownload(t *testing.T) {
	good := strings.Repeat("a", 64)
	var assetRanges []string
	hosts := fakeHosts{
		"nanolathe.gg": serveJSON(manifestJSON(entryJSON("sample", "1.0", releaseURL, len(fixtureArchive), digest(fixtureArchive)), entryJSON("other", "1.0", "https://github.com/someone-else/x/releases/download/mods/o.zip", 1, good))),
		"github.com": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://release-assets.example/signed/sample?expires=1", http.StatusFound)
		}),
		"release-assets.example": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assetRanges = append(assetRanges, r.Header.Get("Range"))
			serveBytes(fixtureArchive)(w, r)
		}),
	}
	client := &Client{CatalogURL: DefaultCatalogURL, HTTP: &http.Client{Transport: hosts}}
	// One foreign entry refuses the whole catalogue.
	if _, err := client.FetchManifest(context.Background()); err == nil {
		t.Fatal("a release asset of another owner was accepted")
	}
	hosts["nanolathe.gg"] = serveJSON(manifestJSON(entryJSON("sample", "1.0", releaseURL, len(fixtureArchive), digest(fixtureArchive))))
	result, err := client.FetchManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry := result.Manifest.Mods[0]
	dst := filepath.Join(t.TempDir(), entry.ArchiveName())
	if err := os.WriteFile(dst+".part", fixtureArchive[:100], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := client.Download(context.Background(), entry, dst, nil); err != nil {
		t.Fatalf("release asset download: %v", err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, fixtureArchive) {
		t.Fatal("the downloaded archive differs")
	}
	if len(assetRanges) != 1 || assetRanges[0] != "bytes=100-" {
		t.Fatalf("asset host saw ranges %q, want one resume from byte 100", assetRanges)
	}
	// The asset host may be anywhere, but only over https.
	hosts["github.com"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://release-assets.example/signed/sample", http.StatusFound)
	})
	if err := client.Download(context.Background(), entry, filepath.Join(t.TempDir(), "plain.zip"), nil); !errors.Is(err, ErrOriginRefused) {
		t.Fatalf("redirect to http = %v, want ErrOriginRefused", err)
	}
}
