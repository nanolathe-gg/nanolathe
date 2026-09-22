package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"io"
	"math"
	"path/filepath"
	"strings"

	// The rule sets this build can select beyond the two reserved ones. The
	// import is what registers them, and it sits beside the flag that names
	// one so the two are read together (docs/DESIGN_GAMEPLAY_RULES.md §8).
	_ "github.com/nanolathe-gg/nanolathe/mods"
)

// Options is the command-line surface for the retail runtime and its host
// configuration. Developer probes and capture modes are separate tools.
type Options struct {
	Gameplay    gameplay.Mode
	GameplaySet bool
	// ContentProfile selects the mounted content set's directory table. The
	// flag takes a shipped profile's name or the path of a profile JSON file;
	// an omitted flag falls back to the saved preference and then to
	// detection. openContent resolves it and run replaces this field with the
	// resolved name, so everything downstream — the headless report among it —
	// reports the profile the mount actually used
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	ContentProfile     string
	Arrival            bool    // modern battle opening (GPU §36)
	ShotArrivalTime    float64 // seconds into a reproducible opening capture; negative disables
	UnitLimit          int     // zero uses the saved preference; explicit CLI values override it
	BattleBenchmark    string
	BenchmarkFactories bool
	BenchmarkFrames    int
	BenchmarkTPS       int
	BenchmarkPreTicks  int
	Root               string      // first content root; default save parent for programmatic callers
	Roots              []string    // ordered content roots; empty enables host discovery
	ListInstalls       bool        // print resolved installation roots without mounting content
	CheckInstall       bool        // validate startup content and exit without opening a window
	SaveDir            string      // exact save/load directory override; empty uses the install root
	Map                string      // map name without extension, e.g. "ashap plateau"
	Seed               int64       // battle RNG seed for both streams; <0 = derive pair from clock
	Headless           bool        // run the session without opening a window
	Ticks              int         // authoritative tick limit; zero uses the headless default
	Mission            string      // campaign path and mission selector, e.g. "camps/Arm Campaign.tdf:MISSION0"
	Difficulty         int         // campaign difficulty
	LoadSave           string      // explicit retail .SAV path to load in the windowed shell
	Report             string      // JSON headless summary path; empty writes to stdout
	Shot               string      // compose one frame to this PNG and exit, opening no window
	ShotTicks          int         // authoritative ticks to advance before the frame is captured
	Remaster           string      // remaster override: a loose directory or HPI mounted above every retail tier
	Zoom               camera.Zoom // presentation zoom factor from --zoom; zero is unset: all routes default to native [F-P1-008]
	ZoomText           string      // the literal --zoom argument, kept so it can be rejected per executor after --renderer is known (DESIGN_GPU_RENDERER §16.8)
	AutoRemaster       bool        // synthesize the detail view's 2x art at load time (DESIGN_GPU_RENDERER §14.4)
	ShotFocus          string      // "x,y" screen point kept fixed while scaling; default the screen centre
	ShotShift          bool        // hold Shift for strategic range captures
	ShotBuild          string      // preview a named product at the capture pointer
	ShotDeveloper      string      // capture mode 0..4; empty leaves developer views off
	ShotProbe          string      // restored state or builder probe in captures
	ShotContour        string      // contour spacing and optional offset in captures
	ShotSelect         bool        // run the Ctrl+A select-all before --shot captures, so the command page is open
	ShotSize           string      // "WxH" surface size for --shot; empty composes at the authored 640x480
	ShotDebris         string      // directory for the --shot-debris tick sequence; empty runs no debris capture
	ShotDebrisUnit     string      // unit blown up by --shot-debris
	ShotDebrisCount    int         // how many of them
	ShotDebrisFrames   int         // ticks captured from the kill onward
	ShotDebrisBurn     int         // features ignited near the site instead of killing units
	ShotDebrisLighting bool        // Enhanced Lighting switch for the capture
	ShotDebrisGlow     bool        // Enhanced glow layer for the capture
	Film               string      // film script path for the --film sequence capture; empty runs no film
	FilmOut            string      // --film destination: a directory of PNGs, or "-" for raw RGBA on stdout
	FilmFrames         int         // stop a --film capture after this many frames; 0 captures the whole script
	ShotModal          string      // battle modal to open before --shot captures: "options", "exit", "confirm", "settings", "help" or "briefing"
	ShotSpace          bool        // hold Space for --shot captures, so the bottom slide strip is fully raised
	RendererSet        bool        // explicit command-line override
	FPSSet             bool        // explicit command-line override
	Renderer           string      // start-up presentation executor: "classic" or "modern" (default)
	Fullscreen         bool        // host desktop fullscreen override
	FullscreenSet      bool        // distinguishes an omitted flag from --fullscreen=false
	Stats              bool        // opt-in terminal presentation statistics
	FPS                int         // cap on presented frames per second for the modern renderer; 0 = the display's refresh rate

	// ShotRenderer selects which executor --shot captures through:
	// "classic" (explicit software composer), "modern" (the GPU executor,
	// captured through a hidden one-frame Ebitengine loop), or "both" (classic
	// to --shot, modern to a sibling path, plus their diff). An omitted value
	// follows Renderer: modern only when --renderer=modern, otherwise classic.
	// [DESIGN_GPU_RENDERER.md §2.5]. ShotRendererMax is the largest differing-
	// pixel count "both" tolerates before the command exits non-zero. The
	// historical default remains effectively unbounded; comparison tools opt
	// into exact acceptance explicitly [DESIGN_GPU_RENDERER.md §6, C-G10].
	ShotRenderer         string // --shot executor: "classic", "modern" or "both"
	ShotRendererMax      int    // with "both", exit non-zero when the diff exceeds this
	ShotGPUProfileFrames int    // repeated modern capture frames to time after warm-up
	// ShotModel is an opt-in, isolated P3 model capture. It is deliberately
	// separate from battle --shot so the prototype never changes runtime cadence.
	ShotModel        string
	ShotModelPose    string
	ShotModelHeading uint
	ShotModelScale   float64
	// ShotModelBuildRemaining poses the isolated model as a nanoframe with this
	// construction fraction remaining (0 = complete).
	ShotModelBuildRemaining float64
	// ShotModelWorldHeight is the isolated model's world height. The preview
	// has no map, so its sea level is zero and a negative height submerges the
	// model by that much, which is what exercises the waterline pass.
	ShotModelWorldHeight int
	// ShotModelUnderwaterExempt sets the committed sonar-contact/exemption bit,
	// which turns the waterline pass from an erase into the blue tint.
	ShotModelUnderwaterExempt bool

	// Host-side profiling. None of these reach the session: a profiled run
	// draws the same numbers in the same order as an unprofiled one, so the
	// shipping path is what gets measured rather than a special build [I11].
	CPUProfile     string // pprof CPU profile of the --shot compose path
	MemProfile     string // pprof allocation profile of the --shot compose path
	ProfileSeconds int    // with --shot, drive the real viewer loop headlessly for this many seconds of battle time before capturing
}

// ErrHelp reports that usage was requested and printed.
var ErrHelp = errors.New("help requested")

func parseFlags(args []string, out io.Writer) (Options, error) {
	var opts Options
	var unitLimitSet bool
	set := flag.NewFlagSet("nanolathe", flag.ContinueOnError)
	set.BoolVar(&opts.Arrival, "arrival", true, "modern battle opening: commander arrival for new games, map reveal for saves")
	set.Float64Var(&opts.ShotArrivalTime, "shot-arrival-time", -1, "capture the arrival prototype at these presentation seconds (requires --shot --shot-ticks=0)")
	set.SetOutput(out)
	set.Func("root", "content root; repeat in load order (later roots win); omitted uses $NANOLATHE_TA_ROOT or installation discovery", func(root string) error {
		if root == "" {
			return fmt.Errorf("content root must not be empty")
		}
		opts.Roots = append(opts.Roots, root)
		if len(opts.Roots) == 1 {
			opts.Root = root
		}
		return nil
	})
	set.BoolVar(&opts.ListInstalls, "list-installs", false, "print selected or discovered content roots, one per line, without mounting")
	set.BoolVar(&opts.CheckInstall, "check-install", false, "validate selected content roots without opening a game window")
	set.StringVar(&opts.SaveDir, "save-dir", "", "exact save/load directory (omitted uses savegame beneath the installation)")
	set.StringVar(&opts.Map, "map", "", "map name without extension, e.g. \"ashap plateau\"")
	set.IntVar(&opts.UnitLimit, "unit-limit", 0, "per-player skirmish unit limit (20..3276); omitted uses saved unitLimit, otherwise 1000")
	set.Int64Var(&opts.Seed, "seed", -1, "battle RNG seed for both streams; negative derives a pair from the clock")
	set.BoolVar(&opts.Headless, "headless", false, "run a skirmish or mission without opening a window")
	set.IntVar(&opts.Ticks, "ticks", 0, "headless authoritative tick limit (0 = until result or 18000 ticks)")
	set.StringVar(&opts.Mission, "mission", "", "campaign selector, e.g. \"camps/Arm Campaign.tdf:MISSION0\"")
	set.IntVar(&opts.Difficulty, "difficulty", 1, "campaign difficulty")
	set.StringVar(&opts.LoadSave, "load-save", "", "load an existing retail .SAV in the windowed shell")
	set.StringVar(&opts.Report, "report", "", "write the headless JSON summary to this file (default stdout)")
	set.StringVar(&opts.Shot, "shot", "", "compose one battle frame to this PNG and exit, opening no window")
	set.StringVar(&opts.ShotDeveloper, "shot-developer", "", "capture developer terrain mode 0..4 with information enabled")
	set.StringVar(&opts.ShotProbe, "shot-probe", "", "capture a restored state, builder, or both probes for the first local unit")
	set.StringVar(&opts.ShotContour, "shot-contour", "", "capture contours: spacing and optional offset, in height units")
	set.IntVar(&opts.ShotTicks, "shot-ticks", 90, "authoritative ticks to advance before --shot captures the frame")
	set.StringVar(&opts.Remaster, "remaster", "", "remastered-art override: a loose directory or .hpi mounted above every retail archive")
	set.Func("zoom", "presentation view scale: any factor in 0.0625..2 with --renderer=modern, or 1 or 2 with classic; defaults to 1 for windows, captures and benchmarks", func(text string) error {
		// The free range is parsed here and the executor's own restriction is
		// applied after Parse, because --renderer may follow --zoom on the
		// command line (DESIGN_GPU_RENDERER §16.8).
		zoom, err := camera.ParseZoom(text)
		if err != nil {
			return err
		}
		opts.Zoom, opts.ZoomText = zoom, text
		return nil
	})
	set.BoolVar(&opts.AutoRemaster, "auto-remaster", true, "synthesize the detail view's 2x terrain and feature art at load time; off leaves every asset to nearest doubling")
	set.StringVar(&opts.ShotFocus, "shot-focus", "", "screen point \"x,y\" kept fixed by --zoom (default the screen centre)")
	set.BoolVar(&opts.ShotShift, "shot-shift", false, "hold Shift for queue overlays and enabled range guides in --shot")
	set.StringVar(&opts.ShotBuild, "shot-build", "", "preview this unit beside the first selection or at viewport centre in --shot (no construction order)")
	set.BoolVar(&opts.ShotSelect, "shot-select", false, "select the viewing player's units before --shot captures, so the side rail's command page is open")
	set.StringVar(&opts.BattleBenchmark, "battle-benchmark", "", "run the seeded live battle benchmark into a new output directory")
	set.BoolVar(&opts.BenchmarkFactories, "benchmark-factories", true, "queue factory production in the battle benchmark")
	set.IntVar(&opts.BenchmarkFrames, "benchmark-frames", 180, "measured battle benchmark frames after two seconds of renderer warmup")
	set.IntVar(&opts.BenchmarkPreTicks, "benchmark-pre-ticks", 300, "simulation ticks before opening the battle benchmark window (30 ticks per second)")
	set.IntVar(&opts.BenchmarkTPS, "benchmark-tps", 30, "battle benchmark presentation rate: 30, 60 or 120 FPS, with 30 simulation ticks per second")
	set.StringVar(&opts.ShotSize, "shot-size", "", "surface size \"WxH\" for --shot, one of the display modes (default 640x480)")
	set.StringVar(&opts.ShotDebris, "shot-debris", "", "prototype: kill a cluster of units and write one modern PNG per tick to this directory")
	set.StringVar(&opts.ShotDebrisUnit, "shot-debris-unit", "armstump", "unit the --shot-debris capture blows up")
	set.IntVar(&opts.ShotDebrisCount, "shot-debris-count", 6, "how many units --shot-debris blows up")
	set.IntVar(&opts.ShotDebrisFrames, "shot-debris-frames", 24, "ticks --shot-debris captures from the kill onward")
	set.IntVar(&opts.ShotDebrisBurn, "shot-debris-burn", 0, "ignite this many features near the site instead of killing units, for a standing-fire capture")
	set.BoolVar(&opts.ShotDebrisLighting, "shot-debris-lighting", true, "Enhanced battle lighting during a --shot-debris capture")
	set.BoolVar(&opts.ShotDebrisGlow, "shot-debris-glow", true, "Enhanced glow layer during a --shot-debris capture")
	set.StringVar(&opts.Film, "film", "", "compose the scripted sequence in this film script (docs/FILM_CAPTURE.md)")
	set.StringVar(&opts.FilmOut, "film-out", "", "where --film writes: a directory of PNG frames, or \"-\" for a raw RGBA stream on stdout")
	set.IntVar(&opts.FilmFrames, "film-frames", 0, "stop a --film capture after this many frames (0 captures the whole script)")
	set.StringVar(&opts.ShotModal, "shot-modal", "", "open a battle modal before --shot captures: \"options\" (Tab), \"exit\", \"confirm\", \"settings\", \"help\", or \"briefing\" (needs --mission)")
	set.BoolVar(&opts.ShotSpace, "shot-space", false, "hold Space for --shot captures, so the bottom slide strip (Game Time / Total Units / Game Speed) is fully raised")
	set.StringVar(&opts.CPUProfile, "cpuprofile", "", "write a pprof CPU profile of the --shot compose path to this file")
	set.StringVar(&opts.MemProfile, "memprofile", "", "write a pprof allocation profile of the --shot compose path to this file")
	set.IntVar(&opts.ProfileSeconds, "profile-seconds", 0, "with classic --shot, run the CPU viewer loop headlessly for this many seconds of battle time and report ms per frame")
	set.Func("content-profile", "content profile: "+strings.Join(contentprofiles.Names(), ", ")+", or the path of a profile JSON file; omitted uses the saved preference, then detection (docs/DESIGN_CONTENT_VFS.md §5)", func(text string) error {
		// Resolve here only to reject an unusable selector while the command
		// line is still the thing being read. The selector itself is what
		// travels: the mount boundary resolves it again so a path-selected
		// profile is read from the file the user named, and it is the mount
		// boundary that reports the name it settled on.
		if _, err := contentprofiles.Lookup(text); err != nil {
			return err
		}
		opts.ContentProfile = text
		return nil
	})
	set.Func("gameplay", "gameplay rule set: modern (default), strict-3.1, or a registered set's name (see mods/); omitted uses saved preference", func(text string) error {
		mode, err := gameplay.Parse(text)
		opts.Gameplay = mode
		return err
	})
	set.StringVar(&opts.Renderer, "renderer", settings.DefaultPresentation().Renderer, "start-up presentation renderer (omitted uses saved preference): \"classic\" (software) or \"modern\" (GPU); any other value is classic")
	set.BoolVar(&opts.Fullscreen, "fullscreen", false, "desktop fullscreen (Alt+Enter toggles); omitted uses saved preference")
	set.BoolVar(&opts.Stats, "stats", false, "print periodic presentation statistics to the terminal")
	set.IntVar(&opts.FPS, "fps", settings.DefaultPresentation().FPS, "cap presented frames per second for modern (omitted uses saved preference), rounded down to a multiple of the display's refresh (0 = the display's refresh rate)")
	set.StringVar(&opts.ShotRenderer, "shot-renderer", "", "which executor --shot captures through: \"classic\", \"modern\", or \"both\"; omitted follows --renderer")
	set.IntVar(&opts.ShotRendererMax, "shot-renderer-max", math.MaxInt32, "with --shot-renderer both, exit non-zero when the diff exceeds this many pixels (default effectively unbounded)")
	set.IntVar(&opts.ShotGPUProfileFrames, "shot-gpu-profile-frames", 0, "with --shot-renderer modern or both, time this many frozen-scene GPU frames after warm-up")
	set.StringVar(&opts.ShotModel, "shot-model", "", "isolated model name for an opt-in GPU preview capture (requires --renderer=modern and --shot)")
	set.StringVar(&opts.ShotModelPose, "shot-model-pose", "", "isolated model pose: \"open\" synthetic ARMSOLAR, \"activated\" production COB pose")
	set.UintVar(&opts.ShotModelHeading, "shot-model-heading", 0, "isolated model preview heading (0..65535)")
	set.Float64Var(&opts.ShotModelScale, "shot-model-scale", 2, "isolated model preview scale: 1 or 2")

	set.Float64Var(&opts.ShotModelBuildRemaining, "shot-model-build-remaining", 0, "isolated model preview construction fraction remaining, 0..1; above 0 poses a nanoframe with its reveal and outline")
	set.IntVar(&opts.ShotModelWorldHeight, "shot-model-world-height", 0, "isolated model preview world height; the preview's sea level is 0, so a negative value submerges the model and runs the waterline pass")
	set.BoolVar(&opts.ShotModelUnderwaterExempt, "shot-model-underwater-exempt", false, "isolated model preview sonar-contact/underwater-exemption bit: a submerged model is blue-tinted instead of erased below the waterline")
	set.Usage = func() {
		fmt.Fprintf(out, "nanolathe — a reimplementation of the Total Annihilation engine\n\n")
		fmt.Fprintf(out, "usage: nanolathe [flags]\n\nflags:\n")
		set.PrintDefaults()
	}
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, ErrHelp
		}
		return opts, err
	}
	set.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "unit-limit":
			unitLimitSet = true
		case "fullscreen":
			opts.FullscreenSet = true
		case "gameplay":
			opts.GameplaySet = true
		case "renderer":
			opts.RendererSet = true
		case "fps":
			opts.FPSSet = true
		}
	})
	if unitLimitSet && (opts.UnitLimit < settings.MinUnitLimit || opts.UnitLimit > settings.MaxUnitLimit) {
		err := fmt.Errorf("nanolathe: invalid unit limit: logical path <command line>, providers searched [unit-limit], expected %d..%d", settings.MinUnitLimit, settings.MaxUnitLimit)
		fmt.Fprintln(out, err) // mainOptions expects parseFlags to print validation failures.
		return opts, err
	}
	// The classic executor has no free zoom: its factor is always its record
	// step, so it takes only the two views (DESIGN_GPU_RENDERER §16.8). The
	// modern one takes any factor the flag's own function accepted; the
	// map-derived floor of §16.7 is applied at battle entry, where the map is
	// known. An unset flag stays zero for the routes to resolve (§14.6).
	if opts.ZoomText != "" && !modernRenderer(opts) {
		if _, err := camera.ParseViewScale(opts.ZoomText); err != nil {
			// The flag package prints its own function's rejections; this one
			// runs after Parse, so it has to say why itself or the run exits
			// with a status and no reason.
			fmt.Fprintf(out, "invalid value %q for flag -zoom: %v\n", opts.ZoomText, err)
			return opts, err
		}
	}
	if opts.BattleBenchmark != "" {
		// --zoom is accepted here: the benchmark scene is the same battle at
		// twice the pixels, and the detail view's cost is exactly what the
		// benchmark exists to measure (DESIGN_GPU_RENDERER §14.6). The scale
		// is recorded in the scene metadata, so two runs are only compared
		// when they were captured at the same one.
		if opts.Shot != "" || opts.ShotModel != "" || opts.Headless || opts.LoadSave != "" || opts.Mission != "" || opts.CPUProfile != "" || opts.MemProfile != "" || opts.ProfileSeconds != 0 || opts.ShotRenderer != "" || opts.ShotGPUProfileFrames != 0 || (opts.ShotSize != "" && opts.ShotSize != "1920x1080") {
			return opts, fmt.Errorf("nanolathe: battle benchmark requires a standalone 1920x1080 battle")
		}
		if opts.BenchmarkPreTicks < 0 || opts.BenchmarkPreTicks > 18000 {
			return opts, fmt.Errorf("nanolathe: benchmark pre-ticks must be 0..18000")
		}
		if opts.BenchmarkFrames < 1 || opts.BenchmarkFrames > 100000 {
			return opts, fmt.Errorf("nanolathe: benchmark frames must be 1..100000")
		}
		if opts.BenchmarkTPS != 30 && opts.BenchmarkTPS != 60 && opts.BenchmarkTPS != 120 {
			return opts, fmt.Errorf("nanolathe: benchmark tps must be 30, 60 or 120")
		}
		if opts.Map == "" {
			opts.Map = "great divide"
		}
		if opts.Seed < 0 {
			opts.Seed = 7
		}
		opts.Shot = filepath.Join(opts.BattleBenchmark, "battle.png")
		opts.ShotSize = "1920x1080"
	}
	if opts.Shot != "" || opts.ShotModel != "" {
		if err := validateShotOptions(opts); err != nil {
			return opts, err
		}
	}
	return opts, nil
}
