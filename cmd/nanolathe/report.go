package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		return fmt.Errorf("nanolathe: unknown --dump verb %q (manifest, providers, shadowed)", opts.Dump)
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
