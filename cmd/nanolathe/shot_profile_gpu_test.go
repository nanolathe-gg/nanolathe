package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareShotImagesThresholdAndDiff(t *testing.T) {
	dir := t.TempDir()
	classicPath := filepath.Join(dir, "classic.png")
	classic := image.NewRGBA(image.Rect(0, 0, 2, 1))
	classic.SetRGBA(0, 0, color.RGBA{R: 10, A: 255})
	modern := image.NewRGBA(image.Rect(0, 0, 2, 1))
	modern.SetRGBA(0, 0, color.RGBA{R: 11, A: 255})

	if err := compareShotImages(classicPath, classic, modern, 0); err == nil {
		t.Fatal("max 0 accepted a one-pixel difference")
	}
	if _, err := os.Stat(shotSiblingPath(classicPath, ".diff.png")); err != nil {
		t.Fatalf("threshold failure did not save diff image: %v", err)
	}
	if err := compareShotImages(classicPath, classic, modern, 1); err != nil {
		t.Fatalf("max 1 rejected a one-pixel difference: %v", err)
	}
}
