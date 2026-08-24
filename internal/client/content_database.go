package client

import (
	"path/filepath"
	"strings"

	"kaijuengine.com/engine/assets"
)

// kaijuContentDatabase maps Kaiju's logical asset names to the directory
// layout copied by `make kaiju-content`. Kaiju requests names such as
// "swapchain.renderpass" and "basic.material"; a plain FileDatabase would
// incorrectly look for those files at the content root.
type kaijuContentDatabase struct {
	assets.Database
}

func newKaijuContentDatabase(root string) (assets.Database, error) {
	db, err := assets.NewFileDatabase(root)
	if err != nil {
		return nil, err
	}
	return &kaijuContentDatabase{Database: db}, nil
}

func (d *kaijuContentDatabase) Cache(key string, data []byte) {
	d.Database.Cache(d.path(key), data)
}

func (d *kaijuContentDatabase) CacheRemove(key string) {
	d.Database.CacheRemove(d.path(key))
}

func (d *kaijuContentDatabase) Read(key string) ([]byte, error) {
	return d.Database.Read(d.path(key))
}

func (d *kaijuContentDatabase) ReadText(key string) (string, error) {
	return d.Database.ReadText(d.path(key))
}

func (d *kaijuContentDatabase) Exists(key string) bool {
	return d.Database.Exists(d.path(key))
}

func (d *kaijuContentDatabase) path(key string) string {
	key = filepath.ToSlash(key)
	if strings.HasPrefix(key, "editor/") || filepath.IsAbs(key) {
		return key
	}
	switch filepath.Ext(key) {
	case ".bin":
		return filepath.ToSlash(filepath.Join("fonts", key))
	case ".fbx", ".gltf":
		return filepath.ToSlash(filepath.Join("meshes", key))
	case ".png":
		for _, dir := range []string{"textures", "fonts", "meshes"} {
			candidate := filepath.ToSlash(filepath.Join(dir, key))
			if d.Database.Exists(candidate) {
				return candidate
			}
		}
		return filepath.ToSlash(filepath.Join("textures", key))
	case ".css", ".html":
		return filepath.ToSlash(filepath.Join("ui", key))
	case ".material":
		return filepath.ToSlash(filepath.Join("renderer/materials", key))
	case ".renderpass":
		return filepath.ToSlash(filepath.Join("renderer/passes", key))
	case ".shaderpipeline":
		return filepath.ToSlash(filepath.Join("renderer/pipelines", key))
	case ".shader":
		return filepath.ToSlash(filepath.Join("renderer/shaders", key))
	case ".spv":
		return filepath.ToSlash(filepath.Join("renderer/spv", key))
	default:
		return key
	}
}
