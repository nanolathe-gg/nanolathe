package ebitenapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"
)

func (a *app) writeDebugDeviceCapture(directory string) error {
	a.c.JoinPreRecord()
	var last *ebiten.Image
	metadata := map[string]any{"renderer": a.mode,
		"presented_tick":    "unavailable: adapter does not retain tick identity; current committed tick must not be substituted",
		"last_presented_at": a.presentedAt,
		"update_bodies":     a.bodies}
	metadata["paused_world"] = map[string]any{"valid": a.paused.valid, "recordings": a.paused.records, "reuses": a.paused.reuses}
	if a.paused.image != nil {
		bounds := a.paused.image.Bounds()
		metadata["paused_world_rgba_bytes"] = int64(bounds.Dx()) * int64(bounds.Dy()) * 4
	}
	if a.mode == RendererModern && a.gpu != nil {
		metadata["gpu"] = a.gpu.DebugSnapshot()
		last = a.gpu.DebugLastFrame()
	} else if a.img != nil {
		last = a.img
		metadata["classic_image_bounds"] = a.img.Bounds()
	}
	file, err := os.OpenFile(filepath.Join(directory, "renderer.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		err = errors.Join(json.NewEncoder(file).Encode(metadata), file.Close())
	}
	if last == nil {
		return errors.Join(err, fmt.Errorf("nanolathe: capture last frame: logical path %s, providers searched [window], expected previously presented image", directory))
	}
	imageFile, imageErr := os.OpenFile(filepath.Join(directory, "last-frame.png"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if imageErr == nil {
		// One device readback; png.Encode must not invoke GPU-backed At per pixel.
		pixels := image.NewRGBA(image.Rect(0, 0, last.Bounds().Dx(), last.Bounds().Dy()))
		last.ReadPixels(pixels.Pix)
		imageErr = errors.Join(png.Encode(imageFile, pixels), imageFile.Close())
	}
	return errors.Join(err, imageErr)
}
