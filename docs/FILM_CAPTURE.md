# Film capture

`--film` composes a scripted sequence of presented frames offline and writes
them as PNGs or as a raw stream an encoder reads. It is how promotional and
demonstration footage is made: a film script names a scene, the shots that look
at it, the camera move inside each shot and the titles drawn over them.

```
tools/film films/announce.json /tmp/announce.mp4
```

`tools/film` builds the binary, streams packed RGBA on stdout and hands it to
ffmpeg; nothing intermediate is written, so a 4K reel costs no disk beyond the
result. Retail assets are required. Keep the output outside the repository.

To inspect frames instead of encoding them:

```
go run ./cmd/nanolathe --film films/announce.json --film-out /tmp/frames
go run ./cmd/nanolathe --film films/announce.json --film-out /tmp/frames --film-frames 30
```

`--film-frames` stops early, which is how a framing or a title is checked
without paying for the whole sequence.

## Why it is not a screen recording

A capture is not paced. A frame that takes half a second to compose still lands
on its own slot in the sequence, so the result is correct at its scripted rate
even when the scene is far too heavy to play at that rate. Three things follow:

* **Resolution is free of the display.** The frame is read back from the
  composed surface, not from a window, so 1440p or 4K costs time and nothing
  else.
* **The rate is exact.** The authoritative simulation stays at 30 Hz
  [01 §4.1]. A capture rate is a whole multiple of it, and each presented frame
  is composed at its own exact blend fraction `k/n` — never a wall-clock
  sample. This is the Enhanced interpolation path the modern window already
  uses (DESIGN_GPU_RENDERER §13.5); the film supplies the fraction instead of
  the host clock.
* **It is reproducible.** Two runs of one script produce byte-identical
  frames, so a shot can be re-rendered after a tuning change and cut into the
  same timeline.

Sprite and animation frame indices and the nanoframe reveal are never
interpolated (§13.5), so effects still step at 30 Hz inside a 60 FPS sequence,
exactly as they do in the window.

## Script format

A script is JSON. Unknown fields are rejected — a silently ignored camera key
is a whole wasted render — and validation reports every problem at once.

```json
{
  "width": 1920, "height": 1080, "fps": 60,
  "clean": true, "letterbox": 0.055, "messages": false,
  "scene": {"kind": "battle", "map": "Great Divide", "seed": 7,
            "pre_ticks": 240, "per_side": 160, "buildings": 16,
            "factories": true, "fog": false},
  "shots": [{
    "name": "opening", "ticks": 180,
    "camera": [{"tick": 0, "x": 0, "z": 40, "zoom": 0.62},
               {"tick": 180, "x": 0, "z": 0, "zoom": 0.82, "ease": "inout"}],
    "text": [{"at": 20, "ticks": 140, "style": "title", "anim": "wipe",
              "in": 22, "out": 24, "lines": ["NANOLATHE"]}]
  }]
}
```

| Field | Meaning |
| --- | --- |
| `width`, `height` | written frame size; even sides, for the usual 4:2:0 encoders |
| `fps` | capture rate; a multiple of 30, so one tick covers a whole number of frames |
| `clean` | write the world viewport only, with no battle interface (below) |
| `letterbox` | bar height as a fraction of the frame, each bar, clamped to a quarter |
| `messages` | keep the battle message column; a film silences it by default |
| `shots[].ticks` | the shot's length in simulation ticks; shots run back to back |

All times are simulation ticks. All sizes and anchors are fractions of the
frame, never pixels, so one script composes the same picture at 720p and 4K.

## Scenes

The scene is a capture fixture: it places units directly rather than opening a
normal skirmish, and its composition and spacing are film choices, not retail
rules. It deliberately does not share the benchmark's stager, whose composition
is pinned by the performance baselines it feeds
([BATTLE_BENCHMARK](BATTLE_BENCHMARK.md)).

* `battle` — two armies of `per_side` armed mobile units facing each other
  across a flat stretch the fixture searches for, `buildings` rear structures
  per side, and factory production when `factories` is set. Every mobile unit
  is ordered across the gap, so combat, pathfinding, COB and construction all
  run production code.
* `skirmish` — an ordinary fresh battle, framed on the viewing player's own
  start. Quieter footage: a base, not a front.

`pre_ticks` advances the scene before the first frame is written. `fog` keeps
the viewing player's fog; a film reveals the map by default, through the same
path as the `+nowisee` developer command, because a film shows the battle
rather than one side's knowledge of it.

## Camera and cuts

Camera keys are world pixels **relative to the scene anchor** — the fixture's
own centre — unless a key sets `"space": "world"`. `zoom` is the factor the
window calls 1× (DESIGN_GPU_RENDERER §16.2); it is clamped to the map's own
floor and to 2×. `ease` names the curve used to arrive at that key:
`inout` (the default), `linear`, `in` or `out`. Position interpolates linearly
and zoom geometrically, so equal ticks cover equal magnification ratios and a
push reads as steady rather than self-decelerating.

The camera is evaluated **once per tick**, never per presented frame. Enhanced
samples the camera origin at the authoritative step and blends the two samples
itself (§13.5), which is what makes a pan sub-pixel smooth at 60 or 120 FPS;
moving it per frame would fight that blend instead of feeding it.

Shots cut hard. At a cut the capture collapses the camera blend onto the
incoming shot (`SnapCameraBlend`), so the outgoing framing is not a move the
new shot slides out of.

A shot whose keys rise above 1× makes the capture synthesize the detail view's
2× art at load time (§14.1, §14.4); a film that stays at or below native skips
it, because no frame could show one of those pixels.

## Clean captures

The battle interface covers the composed surface's leftmost 128 columns and its
top and bottom 32 rows [03 §4.1]. A clean capture composes a surface that much
larger and writes only the world viewport out of it, so it gets interface-free
footage at exactly the requested size without a second viewport contract in the
camera. `film.ChromeInsetX/Y` and `camera.OriginX/Y` are held equal by a test.

The message column draws inside the world viewport, so it cannot be cropped
away; a film silences it at the ring instead, by configuring one authored line
[07 R-HUD-03 §14.3]. Set `"messages": true` to keep it.

Drop `"clean"` to capture the interface as the player sees it. Both belong in a
reel: the clean frames are the beauty shots, the full frames are the proof that
it is a game.

## Titles

Overlay text is drawn onto the composed frame after readback. It is our own
monoline stroke face, not a retail GAF font: promotional wording is Nanolathe's
own, and a stroke outline stays crisp at any capture resolution. The face is
upper case, and folds lower case onto it.

| `style` | Where |
| --- | --- |
| `title` | centred, large |
| `subtitle` | centred, below the title |
| `lower` | left, lower third; `"rule": true` draws a rule above it |
| `caption` | centred, near the bottom |

| `anim` | How it arrives |
| --- | --- |
| `fade` | opacity only |
| `rise` | opacity and a short slide up |
| `wipe` | a reveal column sweeping left to right |
| `type` | characters appear one at a time |

`at` and `ticks` are the cue's start and length within its shot; `in` and `out`
are the animation and fade lengths. `size` overrides the style's cap height as
a fraction of frame height, `x`/`y` its anchor, `align` its alignment and
`color` its face colour. Every cue carries a dark halo and a drop shadow, so a
title stays legible crossing grass, smoke and unit art in the same line.

An unknown style or animation is a script error rather than a default: a title
that quietly does not appear costs a whole re-render to notice.

To review the face itself — a stroke glyph can lose a segment and still measure
and draw ink — write the specimen sheet and look at it:

```
FILM_SPECIMEN=/tmp/face.png go test ./internal/film -run TestFontSpecimen -count=1
```

## Cost

The reference reel — `films/announce.json`, 770 ticks of a 320-unit battle,
1540 frames at 1920×1080, every Enhanced switch on — renders in about a minute
on an M-series laptop, faster than the 25.7 seconds of footage it produces. It
is a normal `go run`, not a benchmark: it takes no host lock and does not
belong beside a benchmark run.

## What it does not do yet

* **No audio.** The capture composes frames only. `internal/audiobackend` is a
  device seam, so a PCM mixdown driven at tick timestamps is possible, but it
  does not exist; score the cut in post, or record a live pass for reference.
* **No saved-game scenes.** `--load-save` reaches the battle through the
  windowed frontend, so a film cannot yet start from a save. This is the
  obvious next scene kind: it is what turns "a staged fixture" into "the moment
  from the game I just played".
* **No follow camera.** The camera takes scripted keys, not a unit to track.
* **One scene per script.** Every shot looks at the same battle, at a different
  time and from a different place.
