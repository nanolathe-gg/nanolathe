package film

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

// SimulationTPS is the authoritative cadence every film timeline is measured
// against [01 §4.1]. A capture rate is a whole multiple of it, so one tick
// covers a whole number of presented frames and every frame's blend fraction
// is an exact k/n rather than a wall-clock sample (DESIGN_GPU_RENDERER §13.5).
const SimulationTPS = 30

// Script is a complete capture: an initial scene, shots cut back to back,
// and the overlay each shot carries.
type Script struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	FPS       int     `json:"fps"`
	Letterbox float64 `json:"letterbox"`
	Clean     bool    `json:"clean"`
	Messages  bool    `json:"messages"` // keep the battle message column; a film silences it by default
	Scene     Scene   `json:"scene"`
	Shots     []Shot  `json:"shots"`
}

// ChromeInsetX and ChromeInsetY are the battle chrome's framebuffer insets:
// the interface covers the leftmost ChromeInsetX columns and the top and
// bottom ChromeInsetY rows of the composed surface [03 §4.1]. A clean capture
// composes a surface that much larger and writes only the world viewport out
// of it, so it gets interface-free footage at exactly the requested size
// without a second viewport contract in the camera.
const (
	ChromeInsetX = 128
	ChromeInsetY = 32
)

// Surface is the size the client and the device compose at. It is the frame
// size unless the script is clean, which grows it by the chrome.
func (s *Script) Surface() (w, h int) {
	if !s.Clean {
		return s.Width, s.Height
	}
	return s.Width + ChromeInsetX, s.Height + 2*ChromeInsetY
}

// CropOrigin is the composed surface pixel the written frame starts at.
func (s *Script) CropOrigin() (x, y int) {
	if !s.Clean {
		return 0, 0
	}
	return ChromeInsetX, ChromeInsetY
}

// Scene names the battle the shots look at. It is a capture fixture, not a
// retail opening: the units are placed directly (docs/FILM_CAPTURE.md
// "Scenes").
type Scene struct {
	Kind      string  `json:"kind"`             // "battle" or "skirmish"
	Anchor    []int32 `json:"anchor,omitempty"` // explicit fixture centre [x,z] in world pixels
	Roster    string  `json:"roster"`           // "mixed" (default), "armor", or "air"; "battle" only
	Map       string  `json:"map"`
	Seed      uint32  `json:"seed"`
	PreTicks  int     `json:"pre_ticks"`
	PerSide   int     `json:"per_side"`  // armed mobile units per side, "battle" only
	Buildings int     `json:"buildings"` // rear buildings per side, "battle" only
	Factories bool    `json:"factories"` // queue factory production, "battle" only
	Fog       bool    `json:"fog"`       // keep the viewing player's fog; a film reveals by default
}

// Shot is one continuous camera take. Shots cut hard: the camera jumps to the
// next shot's first key on its first tick.
type Shot struct {
	Name   string      `json:"name"`
	Scene  *Scene      `json:"scene,omitempty"` // a fresh scene at this cut; nil continues the current session
	Ticks  int         `json:"ticks"`
	Camera []CameraKey `json:"camera"`
	Text   []Cue       `json:"text"`
}

// CameraKey is one keyframe of the shot's camera. X and Z are world pixels
// relative to the scene anchor unless Space is "world"; Zoom is the factor the
// window calls 1x (DESIGN_GPU_RENDERER §16.2). Ease names the curve used to
// arrive AT this key.
type CameraKey struct {
	Tick  float64 `json:"tick"`
	X     float64 `json:"x"`
	Z     float64 `json:"z"`
	Zoom  float64 `json:"zoom"`
	Ease  string  `json:"ease"` // linear | in | out | inout
	Space string  `json:"space"`
}

var cameraEases = map[string]func(float64) float64{
	"":       easeInOut,
	"inout":  easeInOut,
	"linear": func(t float64) float64 { return t },
	"in":     easeIn,
	"out":    easeOut,
}

// Parse reads a film script and applies its defaults.
func Parse(data []byte) (*Script, error) {
	var s Script
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("nanolathe: film: read script: %w", err)
	}
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Script) applyDefaults() {
	if s.Width <= 0 {
		s.Width = 1920
	}
	if s.Height <= 0 {
		s.Height = 1080
	}
	if s.FPS <= 0 {
		s.FPS = 60
	}
	s.Scene.applyDefaults()
	for i := range s.Shots {
		if s.Shots[i].Scene != nil {
			s.Shots[i].Scene.applyDefaults()
		}
		if s.Shots[i].Name == "" {
			s.Shots[i].Name = fmt.Sprintf("shot%02d", i+1)
		}
		slices.SortStableFunc(s.Shots[i].Camera, func(a, b CameraKey) int {
			switch {
			case a.Tick < b.Tick:
				return -1
			case a.Tick > b.Tick:
				return 1
			default:
				return 0
			}
		})
	}
}

func (s *Scene) applyDefaults() {
	if s.Kind == "" {
		s.Kind = "battle"
	}
	if s.PerSide == 0 {
		s.PerSide = 120
	}
	if s.Buildings == 0 && s.Roster != "air" {
		s.Buildings = 12
	}
}

func (s *Scene) problems() []string {
	var problems []string
	switch s.Kind {
	case "battle", "skirmish":
	default:
		problems = append(problems, fmt.Sprintf("unknown scene kind %q, expected \"battle\" or \"skirmish\"", s.Kind))
	}
	switch s.Roster {
	case "", "mixed", "armor", "air":
	default:
		problems = append(problems, fmt.Sprintf("unknown scene roster %q, expected \"mixed\", \"armor\" or \"air\"", s.Roster))
	}
	if s.Anchor != nil && len(s.Anchor) != 2 {
		problems = append(problems, "scene anchor must contain exactly two world coordinates [x,z]")
	}
	if s.Roster == "air" && (s.Buildings != 0 || s.Factories) {
		problems = append(problems, "air scene cannot stage buildings or factories")
	}
	if s.PreTicks < 0 || s.PerSide < 0 || s.Buildings < 0 {
		problems = append(problems, "scene pre_ticks, per_side and buildings must not be negative")
	}
	return problems
}

// Validate reports every reason the script cannot be captured, rather than the
// first: a re-render is minutes, so a script is worth checking whole.
func (s *Script) Validate() error {
	var problems []string
	if s.Width%2 != 0 || s.Height%2 != 0 {
		problems = append(problems, fmt.Sprintf("frame %dx%d must have even sides for the usual 4:2:0 encoders", s.Width, s.Height))
	}
	if s.FPS%SimulationTPS != 0 {
		problems = append(problems, fmt.Sprintf("fps %d must be a multiple of the %d Hz authoritative tick", s.FPS, SimulationTPS))
	}
	problems = append(problems, s.Scene.problems()...)
	if len(s.Shots) == 0 {
		problems = append(problems, "script has no shots")
	}
	for _, shot := range s.Shots {
		if shot.Scene != nil {
			for _, problem := range shot.Scene.problems() {
				problems = append(problems, fmt.Sprintf("shot %q: %s", shot.Name, problem))
			}
		}
		if shot.Ticks <= 0 {
			problems = append(problems, fmt.Sprintf("shot %q has no duration", shot.Name))
		}
		for _, key := range shot.Camera {
			if _, ok := cameraEases[key.Ease]; !ok {
				problems = append(problems, fmt.Sprintf("shot %q: unknown ease %q", shot.Name, key.Ease))
			}
			if key.Space != "" && key.Space != "scene" && key.Space != "world" {
				problems = append(problems, fmt.Sprintf("shot %q: unknown camera space %q", shot.Name, key.Space))
			}
			if key.Tick < 0 || key.Tick > float64(shot.Ticks) {
				problems = append(problems, fmt.Sprintf("shot %q: camera key at tick %.1f is outside the shot's %d ticks", shot.Name, key.Tick, shot.Ticks))
			}
		}
		for _, cue := range shot.Text {
			if err := cue.Validate(); err != nil {
				problems = append(problems, fmt.Sprintf("shot %q: %v", shot.Name, err))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return &ScriptError{Problems: problems}
}

// ScriptError carries every validation problem at once.
type ScriptError struct{ Problems []string }

func (e *ScriptError) Error() string {
	out := "nanolathe: film: script is not capturable:"
	for _, problem := range e.Problems {
		out += "\n  " + problem
	}
	return out
}

// FramesPerTick is how many presented frames one authoritative tick covers.
func (s *Script) FramesPerTick() int { return s.FPS / SimulationTPS }

// TotalTicks is the simulation length of the whole film, excluding warmup.
func (s *Script) TotalTicks() int {
	total := 0
	for _, shot := range s.Shots {
		total += shot.Ticks
	}
	return total
}

// TotalFrames is the number of frames the capture writes.
func (s *Script) TotalFrames() int { return s.TotalTicks() * s.FramesPerTick() }

// Duration is the film's running time in seconds.
func (s *Script) Duration() float64 { return float64(s.TotalTicks()) / SimulationTPS }

// MaxZoom is the largest magnification any shot reaches. The capture loads the
// detail view's 2x art when a film goes above native, and skips synthesizing
// it when no shot can show one of those pixels (DESIGN_GPU_RENDERER §14.1).
func (s *Script) MaxZoom() float64 {
	out := 1.0
	for _, shot := range s.Shots {
		for _, key := range shot.Camera {
			out = math.Max(out, keyZoom(key))
		}
	}
	return out
}

// Cursor locates one presented frame in the timeline.
type Cursor struct {
	Shot     int     // index into Shots
	ShotTick int     // tick within that shot, from zero
	Phase    int     // presented frame within the tick, from zero
	Fraction float64 // Phase / FramesPerTick, the blend fraction for this frame
	Cut      bool    // this frame is the first of its shot
}

// At maps a presented frame index onto the timeline.
func (s *Script) At(frame int) (Cursor, bool) {
	per := s.FramesPerTick()
	if frame < 0 || frame >= s.TotalFrames() || per <= 0 {
		return Cursor{}, false
	}
	tick, phase := frame/per, frame%per
	for i, shot := range s.Shots {
		if tick < shot.Ticks {
			return Cursor{
				Shot:     i,
				ShotTick: tick,
				Phase:    phase,
				Fraction: float64(phase) / float64(per),
				Cut:      tick == 0 && phase == 0,
			}, true
		}
		tick -= shot.Ticks
	}
	return Cursor{}, false
}

// SceneAtCut selects a new scene only on the first frame of a shot. The first
// shot can replace the top-level scene, avoiding loading an unused battle.
func (s *Script) SceneAtCut(cursor Cursor) *Scene {
	if !cursor.Cut || cursor.Shot < 0 || cursor.Shot >= len(s.Shots) {
		return nil
	}
	if scene := s.Shots[cursor.Shot].Scene; scene != nil {
		return scene
	}
	if cursor.Shot == 0 {
		return &s.Scene
	}
	return nil
}

// CameraAt evaluates the shot's camera at a tick. It returns the view centre
// in the key's own space and the zoom factor; a shot with no keys holds the
// scene anchor at native zoom.
//
// The camera is evaluated once per tick, never per presented frame, because
// Enhanced samples the camera origin at the authoritative step and blends the
// two samples itself (DESIGN_GPU_RENDERER §13.5). Moving it per frame would
// fight that blend instead of feeding it.
func (sh *Shot) CameraAt(tick float64) (x, z, zoom float64, world bool) {
	if len(sh.Camera) == 0 {
		return 0, 0, 1, false
	}
	first := sh.Camera[0]
	if tick <= first.Tick || len(sh.Camera) == 1 {
		return first.X, first.Z, keyZoom(first), first.Space == "world"
	}
	last := sh.Camera[len(sh.Camera)-1]
	if tick >= last.Tick {
		return last.X, last.Z, keyZoom(last), last.Space == "world"
	}
	for i := 1; i < len(sh.Camera); i++ {
		a, b := sh.Camera[i-1], sh.Camera[i]
		if tick > b.Tick {
			continue
		}
		span := b.Tick - a.Tick
		t := 0.0
		if span > 0 {
			t = (tick - a.Tick) / span
		}
		t = cameraEases[b.Ease](t)
		// Zoom interpolates geometrically: equal ticks then cover equal
		// magnification ratios, which is what reads as a steady push rather
		// than one that decelerates on its own.
		az, bz := keyZoom(a), keyZoom(b)
		return a.X + (b.X-a.X)*t, a.Z + (b.Z-a.Z)*t, az * math.Pow(bz/az, t), b.Space == "world"
	}
	return last.X, last.Z, keyZoom(last), last.Space == "world"
}

func keyZoom(k CameraKey) float64 {
	if k.Zoom <= 0 {
		return 1
	}
	return k.Zoom
}
