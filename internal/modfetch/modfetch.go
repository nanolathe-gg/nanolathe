// Package modfetch is the mod library's only network client: it fetches the
// nanolathe.gg catalogue, caches the last good copy for offline display, and
// downloads archives with resume, progress and verification
// (docs/DESIGN_MODS_MUTATORS.md §5.1–§5.3).
//
// It is the one package in the module that imports net/http, and only the
// desktop command may import it (DESIGN_MODS_MUTATORS §9; guarded by
// internal/architecture). The client talks to the network only when the
// player opens the Get more mods dialog or starts a download, never at
// start-up, in battle, or from the displayless command (D5, §5.2). Installing
// a downloaded archive is modlibrary's job; this package never extracts.
package modfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/version"
)

// Archive is a catalogue entry's download: where it is, and the size and
// SHA-256 that identify it (§5.1, §5.4).
type Archive struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Entry is one downloadable mod version: its metadata, which the archive's
// own nanolathe-mod.json must agree with, and its archive.
type Entry struct {
	modlibrary.Metadata
	Archive Archive `json:"archive"`
}

// Manifest is the catalogue document. The entries' order is the display
// order (§4.2).
type Manifest struct {
	Schema int     `json:"schema"`
	Mods   []Entry `json:"mods"`
}

// DefaultCatalogURL is the hosted catalogue (§5.1, P12).
const DefaultCatalogURL = "https://nanolathe.gg/mods/manifest.json"

// catalogEnv names the development override for the catalogue URL.
const catalogEnv = "NANOLATHE_MOD_CATALOG"

// catalogHost is the only host a release build reads the catalogue from
// (§5.2, D3). Archives come from it or from nanolathe-gg release assets.
const catalogHost = "nanolathe.gg"

const (
	manifestSchema    = 1
	maxManifestBytes  = 4 << 20
	manifestCacheName = "manifest.json"
	manifestTimeout   = 30 * time.Second
	defaultIdle       = 60 * time.Second
	maxRedirects      = 5
)

// ErrOriginRefused reports a URL or redirect outside the catalogue's origin,
// or a catalogue URL the policy does not admit.
var ErrOriginRefused = errors.New("URL refused by the catalogue origin policy")

// CatalogURL returns DefaultCatalogURL unless NANOLATHE_MOD_CATALOG is set.
//
// The override is for development: it lets the flow be tried against a
// local server. It may be an http:// URL on 127.0.0.1 or localhost; any other
// http URL is refused when used. The override's origin then replaces
// nanolathe.gg as the one origin every request must stay on.
func CatalogURL() string {
	if override := strings.TrimSpace(os.Getenv(catalogEnv)); override != "" {
		return override
	}
	return DefaultCatalogURL
}

// Client fetches the catalogue and downloads archives.
type Client struct {
	CatalogURL string       // "" = CatalogURL()
	HTTP       *http.Client // nil = a client with sane timeouts that refuses redirects to another origin
	CacheDir   string       // where manifest.json is cached (the library root); "" disables the cache

	// idle is how long a download may go without receiving a byte; tests
	// shorten it.
	idle time.Duration
}

// FetchResult is a catalogue fetch. When the live fetch failed and a cached
// catalogue was usable, FromCache is set, FetchedAt is the cache's age and
// Err carries the live failure for the dialog to show.
type FetchResult struct {
	Manifest  Manifest
	FetchedAt time.Time
	FromCache bool
	Err       error // the live fetch error when FromCache
}

// origin is a URL's scheme and host, with a default port dropped.
type origin struct{ scheme, host string }

func originOf(u *url.URL) origin {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return origin{scheme: scheme, host: host}
}

func (o origin) String() string { return o.scheme + "://" + o.host }

// catalogOrigin admits a catalogue URL under the §5.2 policy and returns the
// origin every request must stay on: https on nanolathe.gg, or exactly the
// development override, which may be any https origin or http on the
// loopback names.
func catalogOrigin(raw string) (*url.URL, origin, error) {
	refuse := func(reason string) error {
		return &diagError{what: "mod catalogue URL refused: " + reason, logical: raw, providers: []string{catalogEnv, DefaultCatalogURL}, expected: "an https URL on " + catalogHost + ", or an http override on 127.0.0.1 or localhost", wrapped: ErrOriginRefused}
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Hostname() == "" {
		return nil, origin{}, refuse("not an absolute URL")
	}
	if u.User != nil {
		return nil, origin{}, refuse("credentials in the URL")
	}
	o := originOf(u)
	host := strings.ToLower(u.Hostname())
	if o.scheme == "https" && host == catalogHost {
		return u, o, nil
	}
	if override := strings.TrimSpace(os.Getenv(catalogEnv)); override != "" && raw == override {
		if o.scheme == "https" || (o.scheme == "http" && (host == "127.0.0.1" || host == "localhost")) {
			return u, o, nil
		}
		return nil, origin{}, refuse("the development override may use http only on 127.0.0.1 or localhost")
	}
	return nil, origin{}, refuse("not the nanolathe.gg origin")
}

func (c *Client) catalog() string {
	if c.CatalogURL != "" {
		return c.CatalogURL
	}
	return CatalogURL()
}

// defaultTransport bounds connection set-up and the wait for headers, but
// not a body transfer: a large archive may take minutes, and a stalled one is
// caught by the per-download idle watchdog instead.
var defaultTransport = sync.OnceValue(func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 15 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	return transport
})

// releaseOwner is the GitHub organization whose release assets a catalogue
// entry may name (§5.1, §5.2). The website repository's "mods" release holds
// the hosted archives, so the site does not carry them in its history.
const releaseOwner = "nanolathe-gg"

// releaseAsset reports whether u is a release asset download of a
// nanolathe-gg repository:
// https://github.com/nanolathe-gg/<repository>/releases/download/<tag>/<file>.
// GitHub answers it with a redirect to a signed, expiring URL on its asset
// host, so such a download may follow redirects to any https host
// (httpsRedirects). The size and SHA-256 the nanolathe.gg catalogue gives for
// the entry are what the archive is trusted by, and both are checked before
// it is installed.
func releaseAsset(u *url.URL) bool {
	if u == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || originOf(u) != (origin{scheme: "https", host: "github.com"}) {
		return false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 7 || parts[0] != "" || !strings.EqualFold(parts[1], releaseOwner) || parts[3] != "releases" || parts[4] != "download" {
		return false
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// archiveAllowed is the §5.2 rule for an archive URL: the catalogue's own
// origin, or a nanolathe-gg release asset.
func archiveAllowed(u *url.URL, allowed origin) bool {
	return u.User == nil && (originOf(u) == allowed || releaseAsset(u))
}

// redirectPolicy decides whether a request may follow a redirect to a URL.
type redirectPolicy func(*url.URL) error

// stayOn keeps every redirect on one origin: the catalogue, and an archive
// hosted beside it.
func stayOn(allowed origin) redirectPolicy {
	return func(u *url.URL) error {
		if got := originOf(u); got != allowed {
			return &diagError{what: "redirect to another origin refused", logical: u.String(), providers: []string{allowed.String()}, expected: "a redirect that stays on " + allowed.String(), wrapped: ErrOriginRefused}
		}
		return nil
	}
}

// httpsRedirects admits a redirect to any https URL without credentials: a
// release asset's download host is GitHub's to choose, and has changed
// before.
func httpsRedirects(u *url.URL) error {
	if !strings.EqualFold(u.Scheme, "https") || u.User != nil {
		return &diagError{what: "redirect off https refused", logical: u.String(), providers: []string{"github.com"}, expected: "a redirect to an https URL", wrapped: ErrOriginRefused}
	}
	return nil
}

// httpClient is the configured client with the policy applied whatever the
// caller supplied: no cookie jar, at most maxRedirects redirects, and each
// one admitted by redirect.
func (c *Client) httpClient(redirect redirectPolicy) *http.Client {
	var client http.Client
	if c.HTTP != nil {
		client = *c.HTTP
	} else {
		client.Transport = defaultTransport()
	}
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("nanolathe: mod download stopped after %d redirects", maxRedirects)
		}
		return redirect(req.URL)
	}
	return &client
}

// newRequest is a GET that identifies the build and nothing else (§5.2).
func newRequest(ctx context.Context, target string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nanolathe/"+version.ProfileID())
	return req, nil
}

// FetchManifest fetches the catalogue live. On success the bytes are cached
// in CacheDir; on failure the cached catalogue, if it still validates against
// the current origin, is returned with FromCache and the live error. A
// catalogue URL the policy refuses is an error with no fallback.
func (c *Client) FetchManifest(ctx context.Context) (FetchResult, error) {
	catalog := c.catalog()
	base, allowed, err := catalogOrigin(catalog)
	if err != nil {
		return FetchResult{}, err
	}
	manifest, liveErr := c.fetchLive(ctx, base, allowed)
	if liveErr == nil {
		// A cache that cannot be written only costs the offline copy.
		_ = c.writeCache(manifest)
		return FetchResult{Manifest: manifest, FetchedAt: time.Now()}, nil
	}
	if cached, at, ok := c.readCache(base, allowed); ok {
		return FetchResult{Manifest: cached, FetchedAt: at, FromCache: true, Err: liveErr}, nil
	}
	return FetchResult{}, liveErr
}

// CachedManifest returns the last fetched catalogue without using the
// network, when a cache exists and still validates against the current
// origin. A save that names a mod which is not installed uses it to say
// whether the catalogue offers that version (docs/DESIGN_MODS_MUTATORS.md
// §7.3 step 2); the client talks to the network only for a fetch the
// player asks for (§5.2, D5).
func (c *Client) CachedManifest() (Manifest, time.Time, bool) {
	base, allowed, err := catalogOrigin(c.catalog())
	if err != nil {
		return Manifest{}, time.Time{}, false
	}
	return c.readCache(base, allowed)
}

// Offers reports the entry that lists a mod id and version, if any.
func (m Manifest) Offers(id, version string) (Entry, bool) {
	for _, e := range m.Mods {
		if e.ID == id && e.Version == version {
			return e, true
		}
	}
	return Entry{}, false
}

func (c *Client) fetchLive(ctx context.Context, base *url.URL, allowed origin) (Manifest, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, manifestTimeout)
		defer cancel()
	}
	fail := func(what string, wrapped error) error {
		return &diagError{what: what, logical: base.String(), providers: []string{allowed.String()}, expected: "a schema 1 mod catalogue", wrapped: wrapped}
	}
	req, err := newRequest(ctx, base.String())
	if err != nil {
		return Manifest{}, fail("building the catalogue request failed: "+err.Error(), err)
	}
	resp, err := c.httpClient(stayOn(allowed)).Do(req)
	if err != nil {
		return Manifest{}, fail("fetching the mod catalogue failed: "+err.Error(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fail("fetching the mod catalogue failed: HTTP "+strconv.Itoa(resp.StatusCode), nil)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, fail("reading the mod catalogue failed: "+err.Error(), err)
	}
	if len(raw) > maxManifestBytes {
		return Manifest{}, fail(fmt.Sprintf("mod catalogue is larger than %d bytes", maxManifestBytes), nil)
	}
	return parseManifest(raw, base, allowed)
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// parseManifest decodes and validates a catalogue. Each entry is held to the
// metadata contract, may appear once, and must name an archive of positive
// size and a SHA-256 on the catalogue's own origin; relative archive URLs are
// resolved against the catalogue URL. Unknown fields are tolerated so the
// hosted file can grow without breaking released clients; a change in
// meaning is a new schema. One bad entry refuses the whole catalogue.
func parseManifest(raw []byte, base *url.URL, allowed origin) (Manifest, error) {
	fail := func(what string) error {
		return &diagError{what: what, logical: base.String(), providers: []string{allowed.String()}, expected: "a schema 1 mod catalogue"}
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fail("reading the mod catalogue failed: " + err.Error())
	}
	if manifest.Schema != manifestSchema {
		return Manifest{}, fail(fmt.Sprintf("mod catalogue schema %d is not supported", manifest.Schema))
	}
	seen := make(map[string]bool, len(manifest.Mods))
	for i := range manifest.Mods {
		entry := &manifest.Mods[i]
		if err := entry.Metadata.Validate(); err != nil {
			return Manifest{}, fail(fmt.Sprintf("mod catalogue entry %d is invalid: %v", i, err))
		}
		key := entry.ID + "@" + entry.Version
		if seen[key] {
			return Manifest{}, fail("mod catalogue lists " + key + " twice")
		}
		seen[key] = true
		archiveURL, err := base.Parse(entry.Archive.URL)
		if err != nil || entry.Archive.URL == "" {
			return Manifest{}, fail(fmt.Sprintf("mod catalogue entry %s has an unreadable archive URL", key))
		}
		if !archiveAllowed(archiveURL, allowed) {
			return Manifest{}, fail(fmt.Sprintf("mod catalogue entry %s downloads from another origin", key))
		}
		entry.Archive.URL = archiveURL.String()
		if entry.Archive.Size <= 0 {
			return Manifest{}, fail(fmt.Sprintf("mod catalogue entry %s has no archive size", key))
		}
		if !sha256Pattern.MatchString(entry.Archive.SHA256) {
			return Manifest{}, fail(fmt.Sprintf("mod catalogue entry %s has no valid SHA-256", key))
		}
		entry.Archive.SHA256 = strings.ToLower(entry.Archive.SHA256)
	}
	return manifest, nil
}

func (c *Client) cachePath() string { return filepath.Join(c.CacheDir, manifestCacheName) }

// writeCache replaces the cached catalogue through a temporary file and a
// rename, so a reader never sees a partial copy. It stores the validated
// catalogue, whose archive URLs are already absolute, rather than the served
// bytes: a relative URL would let a cache fetched from one origin validate
// against another.
func (c *Client) writeCache(manifest Manifest) error {
	if c.CacheDir == "" {
		return nil
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.CacheDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.CacheDir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(raw)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(writeErr, closeErr)
	}
	if err := os.Rename(tmp.Name(), c.cachePath()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// readCache returns the cached catalogue and its modification time when it
// still validates against the current origin. A cache fetched from another
// origin (a development override, say) therefore never masquerades as the
// hosted catalogue.
func (c *Client) readCache(base *url.URL, allowed origin) (Manifest, time.Time, bool) {
	if c.CacheDir == "" {
		return Manifest{}, time.Time{}, false
	}
	info, err := os.Stat(c.cachePath())
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return Manifest{}, time.Time{}, false
	}
	raw, err := os.ReadFile(c.cachePath())
	if err != nil {
		return Manifest{}, time.Time{}, false
	}
	manifest, err := parseManifest(raw, base, allowed)
	if err != nil {
		return Manifest{}, time.Time{}, false
	}
	return manifest, info.ModTime(), true
}

// ArchiveName is the file name §5.3 downloads an entry to inside the
// library's staging directory: <id>-<version>.zip.
func (e Entry) ArchiveName() string { return e.ID + "-" + e.Version + ".zip" }

// InstallOptions are the library options a catalogue install needs: the
// entry as the metadata its archive must match, and the archive identity.
// The caller adds Validate and Progress.
func (e Entry) InstallOptions() modlibrary.InstallOptions {
	expect := e.Metadata
	return modlibrary.InstallOptions{Expect: &expect, SHA256: e.Archive.SHA256, Size: e.Archive.Size, Source: e.Archive.URL}
}

// Download fetches an entry's archive to dst (§5.3 steps 1–2). Bytes go to
// dst+".part"; an existing part is resumed with a range request, and kept if
// the server answers 206, or restarted if it answers 200. A transfer that
// fails part-way (a dropped connection, a cancelled context, a stall) keeps
// the part so a later call resumes it. A completed transfer is checked
// against the entry's size and SHA-256: a mismatch deletes the part, and a
// match renames it to dst.
func (c *Client) Download(ctx context.Context, e Entry, dst string, progress func(done, total int64)) error {
	_, allowed, err := catalogOrigin(c.catalog())
	if err != nil {
		return err
	}
	provider := allowed.String()
	fail := func(what string, wrapped error) error {
		return &diagError{what: what, logical: e.Archive.URL, providers: []string{provider}, expected: fmt.Sprintf("the %d-byte archive with SHA-256 %s", e.Archive.Size, e.Archive.SHA256), wrapped: wrapped}
	}
	target, err := url.Parse(e.Archive.URL)
	if err != nil || !target.IsAbs() {
		return fail("mod archive URL is not absolute", ErrOriginRefused)
	}
	if !archiveAllowed(target, allowed) {
		return fail("mod archive URL is on another origin", ErrOriginRefused)
	}
	redirect := stayOn(allowed)
	if releaseAsset(target) {
		redirect = httpsRedirects
	}
	if e.Archive.Size <= 0 || !sha256Pattern.MatchString(e.Archive.SHA256) {
		return fail("mod catalogue entry has no archive identity", nil)
	}

	part := dst + ".part"
	var offset int64
	if info, err := os.Stat(part); err == nil && info.Mode().IsRegular() {
		offset = info.Size()
	}
	if offset > e.Archive.Size {
		_ = os.Remove(part)
		offset = 0
	}
	if offset < e.Archive.Size {
		if err := c.transfer(ctx, redirect, target, part, offset, e.Archive.Size, progress, fail); err != nil {
			return err
		}
	}

	sum, size, err := hashFile(part)
	if err != nil {
		return fail("reading the downloaded archive failed: "+err.Error(), err)
	}
	if size != e.Archive.Size {
		_ = os.Remove(part)
		return fail(fmt.Sprintf("downloaded archive is %d bytes", size), nil)
	}
	if !strings.EqualFold(sum, e.Archive.SHA256) {
		_ = os.Remove(part)
		return fail("downloaded archive has SHA-256 "+sum, nil)
	}
	if err := os.Rename(part, dst); err != nil {
		return fail("committing the downloaded archive failed: "+err.Error(), err)
	}
	return nil
}

// transfer performs one GET into part, appending from offset when the server
// honours the range.
func (c *Client) transfer(ctx context.Context, redirect redirectPolicy, target *url.URL, part string, offset, size int64,
	progress func(done, total int64), fail func(string, error) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := c.idle
	if idle <= 0 {
		idle = defaultIdle
	}
	stalled := false
	var stallMu sync.Mutex
	watchdog := time.AfterFunc(idle, func() {
		stallMu.Lock()
		stalled = true
		stallMu.Unlock()
		cancel()
	})
	defer watchdog.Stop()
	interrupted := func(err error) error {
		stallMu.Lock()
		wasStalled := stalled
		stallMu.Unlock()
		if wasStalled {
			return fail(fmt.Sprintf("mod download stalled for %s; it resumes from %s on the next attempt", idle, part), err)
		}
		return fail("mod download interrupted: "+err.Error(), err)
	}

	req, err := newRequest(ctx, target.String())
	if err != nil {
		return fail("building the download request failed: "+err.Error(), err)
	}
	// Identity encoding keeps the byte count and range offsets those of the
	// archive itself rather than of a transparently decompressed stream.
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := c.httpClient(redirect).Do(req)
	if err != nil {
		return interrupted(err)
	}
	defer resp.Body.Close()

	flags := os.O_WRONLY | os.O_CREATE
	switch {
	case offset > 0 && resp.StatusCode == http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset || (total >= 0 && total != size) {
			_ = os.Remove(part)
			return fail("the server answered the resume with another range: "+resp.Header.Get("Content-Range"), nil)
		}
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		offset = 0 // the server ignored the range: start over
		flags |= os.O_TRUNC
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return fail("the server refused to resume the download", nil)
	default:
		return fail("mod download failed: HTTP "+strconv.Itoa(resp.StatusCode), nil)
	}
	if resp.ContentLength >= 0 && offset+resp.ContentLength != size {
		_ = os.Remove(part)
		return fail(fmt.Sprintf("the server is sending %d bytes", offset+resp.ContentLength), nil)
	}

	file, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return fail("writing the download failed: "+err.Error(), err)
	}
	counter := &progressWriter{done: offset, total: size, report: progress}
	counter.emit()
	body := &idleReader{reader: resp.Body, timer: watchdog, idle: idle}
	// One byte past the expected size is read so an oversized body is caught.
	written, copyErr := io.Copy(io.MultiWriter(file, counter), io.LimitReader(body, size-offset+1))
	closeErr := file.Close()
	if copyErr != nil {
		return interrupted(copyErr)
	}
	if closeErr != nil {
		return fail("writing the download failed: "+closeErr.Error(), closeErr)
	}
	switch got := offset + written; {
	case got > size:
		_ = os.Remove(part)
		return fail(fmt.Sprintf("downloaded archive is larger than %d bytes", size), nil)
	case got < size:
		// The server ended the body cleanly but short: that is its complete
		// answer, not an interruption, so there is nothing to resume.
		_ = os.Remove(part)
		return fail(fmt.Sprintf("downloaded archive is truncated at %d bytes", got), nil)
	}
	return nil
}

// parseContentRange reads "bytes <start>-<end>/<total>"; total is -1 when
// the server sends "*".
func parseContentRange(value string) (start, total int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(value), "bytes ")
	if !found {
		return 0, 0, false
	}
	span, totalText, found := strings.Cut(spec, "/")
	if !found {
		return 0, 0, false
	}
	startText, _, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if totalText == "*" {
		return start, -1, true
	}
	total, err = strconv.ParseInt(totalText, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return start, total, true
}

// idleReader re-arms the stall watchdog whenever bytes arrive.
type idleReader struct {
	reader io.Reader
	timer  *time.Timer
	idle   time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.timer.Reset(r.idle)
	}
	return n, err
}

// progressWriter reports downloaded bytes against the archive size.
type progressWriter struct {
	done, total int64
	report      func(done, total int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.done += int64(len(p))
	w.emit()
	return len(p), nil
}

func (w *progressWriter) emit() {
	if w.report != nil {
		w.report(w.done, w.total)
	}
}

func hashFile(name string) (string, int64, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// diagError is the AGENTS.md diagnostic shape, wrapping its cause.
type diagError struct {
	what, logical, expected string
	providers               []string
	wrapped                 error
}

func (e *diagError) Error() string {
	return fmt.Sprintf("nanolathe: %s: logical path %s, providers searched [%s], expected %s",
		e.what, e.logical, strings.Join(e.providers, ", "), e.expected)
}

func (e *diagError) Unwrap() error { return e.wrapped }
