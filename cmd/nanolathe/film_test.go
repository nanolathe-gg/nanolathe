package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/film"
)

// A clean capture crops the composed surface by the chrome insets instead of
// asking the camera for a second viewport contract, so the two definitions of
// that inset must stay the same number [03 §4.1]. If the chrome ever moves,
// this fails rather than quietly shifting every clean frame.
func TestFilmChromeInsetsMatchTheCameraViewport(t *testing.T) {
	if film.ChromeInsetX != int(camera.OriginX) {
		t.Fatalf("film chrome inset X is %d, camera viewport inset is %d", film.ChromeInsetX, camera.OriginX)
	}
	if film.ChromeInsetY != int(camera.OriginY) {
		t.Fatalf("film chrome inset Y is %d, camera viewport inset is %d", film.ChromeInsetY, camera.OriginY)
	}
}

func TestFilmFlagsParse(t *testing.T) {
	opts, err := parseFlags([]string{"--film", "reel.json", "--film-out", "-", "--film-frames", "120"}, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Film != "reel.json" || opts.FilmOut != "-" || opts.FilmFrames != 120 {
		t.Fatalf("film options = %q %q %d", opts.Film, opts.FilmOut, opts.FilmFrames)
	}
}

// A film that streams frames to stdout cannot share it with a banner or a
// report: the encoder reads raw pixels.
func TestFilmSinkRejectsAnUnnamedDestination(t *testing.T) {
	if _, err := newFilmSink("", 16, 16); err == nil {
		t.Fatal("an empty --film-out was accepted")
	}
}
