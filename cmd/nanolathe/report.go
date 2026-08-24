package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	contentpkg "github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// report prints what phase 0 can prove: the mounted set, its identity, and any
// mount-time notes. Later phases replace this with a session.
func report(opts Options, content *contentSet, out *os.File) error {
	for _, note := range content.notes {
		fmt.Fprintf(out, "note: %s\n", note)
	}

	manifestHash, err := content.fs.ManifestHash()
	if err != nil {
		return err
	}
	records, err := content.fs.Manifest(vfs.ManifestOptions{})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "root: %s\n", content.root)
	fmt.Fprintf(out, "providers: %d\n", content.fs.MountCount())
	fmt.Fprintf(out, "files: %d\n", len(records))
	fmt.Fprintf(out, "manifest: %s\n", manifestHash)

	switch opts.Dump {
	case "":
	case "providers":
		dumpProviders(content, out)
	case "manifest":
		for _, record := range records {
			fmt.Fprintf(out, "%s\t%s\t%d\n", record.LogicalPath, filepath.Base(record.ProviderID), record.Size)
		}
	case "shadowed":
		for _, record := range records {
			if len(record.Shadowed) == 0 {
				continue
			}
			shadows := make([]string, 0, len(record.Shadowed))
			for _, id := range record.Shadowed {
				shadows = append(shadows, filepath.Base(id))
			}
			fmt.Fprintf(out, "%s\twinner=%s\tshadowed=%s\n",
				record.LogicalPath, filepath.Base(record.ProviderID), strings.Join(shadows, ","))
		}
	default:
		if strings.HasPrefix(opts.Dump, "heightAt=") {
			if err := dumpHeightAt(content, opts, out); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("nanolathe: unknown --dump verb %q (manifest, providers, shadowed)", opts.Dump)
		}
	}

	if opts.Map != "" {
		return reportMap(content, opts.Map, out)
	}
	return nil
}

// dumpProviders prints the mounted set in precedence order — the order a
// lookup actually walks.
func dumpProviders(content *contentSet, out *os.File) {
	for index, provider := range content.fs.Providers() {
		fmt.Fprintf(out, "%2d %-24s %-10s priority=%-4d %6d files\n",
			index, filepath.Base(provider.ID), provider.Type, provider.Priority, provider.Files)
	}
}

// reportMap resolves a map by name the way later phases will: the OTA header
// and the TNT terrain must both exist before a session can start.
func reportMap(content *contentSet, name string, out *os.File) error {
	base := "maps/" + strings.TrimSuffix(name, filepath.Ext(name))
	for _, suffix := range []string{".ota", ".tnt"} {
		logical := base + suffix
		info, err := content.fs.Stat(logical)
		if err != nil {
			return &missingProductError{
				what:      "map is missing a required file",
				logical:   logical,
				providers: providerNames(content.fs),
				expected:  "an OTA header and a TNT terrain of the same name",
			}
		}
		fmt.Fprintf(out, "map%s: %s (%d bytes, %s)\n",
			suffix, info.Path, info.Size, filepath.Base(info.Source.SourcePath))
	}
	return nil
}

// dumpHeightAt handles --dump heightAt=x,z for Gate 1 headless verification [PLAN_04].
func dumpHeightAt(cs *contentSet, opts Options, out *os.File) error {
	if opts.Map == "" {
		return fmt.Errorf("nanolathe: --dump heightAt requires --map")
	}
	coords := strings.TrimPrefix(opts.Dump, "heightAt=")
	parts := strings.Split(coords, ",")
	if len(parts) != 2 {
		return fmt.Errorf("nanolathe: --dump heightAt expects x,z")
	}
	x, err1 := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	z, err2 := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("nanolathe: --dump heightAt expects integer x,z")
	}
	cat, err := contentpkg.Compile(cs.fs)
	if err != nil {
		return fmt.Errorf("nanolathe: catalog: %w", err)
	}
	terrain, err := world.Load(cs.fs, cat, opts.Map)
	if err != nil {
		return fmt.Errorf("nanolathe: terrain %q: %w", opts.Map, err)
	}
	h := terrain.HeightAt(numeric.Fixed(x), numeric.Fixed(z))
	fmt.Fprintf(out, "heightAt %d,%d = %d (%.2f) sea %d\n", x, z, int64(h), float64(h)/65536, int64(terrain.SeaLevelWorld()))
	fmt.Fprintf(out, "coarse %d,%d = %d\n", x>>20, z>>20, int64(terrain.CoarseHeightAt(world.WorldToCell(numeric.Fixed(x)), world.WorldToCell(numeric.Fixed(z)))))
	return nil
}
