package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/film"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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

func TestFilmSceneCutFailureStopsCaptureBeforeWriting(t *testing.T) {
	script, err := film.Parse([]byte(`{"shots":[{"ticks":1},{"name":"incoming","ticks":1,"scene":{"map":"missing"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("map unavailable")
	var loaded []string
	g := &filmGame{script: script, loadScene: func(scene film.Scene) error {
		loaded = append(loaded, scene.Map)
		if scene.Map == "missing" {
			return cause
		}
		return nil
	}}
	for frame := 0; frame < 3; frame++ {
		cursor, _ := script.At(frame)
		if ok := g.prepareScene(cursor); ok != (frame < 2) {
			t.Fatalf("frame %d prepare = %v", frame, ok)
		}
	}
	if len(loaded) != 2 || !g.done || !errors.Is(g.err, cause) || !strings.Contains(g.err.Error(), "incoming") || g.drawn != 0 {
		t.Fatalf("capture did not preserve the scene failure: loads=%v, done=%v, err=%v, drawn=%d", loaded, g.done, g.err, g.drawn)
	}
	if err := g.Update(); err != ebiten.Termination {
		t.Fatalf("failed capture keeps running: %v", err)
	}
}

func TestFilmExplicitAnchorBounds(t *testing.T) {
	terrain := &world.Terrain{PlayRight: 2000, PlayBottom: 1600}
	if x, z, _, err := filmBattleCentre(film.Scene{Anchor: []int32{1000, 800}}, terrain); err != nil || x != 1000 || z != 800 {
		t.Fatalf("explicit anchor = %d,%d, %v", x, z, err)
	}
	for _, anchor := range [][]int32{{}, {1}, {-1, 800}, {2000, 800}, {1000, 1600}} {
		if _, _, _, err := filmBattleCentre(film.Scene{Anchor: anchor}, terrain); err == nil {
			t.Fatalf("invalid anchor accepted: %v", anchor)
		}
	}
}

func TestFilmAirRosterUsesAuthoredFlightAboveWater(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root, Map: "Coast to Coast", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	sess, _, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mixed", "armor", "air"} {
		roster, err := filmRoster(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, side := range roster {
			for _, unit := range side {
				if _, ok := sess.Catalog.Unit(unit); !ok {
					t.Fatalf("%s roster unit %s missing from authored catalog", name, unit)
				}
			}
		}
	}
	scene := film.Scene{Kind: "battle", Roster: "air", PerSide: 4, Anchor: []int32{sess.World.PlayRight / 2, sess.World.PlayBottom / 2}}
	if _, _, err := stageFilmScene(scene, sess); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || !u.Def.CanFly {
			continue
		}
		found = true
		wantY := movement.CruiseAltitudeForOffset(sess.World, u.X, u.Z, u.Def.CruiseAlt)
		if u.Move.Mode != 2 || u.Y != wantY || u.Y <= numeric.FixedFromInt(int64(sess.World.SeaLevel)) {
			t.Fatalf("%s mode=%d altitude=%d, want airborne at %d above sea", u.Def.UnitName, u.Move.Mode, u.Y, wantY)
		}
	}
	if !found {
		t.Fatal("air scene contained no aircraft")
	}
}
