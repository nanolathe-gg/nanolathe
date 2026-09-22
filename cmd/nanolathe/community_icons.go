package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// configuredStrategicIcons is the command boundary for the optional host path.
// The client loader always returns usable generated icons when the path is
// empty, requests defaults, or reports invalid custom art; battle setup can
// therefore publish the diagnostic without losing the existing presentation.
func configuredStrategicIcons(cat *content.Catalog, path string) (*client.StrategicIconCatalog, error) {
	return client.LoadStrategicIconCatalog(cat, path)
}
