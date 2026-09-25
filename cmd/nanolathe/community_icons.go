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

// battleStrategicIcons loads a battle's strategic icons. An explicit
// `presentation.strategicIconConfig` always wins. Left empty, the running
// content's own configuration is used when one of roots holds exactly one
// (DESIGN_GPU_RENDERER §18.7); finding none there is silent and keeps the
// generated catalog.
func battleStrategicIcons(cat *content.Catalog, preference string, roots []string) (*client.StrategicIconCatalog, error) {
	if strings.TrimSpace(preference) == "" {
		found, err := automaticStrategicIconConfig(roots)
		if err != nil {
			icons, _ := client.LoadStrategicIconCatalog(cat, "")
			return icons, err
		}
		preference = found
	}
	return configuredStrategicIcons(cat, preference)
}

// strategicIconSearchRoots is where an empty preference looks: the running
// mod's directory, or a manual root stack from its last root to its first,
// the order in which the stack's roots win. The base install alone is never
// searched.
func strategicIconSearchRoots(cs *contentSet) []string {
	if cs == nil {
		return nil
	}
	if cs.mod != nil {
		return []string{cs.mod.Dir}
	}
	if !cs.manualRoots {
		return nil
	}
	roots := make([]string, 0, len(cs.roots))
	for i := len(cs.roots) - 1; i >= 0; i-- {
		roots = append(roots, cs.roots[i])
	}
	return roots
}

// automaticStrategicIconConfig takes the first root, in order, that holds an
// icon configuration in one of the recognised places. A root with none, or
// one that cannot be read, is passed over silently; a root with several is
// ambiguous and reported, so one package is never chosen over another by
// directory order. It returns "" when no root holds one.
func automaticStrategicIconConfig(roots []string) (string, error) {
	for _, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		candidates, err := strategicIconConfigsInDirectory(absolute)
		if err != nil {
			continue
		}
		sort.Strings(candidates)
		switch len(candidates) {
		case 0:
			continue
		case 1:
			return candidates[0], nil
		default:
			return "", strategicIconDiscoveryError(absolute, candidates, nil)
		}
	}
	return "", nil
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
