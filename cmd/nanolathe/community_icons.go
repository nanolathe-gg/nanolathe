package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// configuredStrategicIcons is the command boundary for the optional host path.
// The client loader always returns usable generated icons when the path is
// empty, requests defaults, or reports invalid custom art; battle setup can
// therefore publish the diagnostic without losing the existing presentation.
func configuredStrategicIcons(cat *content.Catalog, path string) (*client.StrategicIconCatalog, error) {
	resolved, err := discoverStrategicIconConfig(path)
	if err != nil {
		icons, _ := client.LoadStrategicIconCatalog(cat, "")
		return icons, err
	}
	return client.LoadStrategicIconCatalog(cat, resolved)
}

// discoverStrategicIconConfig lets the existing host preference name either
// its exact INI or one package/config directory. The recognized relative
// locations are the ones authored by the inspected community draw packages;
// a directory must resolve uniquely so choosing one package can never silently
// select another
// ([Shared draw-DLL interface, Megamap](../../research/extensions/draw-engine-interface.md#megamap)) [I6].
func discoverStrategicIconConfig(selection string) (string, error) {
	trimmed := strings.TrimSpace(strings.SplitN(selection, ";", 2)[0])
	if trimmed == "" {
		return selection, nil
	}
	path := filepath.FromSlash(strings.ReplaceAll(trimmed, `\`, "/"))
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		// Literal file paths, including missing ones, retain the client's
		// existing normalization and diagnostic.
		return selection, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", strategicIconDiscoveryError(path, nil, err)
	}
	candidates, err := strategicIconConfigsInDirectory(absolute)
	if err != nil {
		return "", strategicIconDiscoveryError(absolute, nil, err)
	}
	sort.Strings(candidates)
	if len(candidates) != 1 {
		return "", strategicIconDiscoveryError(absolute, candidates, nil)
	}
	return candidates[0], nil
}

func strategicIconConfigsInDirectory(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var candidates []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), "iconcfg.ini") {
			candidates = append(candidates, filepath.Join(root, entry.Name()))
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.EqualFold(entry.Name(), "Icon") && !strings.EqualFold(entry.Name(), "ZIcon") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		children, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if !child.IsDir() && strings.EqualFold(child.Name(), "iconcfg.ini") {
				candidates = append(candidates, filepath.Join(dir, child.Name()))
			}
		}
	}
	return candidates, nil
}

func strategicIconDiscoveryError(root string, candidates []string, cause error) error {
	detail := "none found"
	if len(candidates) > 0 {
		detail = "found " + strings.Join(candidates, ", ")
	}
	if cause != nil {
		detail = cause.Error()
	}
	return fmt.Errorf("nanolathe: strategic icon configuration discovery failed: logical path %s, providers searched [filesystem], expected exactly one iconcfg.ini at iconcfg.ini, Icon/iconcfg.ini, or ZIcon/iconcfg.ini: %s", root, detail)
}
