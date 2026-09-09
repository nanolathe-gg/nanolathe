package main

// The load-time remaster — contract D3 of docs/DESIGN_GPU_RENDERER.md §14.4.
//
// This is the command layer's half of the detail view: on the loader goroutine,
// after the session composes and before the battle is adopted, the map's own
// authored pixels are synthesized into the 2x art the client draws at view
// scale 2 (§14.3). Nothing here is authoritative — the simulation never sees
// the view scale or the art it selects [I6] — and nothing here can fail a load:
// a part that cannot be synthesized is reported on stderr and left nil, which
// routes that art to the client's own nearest doubling (D2).

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/upscale"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// familyDetailArt is the load family the remaster reports progress under. The
// six loading bars are retail's; which loaders feed them is ours (see
// retailLoadStageOf), and the remaster is map art, so it feeds the Terrain bar.
const familyDetailArt = "detailart"

// detailArtExclusions are the sprite tool's shipped example exclusions: fire,
// explosion, smoke and reclaim art whose colours never occur in idle art and
// leak into it when they are offered as examples (§14.4; tools/mapupscale/
// featupscale's -exclude default). They exclude an entry from the EXAMPLE set
// only — a burn or reclaim sequence a feature definition names is still
// synthesized.
var detailArtExclusions = []string{"burn", "boom", "fire", "smoke", "rec"}

// buildDetailArt synthesizes the battle's 2x art: one 64x64 tile per tile-set
// entry, and a 2x bank for every feature GAF the map's plot cells name through
// their definitions (§14.4 "Coverage").
//
// progress may be nil. When it is not, the family above is reported from 0 to
// 100 across the whole job, so the loading bar moves while the synthesis runs.
// It is always driven to 100, including on the paths that synthesize nothing,
// because the bar it feeds is the mean of its families.
//
// It returns nil when there is nothing to install, which is the same thing to
// the client as no provider at all: every frame is doubled by nearest sampling.
func buildDetailArt(cs *contentSet, terrain *world.Terrain, progress content.Progress) *client.DetailArt {
	report := func(percent int) {
		if progress != nil {
			progress(familyDetailArt, percent)
		}
	}
	report(0)
	defer report(100)
	if cs == nil || cs.fs == nil || terrain == nil {
		return nil
	}
	tables, err := palette.Load(cs.fs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: upscale: no palette: logical path palettes/PALETTE.PAL, providers searched [%s], expected the shared retail palette tables: %v\n",
			cs.root, err)
		return nil
	}
	var pal [256][3]uint8
	for i := range 256 {
		r, g, b, _ := tables.RGBA(byte(i))
		pal[i] = [3]uint8{r, g, b}
	}
	alp := tables.Alpha[:]

	cache, err := upscale.DefaultCache()
	if err != nil {
		// Without a cache every load recomputes; that is slow, not fatal.
		fmt.Fprintf(os.Stderr, "nanolathe: upscale: no result cache: logical path <user cache directory>, providers searched [os.UserCacheDir], expected a cache directory: %v\n", err)
		cache = &upscale.Cache{}
	}

	banks := detailArtBankQueries(terrain)
	// The terrain is one part and each bank is one; the bar walks them in
	// order so it advances even though the parts cost wildly different time.
	parts := 1 + len(banks)
	art := &client.DetailArt{Banks: map[string]*formats.GAF{}}
	art.Tiles = buildDetailTiles(cache, terrain, pal, alp, func(done, total int) {
		if total > 0 {
			report(done * 100 / total / parts)
		}
	})
	names := make([]string, 0, len(banks))
	for name := range banks { // load-time only; no simulation order [I1]
		names = append(names, name)
	}
	sort.Strings(names)
	for index, name := range names {
		base := (index + 1) * 100 / parts
		span := 100 / parts
		bank := buildDetailBank(cache, cs.fs, name, banks[name], pal, alp, func(done, total int) {
			if total > 0 {
				report(base + done*span/total)
			}
		})
		if bank != nil {
			art.Banks[name] = bank
		}
	}
	if art.Tiles == nil && len(art.Banks) == 0 {
		return nil
	}
	return art
}

// detailArtFor is the switch of §14.4: `--auto-remaster` (default on) enables
// the load-time remaster, and off installs no provider at all, which leaves
// every asset to the client's nearest doubling. It is the one place the flag is
// read, so every route gates the same way.
func detailArtFor(opts Options, cs *contentSet, terrain *world.Terrain, progress content.Progress) *client.DetailArt {
	if !opts.AutoRemaster {
		// The family still completes: the bar it feeds is the mean of its
		// families, so a family that never reports would hold the bar short.
		if progress != nil {
			progress(familyDetailArt, 100)
		}
		return nil
	}
	return buildDetailArt(cs, terrain, progress)
}

// captureDetailArt is detailArtFor for the capture route. A capture has no F9,
// so its view scale is decided before the load: at scale 1 the provider is
// never consulted (§14.1) and synthesizing it would cost seconds for pixels no
// capture can show.
func captureDetailArt(opts Options, cs *contentSet, terrain *world.Terrain) *client.DetailArt {
	if opts.Zoom <= 1 {
		return nil
	}
	return detailArtFor(opts, cs, terrain, nil)
}

// detailArtBankQuery is one feature bank's query set: the entries the map's
// feature definitions name, and which of those are shadow twins.
type detailArtBankQuery struct {
	named  map[string]bool
	shadow map[string]bool
}

// detailArtBankQueries collects, per feature bank filename, the entries the
// map's placed features actually name (§14.4 "Coverage"). A bank can hold art
// no feature uses — trees.gaf carries three 640x480 growth frames that cost
// seconds each — and synthesizing it would lengthen the first load for nothing
// on screen.
func detailArtBankQueries(terrain *world.Terrain) map[string]detailArtBankQuery {
	out := map[string]detailArtBankQuery{}
	if terrain == nil {
		return out
	}
	seen := map[uint16]bool{}
	for i := range terrain.Plot {
		id := terrain.Plot[i].Feature()
		if seen[id] {
			continue
		}
		seen[id] = true
		def, ok := terrain.FeatureDefAt(id)
		if !ok || def == nil || def.Filename == "" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(def.Filename))
		if name == "" {
			continue
		}
		query, ok := out[name]
		if !ok {
			query = detailArtBankQuery{named: map[string]bool{}, shadow: map[string]bool{}}
			out[name] = query
		}
		for _, seq := range []string{def.SeqName, def.SeqNameBurn, def.SeqNameDie, def.SeqNameReclamate} {
			if seq = strings.ToLower(strings.TrimSpace(seq)); seq != "" {
				query.named[seq] = true
			}
		}
		// Shadow twins are flat two-colour art the search handles badly, so
		// they are named but never synthesized: the client doubles them, which
		// is what keeps a 2x shadow aligned with its 2x sprite (§14.4).
		for _, seq := range []string{def.SeqNameShad, def.SeqNameBurnShad, def.SeqNameDieShad, def.SeqNameReclamateShad} {
			if seq = strings.ToLower(strings.TrimSpace(seq)); seq != "" {
				query.shadow[seq] = true
			}
		}
	}
	return out
}

// buildDetailTiles synthesizes the detail tile set, or reports why it could not
// and returns nil.
func buildDetailTiles(cache *upscale.Cache, terrain *world.Terrain, pal [256][3]uint8, alp []byte, progress func(done, total int)) [][4096]byte {
	if len(terrain.TileSet) == 0 || len(terrain.TileIndices) == 0 {
		return nil
	}
	started := time.Now()
	tiles, cached, err := cache.Tiles2x(upscale.TerrainInput{
		Tiles:   terrain.TileSet,
		TileMap: terrain.TileIndices,
		TilesW:  int(terrain.CellW / 2),
		TilesH:  int(terrain.CellH / 2),
		Palette: pal,
		ALP:     alp,
	}, upscale.Options{Progress: progress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: upscale: terrain synthesis failed: logical path <map terrain>, providers searched [%s], expected one 64x64 tile per tile-set entry: %v\n",
			cacheDirName(cache), err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "nanolathe: upscale: terrain %d tiles %s (%s)\n", len(tiles), elapsedText(started), cachedText(cached))
	return tiles
}

// buildDetailBank synthesizes one feature bank's 2x variant, or reports why it
// could not and returns nil.
func buildDetailBank(cache *upscale.Cache, fs vfs.FSOps, name string, query detailArtBankQuery,
	pal [256][3]uint8, alp []byte, progress func(done, total int)) *formats.GAF {
	path := "anims/" + name + ".gaf"
	loaded, err := formats.LoadGAFFile(fs, path)
	if err != nil || loaded == nil {
		fmt.Fprintf(os.Stderr, "nanolathe: upscale: feature bank unreadable: logical path %s, providers searched [mounted content], expected a GAF bank to remaster: %v\n", path, err)
		return nil
	}
	// The two exclusions of §14.4 are different things and are expressed
	// separately: examples drops the art whose colours must not leak into idle
	// sprites while leaving it synthesizable; skip names what is not
	// synthesized at all — everything no definition names, and the shadow
	// twins.
	examples := filterExampleBank(loaded)
	skip := func(entryName string) bool {
		lower := strings.ToLower(strings.TrimSpace(entryName))
		return !query.named[lower] || query.shadow[lower]
	}
	started := time.Now()
	bank, cached, err := cache.Bank2x(loaded, []*formats.GAF{examples}, pal, alp, skip, upscale.Options{Progress: progress})
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: upscale: feature bank synthesis failed: logical path %s, providers searched [%s], expected a parallel 2x bank: %v\n",
			path, cacheDirName(cache), err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "nanolathe: upscale: bank %s %d frames %s (%s)\n", name, synthesizedFrames(bank), elapsedText(started), cachedText(cached))
	return bank
}

// filterExampleBank returns the bank without the entries the sprite tool's
// shipped exclusions name. The entries are shared, not copied: Bank2x reads
// them and the caller keeps the loaded bank.
func filterExampleBank(bank *formats.GAF) *formats.GAF {
	if bank == nil {
		return nil
	}
	out := &formats.GAF{Version: bank.Version, Unknown: bank.Unknown}
	for i := range bank.Entries {
		lower := strings.ToLower(bank.Entries[i].Name)
		excluded := false
		for _, part := range detailArtExclusions {
			if strings.Contains(lower, part) {
				excluded = true
				break
			}
		}
		if !excluded {
			out.Entries = append(out.Entries, bank.Entries[i])
		}
	}
	out.EntryCount = uint32(len(out.Entries))
	return out
}

// synthesizedFrames counts the frames the synthesizer actually produced; a nil
// slot is one the client doubles itself (D2).
func synthesizedFrames(bank *formats.GAF) int {
	if bank == nil {
		return 0
	}
	count := 0
	for i := range bank.Entries {
		for _, ref := range bank.Entries[i].Frames {
			if ref.Frame != nil {
				count++
			}
		}
	}
	return count
}

func cachedText(cached bool) string {
	if cached {
		return "cached"
	}
	return "computed"
}

func elapsedText(started time.Time) string {
	return time.Since(started).Round(time.Millisecond).String()
}

func cacheDirName(cache *upscale.Cache) string {
	if cache == nil || cache.Dir == "" {
		return "no cache"
	}
	return cache.Dir
}
