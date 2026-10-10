package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"io"
	"maps"
	"math"
	"path/filepath"
	"strings"

	// The rule sets this build can select beyond the two reserved ones. The
	// import is what registers them, and it sits beside the flag that names
	// one so the two are read together (docs/DESIGN_GAMEPLAY_RULES.md §8).
	_ "github.com/nanolathe-gg/nanolathe/mods"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// Options is the command-line surface for the retail runtime and its host
// configuration. Developer probes and capture modes are separate tools.
type Options struct {
	Metal                                  bool
	MetalReport, MetalSize                 string
	MetalModelCapture                      string
	MetalFrames, MetalQuads, MetalTextures int

	// Extra delay for both seats in the local responsiveness experiment.
	LocalMPCommandDelayMS int

	// Hosted two-human play test; empty room creates one on the relay.
	RelayAddress          string
	RelayRoom             string
	RelayCA               string
	RelayInsecureLoopback bool

	// excludedMapRoot belongs only to a prepared map-removal mount; never saved.
	excludedMapRoot   string
	Gameplay          gameplay.Mode
	GameplaySet       bool
	GameplayOverrides []community.Overrides
	// ModConfig is the path of a nanolathe-mod.json whose content section
	// and rules apply to the mount: the config for a manual --root stack or a
	// benchmark run, or a config tried against an installed --mod in place of
	// its own. Empty means the selected mod's own config, else the saved
	// preference (settings contentProfile), else plain content
	// (docs/DESIGN_MODS_MUTATORS.md §4.3). After the mount, run sets it to the
	// file the mount actually read, "" when none, so a remount keeps it.
	ModConfig string
	// Mod selects an installed mod ("id", "id@version" or "none"); ModSet
	// records that the flag was given, so it wins over the saved choice
	// (docs/DESIGN_MODS_MUTATORS.md §4.3).
	Mod    string
	ModSet bool
	// MutatorArgs are the raw --mutator name=factor pairs; Mutators is the
	// resolved set every battle request carries. The screen updates Mutators
	// when the player applies a new set (docs/DESIGN_MODS_MUTATORS.md §6).
	MutatorArgs map[string]string
	Mutators    content.Mutators
	// Restrictions is the unit-restriction set every skirmish and Survival
	// battle request carries; a campaign mission takes none
	// (docs/DESIGN_MODS_MUTATORS.md §15). RestrictionsSet records that
	// --restrict was given, a set or none, so the flag replaces the saved
	// set for the run (§15.5). Names are checked once the content is
	// compiled, at battle entry.
	Restrictions    content.Restrictions
	RestrictionsSet bool
	// AIArgs are the --ai key=value parameters, already checked against the
	// Modern AI brain's keys; AIOverrides is the resolved configuration
	// every battle request carries: the flag's parameters for every
	// computer player, or else the saved modernAI block in the window
	// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player").
	AIArgs      map[string]string
	AIOverrides session.AIOverrides
	// ComputerAI are the --ai-player choices: a computer row's controller,
	// Classic or Modern, for a battle the command line composes (--map,
	// --survival, --headless, the captures). The lobby's own rows carry
	// their choice for a battle started from the screens
	// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
	// "Per-player selection").
	ComputerAI []session.ComputerAI
	// InstallMod installs a local zip or directory into the mod library and
	// exits (a command-line stand-in for drag-and-drop, §4.5).
	InstallMod string
	// modBaseRoots marks Roots as the base install only (an internal remount
	// with a mod selected), so several roots are not a manual stack.
	modBaseRoots       bool
	ArrivalSet         bool    // explicit command-line override
	Arrival            bool    // modern battle opening (GPU §36)
	ShotArrivalTime    float64 // seconds into a reproducible opening capture; negative disables
	UnitLimit          int     // zero uses the saved preference; explicit CLI values override it
	BattleBenchmark    string
	BenchmarkCapture   string
	BenchmarkFactories bool
	BenchmarkFrames    int
	BenchmarkTPS       int
	BenchmarkPreTicks  int
	BenchmarkScene     string
	BenchmarkArmySize  int
	BenchmarkRenderer  string // optional benchmark-only executor; metal uses the modern client
	// BenchmarkScale scales the coastal scene's mobile rosters; zero or one is
	// the fixture as documented. BenchmarkCaptureCopies stages a capture more
	// than once.
	BenchmarkScale         float64
	BenchmarkCaptureCopies int
	// LiveTrace names a new directory for the live window's frame trace;
	// LiveSeconds ends that run, LiveScene stages a scene into its battle,
	// LiveSpeed sets its game speed, and LiveProfileFrom/LiveExecTrace place a
	// CPU profile and a Go execution trace (docs/BATTLE_BENCHMARK.md "Live
	// window trace").
	LiveTrace          string
	LiveSeconds        float64
	LiveScene          string
	LiveSpeed          int
	LiveProfileFrom    float64
	LiveExecTrace      float64
	LiveFlight         bool
	LiveUnpaced        bool
	Root               string   // first content root; default save parent for programmatic callers
	Roots              []string // ordered content roots; empty enables host discovery
	ListInstalls       bool     // print resolved installation roots without mounting content
	CheckInstall       bool     // validate startup content and exit without opening a window
	SaveDir            string   // exact save/load directory override; empty uses the install root
	LocalMPListen      string   // developer loopback relay, seat 0
	LocalMPJoin        string   // developer loopback relay, seat 1
	Map                string   // map name without extension, e.g. "ashap plateau"
	Survival           bool     // a Survival battle on Map (docs/DESIGN_SURVIVAL.md)
	SurvivalBuddies    int      // allied computer players, 0..2
	SurvivalPace       string   // normal, relaxed or relentless
	SurvivalNoAir      bool
	SurvivalNoNaval    bool
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
	ShotUnitViewer     string      // unit ID for a full-screen viewer capture
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
	NLShot             string      // directory for --nl-shot, the Nanolathe screen's page captures; empty runs none
	NLShotSize         string      // "WxH" canvas for --nl-shot
	NLShotOnly         string      // comma-separated card keys --nl-shot limits itself to; empty captures every card
	ShotModal          string      // battle modal to open before --shot captures: "options", "exit", "confirm", "settings", "help" or "briefing"
	ShotMegamap        bool        // show the megamap overview in --shot (DESIGN_INTERFACE_HUD_INPUT §3.15)
	ShotSpace          bool        // hold Space for --shot captures, so the bottom slide strip is fully raised
	RendererSet        bool        // explicit command-line override
	FPSSet             bool        // explicit command-line override
	UIScale            int         // -1 keeps the saved preference; 0 Auto, 1 or 2 fixed
	Renderer           string      // start-up presentation executor: "classic" or "modern" (default)
	Fullscreen         bool        // host desktop fullscreen override
	FullscreenSet      bool        // distinguishes an omitted flag from --fullscreen=false
	Stats              bool        // opt-in terminal presentation statistics
	FPS                int         // cap on presented frames per second for the modern renderer; 0 = the display's refresh rate

	WalkPreview         string // unit ID for the standalone walk smoothing preview
	WalkPreviewOriginal bool   // start the preview with its original pose sampling
	WalkPreviewFrames   int    // numbered 120 fps captures when greater than one

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
	var unitLimitSet, liveSecondsSet, benchmarkPreTicksSet, localMPSet bool
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
	set.BoolVar(&opts.Metal, "metal", false, "experimental, macOS: play the --map or --mission battle in the native Metal renderer (docs/DESIGN_METAL_RENDERER.md)")
	set.StringVar(&opts.MetalReport, "metal-report", "", "with --metal: write timing rows, a final capture and a report to this new directory on exit")
	set.StringVar(&opts.MetalModelCapture, "metal-model-capture", "", "with --metal and --metal-report: capture armsolar, armcom, armcom-ground/tree/air, armcom-cloaked/underlay or armflash-wreck[-sink] offscreen")
	set.StringVar(&opts.MetalSize, "metal-size", "2880x1800", "with --metal: drawable pixels, with a 2x logical HUD")
	set.IntVar(&opts.MetalFrames, "metal-frames", 0, "with --metal: frame limit; zero plays until the window closes")
	set.IntVar(&opts.MetalQuads, "metal-quads", 1, "Metal battle benchmark stress: mesh subdivisions, 1, 2 or 5")
	set.IntVar(&opts.MetalTextures, "metal-textures", 1, "Metal battle benchmark stress: model texture scale, 1 or 2")
	set.StringVar(&opts.SaveDir, "save-dir", "", "exact save/load directory (omitted uses savegame beneath the installation)")
	set.StringVar(&opts.Map, "map", "", "map name without extension, e.g. \"ashap plateau\"")
	set.StringVar(&opts.LocalMPListen, "local-mp-listen", "", "host a two-human Modern play test at a numeric loopback address (requires --map)")
	set.StringVar(&opts.LocalMPJoin, "local-mp-join", "", "join seat 2 of a local play test (requires --map)")
	set.IntVar(&opts.LocalMPCommandDelayMS, "local-mp-command-delay-ms", 0, "additional order delay for both local seats, 0..1000 ms (listener only; not simulated ping)")
	set.StringVar(&opts.RelayAddress, "relay-address", "", "hosted relay host:port or wss://host/relay for a two-human Modern play test (requires --map)")
	set.StringVar(&opts.RelayRoom, "relay-room", "", "join this hosted room code; omit to create a room")
	set.StringVar(&opts.RelayCA, "relay-ca", "", "additional trusted PEM certificate for a private TLS relay")
	set.BoolVar(&opts.RelayInsecureLoopback, "relay-insecure-loopback", false, "plaintext hosted transport for numeric loopback tests only")
	set.BoolVar(&opts.Survival, "survival", false, "start a Survival battle on --map (docs/DESIGN_SURVIVAL.md)")
	set.IntVar(&opts.SurvivalBuddies, "survival-buddies", 0, "allied computer players in a Survival battle, 0..2")
	set.StringVar(&opts.SurvivalPace, "survival-pace", "normal", "Survival wave pace: normal, relaxed or relentless")
	set.BoolVar(&opts.SurvivalNoAir, "survival-no-air", false, "Survival: no air waves")
	set.BoolVar(&opts.SurvivalNoNaval, "survival-no-naval", false, "Survival: no naval waves")
	set.IntVar(&opts.UnitLimit, "unit-limit", 0, "per-player skirmish unit setting (20..3276); beats a gameplay feature table's limit; omitted uses the saved unitLimit, else the table's limit, else 1000; never saved")
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
	set.StringVar(&opts.BenchmarkScene, "benchmark-scene", "coastal", "battle benchmark fixture: coastal or field (Town & Country's three armies)")
	set.IntVar(&opts.BenchmarkArmySize, "benchmark-army-size", 500, "field benchmark units per computer army, excluding its commander (250..1000)")
	set.StringVar(&opts.BenchmarkRenderer, "benchmark-renderer", "", "battle benchmark executor: classic, modern or metal; omitted follows --renderer")
	set.StringVar(&opts.BenchmarkCapture, "benchmark-capture", "", "stage a battle benchmark from a Ctrl+Shift+F11 diagnostic directory")
	set.BoolVar(&opts.BenchmarkFactories, "benchmark-factories", true, "queue factory production in the battle benchmark")
	set.IntVar(&opts.BenchmarkFrames, "benchmark-frames", 180, "measured battle benchmark frames after two seconds of renderer warmup")
	set.IntVar(&opts.BenchmarkPreTicks, "benchmark-pre-ticks", 300, "simulation ticks before opening the battle benchmark window (30 ticks per second)")
	set.IntVar(&opts.BenchmarkTPS, "benchmark-tps", 30, "battle benchmark presentation rate: 30, 60 or 120 FPS, with 30 simulation ticks per second")
	set.Float64Var(&opts.BenchmarkScale, "benchmark-scale", 1, "scale the coastal benchmark scene's mobile rosters (1 = the documented fixture)")
	set.IntVar(&opts.BenchmarkCaptureCopies, "benchmark-capture-copies", 1, "stage a --benchmark-capture bundle this many times, each copy offset by five cells (1..4)")
	set.StringVar(&opts.LiveTrace, "live-trace", "", "time the ordinary window loop — play from the menus, or a --map battle — into this NEW directory (docs/BATTLE_BENCHMARK.md)")
	set.Float64Var(&opts.LiveSeconds, "live-seconds", 30, "end a --live-trace run after this many seconds of battle presentation (0 = until closed; menu play defaults to 0)")
	set.StringVar(&opts.LiveScene, "live-scene", "", "stage a --live-trace battle: coastal[:scale], field[:army] or capture:<dir>[:copies]")
	set.IntVar(&opts.LiveSpeed, "live-speed", 0, "run a --live-trace --map battle at this game speed, 1..20 where 10 is normal (0 = unchanged)")
	set.Float64Var(&opts.LiveProfileFrom, "live-profile-from", -1, "start a CPU profile this many seconds into a --live-trace run (negative = none)")
	set.Float64Var(&opts.LiveExecTrace, "live-exec-trace", 0, "with --live-profile-from, also record this many seconds of Go execution trace")
	set.BoolVar(&opts.LiveFlight, "live-flight", false, "keep a Go execution trace flight recorder during --live-trace and write flight-<frame>.trace around frame spikes")
	set.BoolVar(&opts.LiveUnpaced, "live-unpaced", false, "with --live-trace, present on every display refresh and scan a fullscreen window out directly, as Ebitengine does by itself (macOS; for comparison)")
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
	set.StringVar(&opts.NLShot, "nl-shot", "", "render every card of the Nanolathe screen to PNGs in this directory, with no visible window")
	set.StringVar(&opts.WalkPreview, "walk-preview", "", "open the standalone walk smoothing preview for this unit ID; I compares Original/Smooth")
	set.BoolVar(&opts.WalkPreviewOriginal, "walk-preview-original", false, "start the walk preview with original pose sampling")
	set.IntVar(&opts.WalkPreviewFrames, "walk-preview-frames", 1, "with --walk-preview and --shot, capture this many 120 fps frames (numbered PNGs when greater than one)")
	set.StringVar(&opts.ShotUnitViewer, "shot-unit-viewer", "", "with --shot, capture the unit viewer for this unit ID (or @tools for the Tools menu)")
	set.StringVar(&opts.NLShotSize, "nl-shot-size", "1920x1080", "canvas size for --nl-shot, as WxH")
	set.StringVar(&opts.NLShotOnly, "nl-shot-only", "", "comma-separated card keys (or page:card) --nl-shot captures; empty captures every card")
	set.StringVar(&opts.ShotModal, "shot-modal", "", "open a battle modal before --shot captures: \"options\" (Tab), \"exit\", \"confirm\", \"settings\", \"help\", or \"briefing\" (needs --mission)")
	set.BoolVar(&opts.ShotMegamap, "shot-megamap", false, "select the Megamap overview and show it before --shot captures")
	set.BoolVar(&opts.ShotSpace, "shot-space", false, "hold Space for --shot captures, so the bottom slide strip (Game Time / Total Units / Game Speed) is fully raised")
	set.StringVar(&opts.CPUProfile, "cpuprofile", "", "write a pprof CPU profile of the --shot compose path to this file")
	set.StringVar(&opts.MemProfile, "memprofile", "", "write a pprof allocation profile of the --shot compose path to this file")
	set.IntVar(&opts.ProfileSeconds, "profile-seconds", 0, "with classic --shot, run the CPU viewer loop headlessly for this many seconds of battle time and report ms per frame")
	set.Func("mod-config", "path of a nanolathe-mod.json whose content layout, limits and rules apply to a manual --root stack or benchmark run, or replace an installed --mod's own; omitted uses the mod's own config, then the saved preference, then plain content (docs/DESIGN_MODS_MUTATORS.md §4.3)", func(text string) error {
		// Read here only to reject an unusable file while the command line is
		// still the thing being read; the mount boundary reads it again.
		if _, err := modlibrary.ReadConfigFile(text); err != nil {
			return err
		}
		opts.ModConfig = text
		return nil
	})
	set.Func("mod", "installed mod to mount after the base install: id, id@version or none; omitted uses the saved choice in the window, and none for --shot, --film, --battle-benchmark and --headless (docs/DESIGN_MODS_MUTATORS.md §4.3)", func(text string) error {
		if _, _, err := modlibrary.ParseSelector(text); err != nil {
			return err
		}
		opts.Mod = text
		return nil
	})
	set.Func("mutator", "battle mutator name=factor, e.g. buildSpeed=2 or buildCost=0.5 (repeatable; omitted uses the saved set in the window, and none for --shot, --film, --battle-benchmark and --headless)", func(text string) error {
		name, value, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("mutator %q: want name=factor", text)
		}
		if opts.MutatorArgs == nil {
			opts.MutatorArgs = map[string]string{}
		}
		opts.MutatorArgs[strings.TrimSpace(name)] = strings.TrimSpace(value)
		if _, err := content.ParseMutators(opts.MutatorArgs); err != nil {
			return err
		}
		return nil
	})
	var restrictArgs []string
	set.Func("restrict", "unit restriction <unit>=<count>, e.g. armpw=20 or armkrog=0: 0 removes the unit from a skirmish or Survival battle and 1..100 caps each player's units of it, in every gameplay mode (repeatable; none selects no restrictions; omitted uses the saved set in the window, and none for --shot, --film, --battle-benchmark and --headless; refused with --mission and --load-save)", func(text string) error {
		restrictArgs = append(restrictArgs, text)
		return nil
	})
	set.Func("ai-player", "a computer player's AI by lobby row, <row>=<classic|modern> or all=<classic|modern> for every computer row (repeatable; a named row overrides all), for a battle the command line composes, in any gameplay mode: row 2 is the --map skirmish's computer player, rows 2 and 3 a Survival battle's buddies; omitted rows play Classic, and the lobby's rows carry their own choice", func(text string) error {
		choice, err := session.ParseComputerAI(text)
		if err != nil {
			return fmt.Errorf("nanolathe: invalid computer AI: logical path <command line>, providers searched [ai-player], expected <row>=<classic|modern> or all=<classic|modern>: %w", err)
		}
		opts.ComputerAI = append(opts.ComputerAI, choice)
		return nil
	})
	set.Func("ai", "Modern AI brain parameters for every computer player, key=value[,key=value...], e.g. style=eco,jitter=0 (repeatable; they act on every Modern AI computer player; omitted uses the saved modernAI block in the window, and none for --shot, --film, --battle-benchmark and --headless; keys: docs/DESIGN_SESSIONS_AI_SAVE.md \"Modern AI computer player\")", func(text string) error {
		params, err := aikitmod.ValidateParamsText(text)
		if err != nil {
			return aiParamsError("--ai "+text, err)
		}
		if opts.AIArgs == nil {
			opts.AIArgs = map[string]string{}
		}
		// A later flag's value for a key wins, as a later --mutator's does.
		maps.Copy(opts.AIArgs, params)
		return nil
	})
	set.StringVar(&opts.InstallMod, "install-mod", "", "install a mod zip or directory into the mod library, then exit")
	set.Func("gameplay-feature", "community feature override name=value (repeatable; Strict ignores overrides)", func(text string) error {
		v, err := community.ParseOverride(text)
		if err == nil {
			opts.GameplayOverrides = append(opts.GameplayOverrides, v)
		}
		return err
	})
	set.Func("gameplay", "gameplay rule set: modern (default), community-3.9, strict-3.1, or a registered set's name (see mods/); omitted uses saved preference", func(text string) error {
		mode, err := gameplay.Parse(text)
		opts.Gameplay = mode
		return err
	})
	set.StringVar(&opts.Renderer, "renderer", settings.DefaultPresentation().Renderer, "start-up presentation renderer (omitted uses saved preference): \"classic\" (software) or \"modern\" (GPU); any other value is classic")
	set.BoolVar(&opts.Fullscreen, "fullscreen", false, "desktop fullscreen (Alt+Enter toggles); omitted uses saved preference")
	set.BoolVar(&opts.Stats, "stats", false, "print periodic presentation statistics to the terminal")
	set.IntVar(&opts.UIScale, "ui-scale", -1, "Modern battle UI scale: 0 Auto, 1 or 2 (omitted uses the saved preference); also applies to --shot")
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
		case "local-mp-listen", "local-mp-join", "local-mp-command-delay-ms", "relay-address", "relay-room", "relay-ca", "relay-insecure-loopback":
			localMPSet = true
		case "unit-limit":
			unitLimitSet = true
		case "fullscreen":
			opts.FullscreenSet = true
		case "gameplay":
			opts.GameplaySet = true
		case "mod":
			opts.ModSet = true
		case "renderer":
			opts.RendererSet = true
		case "arrival":
			opts.ArrivalSet = true
		case "fps":
			opts.FPSSet = true
		case "live-seconds":
			liveSecondsSet = true
		case "benchmark-pre-ticks":
			benchmarkPreTicksSet = true
		}
	})
	if opts.BattleBenchmark != "" {
		if opts.BenchmarkRenderer == "" && opts.Renderer == "metal" {
			opts.BenchmarkRenderer = "metal"
		}
		switch opts.BenchmarkRenderer {
		case "":
		case "classic", "modern":
			opts.Renderer, opts.RendererSet = opts.BenchmarkRenderer, true
		case "metal":
			opts.Renderer, opts.RendererSet = "modern", true
		default:
			return opts, fmt.Errorf("nanolathe: benchmark renderer must be classic, modern or metal")
		}
		if opts.BenchmarkScene != "coastal" && opts.BenchmarkScene != "field" {
			return opts, fmt.Errorf("nanolathe: benchmark scene must be coastal or field")
		}
		if opts.BenchmarkScene == "field" && !benchmarkPreTicksSet {
			opts.BenchmarkPreTicks = int(headless.SimBenchDefaultWarmupTicks)
		}
	}
	if localMPSet {
		var refused error
		if !opts.multiplayerPlaytest() {
			refused = localMultiplayerError("address", "a local play-test address or --relay-address host:port")
		}
		set.Visit(func(f *flag.Flag) {
			if !localMultiplayerFlagAllowed(f.Name) {
				refused = localMultiplayerError("--"+f.Name, "a window play-test option")
			}
			if strings.HasPrefix(f.Name, "relay-") && opts.RelayAddress == "" || strings.HasPrefix(f.Name, "local-mp-") && opts.RelayAddress != "" {
				refused = localMultiplayerError("--"+f.Name, "one local or hosted transport")
			}
			if f.Name == "local-mp-command-delay-ms" && opts.LocalMPListen == "" {
				refused = localMultiplayerError("--"+f.Name, "--local-mp-listen to set the common order delay")
			}
			if f.Name == "arrival" && opts.Arrival {
				refused = localMultiplayerError("--arrival", "no startup arrival in a multiplayer game")
			}
			if f.Name == "seed" && opts.Seed < 0 {
				refused = localMultiplayerError("--seed", "a nonnegative fixed seed")
			}
		})
		if refused != nil {
			fmt.Fprintln(out, refused)
			return opts, refused
		}
		if err := validateLocalMultiplayerOptions(opts); err != nil {
			fmt.Fprintln(out, err)
			return opts, err
		}
		opts.Gameplay, opts.GameplaySet = gameplay.Modern, true
		opts.Arrival, opts.ArrivalSet = false, true
	}
	if opts.Survival {
		if _, err := survival.ParsePace(opts.SurvivalPace); err != nil {
			fmt.Fprintln(out, err)
			return opts, err
		}
		if opts.Map == "" || opts.Mission != "" || opts.LoadSave != "" || opts.SurvivalBuddies < 0 || opts.SurvivalBuddies > session.SurvivalMaxBuddies {
			err := fmt.Errorf("nanolathe: invalid survival selection: logical path <command line>, providers searched [survival], expected --map, no --mission or --load-save, and --survival-buddies 0..%d", session.SurvivalMaxBuddies)
			fmt.Fprintln(out, err)
			return opts, err
		}
	}
	if len(restrictArgs) != 0 {
		// A mission keeps its own authored unit list and a save brings its
		// own set (docs/DESIGN_MODS_MUTATORS.md §15.1, §15.5).
		if opts.Mission != "" || opts.LoadSave != "" {
			err := fmt.Errorf("nanolathe: unit restrictions do not apply to a campaign mission or a loaded save: logical path <command line>, providers searched [restrict], expected no --restrict with --mission or --load-save")
			fmt.Fprintln(out, err)
			return opts, err
		}
		restrictions, err := headless.ParseRestrictFlags(restrictArgs)
		if err != nil {
			fmt.Fprintln(out, err)
			return opts, err
		}
		opts.Restrictions, opts.RestrictionsSet = restrictions, true
	}
	if len(opts.ComputerAI) != 0 && (opts.Map == "" || opts.Mission != "" || opts.LoadSave != "") {
		err := fmt.Errorf("nanolathe: invalid computer AI selection: logical path <command line>, providers searched [ai-player], expected --map and no --mission or --load-save")
		fmt.Fprintln(out, err)
		return opts, err
	}
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
		if opts.Metal || opts.MetalModelCapture != "" {
			return opts, fmt.Errorf("nanolathe: battle benchmark cannot be combined with --metal or model capture")
		}
		if opts.BenchmarkScene == "field" && (opts.BenchmarkArmySize < headless.SimBenchMinArmySize || opts.BenchmarkArmySize > headless.SimBenchMaxArmySize || opts.BenchmarkCapture != "" || opts.Survival || !opts.BenchmarkFactories || opts.BenchmarkScale != 1 || len(opts.ComputerAI) != 0) {
			return opts, fmt.Errorf("nanolathe: field benchmark requires 250..1000 units per army, Classic computer players and the ordinary fixture production, without capture, Survival or coastal scaling")
		}
		if opts.BenchmarkCapture != "" && (!opts.Survival || opts.Map == "") {
			return opts, fmt.Errorf("nanolathe: capture benchmark requires --survival and --map")
		}
		// --zoom is accepted here: the benchmark scene is the same battle at
		// twice the pixels, and the detail view's cost is exactly what the
		// benchmark exists to measure (DESIGN_GPU_RENDERER §14.6). The scale
		// is recorded in the scene metadata, so two runs are only compared
		// when they were captured at the same one.
		if opts.Shot != "" || opts.ShotModel != "" || opts.Headless || opts.LoadSave != "" || opts.Mission != "" || opts.CPUProfile != "" || opts.MemProfile != "" || opts.ProfileSeconds != 0 || opts.ShotRenderer != "" || opts.ShotGPUProfileFrames != 0 || (opts.BenchmarkScene != "field" && opts.BenchmarkCapture == "" && opts.ShotSize != "" && opts.ShotSize != "1920x1080") {
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
			opts.Map = "expanded confluence"
			if opts.BenchmarkScene == "field" {
				opts.Map = headless.SimBenchDefaultMap
			}
		}
		if opts.Seed < 0 {
			opts.Seed = 7
		}
		opts.Shot = filepath.Join(opts.BattleBenchmark, "battle.png")
		if opts.ShotSize == "" {
			opts.ShotSize = "1920x1080"
		}
	}
	if opts.LiveTrace != "" || opts.LiveScene != "" {
		// Without --map the trace times play started from the menus, and a
		// session of play runs until the window closes unless told otherwise.
		if opts.LiveTrace == "" || opts.LiveScene != "" && opts.Map == "" || opts.Mission != "" || opts.LoadSave != "" && opts.Map != "" || opts.Headless || opts.Shot != "" || opts.BattleBenchmark != "" || opts.Film != "" {
			return opts, fmt.Errorf("nanolathe: --live-trace requires the window, from the menus (with or without --load-save) or a --map battle, and --live-scene requires --live-trace and --map")
		}
		if opts.Map == "" && !liveSecondsSet {
			opts.LiveSeconds = 0
		}
		scene, err := parseLiveScene(opts.LiveScene)
		if err != nil {
			return opts, err
		}
		if scene.Kind == "capture" && !opts.Survival {
			return opts, fmt.Errorf("nanolathe: a capture live scene requires --survival and --map")
		}
		if opts.LiveSpeed != 0 && (opts.Map == "" || opts.LiveSpeed < 1 || opts.LiveSpeed > 20) {
			return opts, fmt.Errorf("nanolathe: --live-speed takes 1..20 and requires --live-trace and --map")
		}
		if opts.LiveFlight && opts.LiveExecTrace > 0 {
			return opts, fmt.Errorf("nanolathe: --live-flight cannot be combined with --live-exec-trace")
		}
		if opts.Seed < 0 && scene.Kind != "" {
			opts.Seed = 7
		}
	} else if opts.LiveUnpaced {
		return opts, fmt.Errorf("nanolathe: --live-unpaced requires --live-trace")
	}
	if opts.BenchmarkCaptureCopies < 1 || opts.BenchmarkCaptureCopies > 4 {
		return opts, fmt.Errorf("nanolathe: benchmark capture copies must be 1..4")
	}
	if opts.BenchmarkCapture != "" && opts.BattleBenchmark == "" {
		return opts, fmt.Errorf("nanolathe: --benchmark-capture requires --battle-benchmark")
	}
	if opts.Shot != "" || opts.ShotModel != "" {
		if err := validateShotOptions(opts); err != nil {
			return opts, err
		}
	}
	if err := validateMetalOptions(opts); err != nil {
		return opts, err
	}
	return opts, nil
}

// validateMetalOptions keeps the Metal flags to the routes that read them: the
// --metal battle and the Metal battle benchmark (docs/DESIGN_METAL_RENDERER.md).
func validateMetalOptions(opts Options) error {
	metalBenchmark := opts.BattleBenchmark != "" && opts.BenchmarkRenderer == "metal"
	if !opts.Metal && (opts.MetalReport != "" || opts.MetalModelCapture != "" || opts.MetalFrames != 0) {
		return fmt.Errorf("nanolathe: --metal-report, --metal-model-capture and --metal-frames require --metal")
	}
	if opts.Metal {
		if opts.Map == "" && opts.Mission == "" {
			return fmt.Errorf("nanolathe: --metal plays one battle and requires --map or --mission")
		}
		if opts.Shot != "" || opts.ShotModel != "" || opts.Headless || opts.LoadSave != "" {
			return fmt.Errorf("nanolathe: --metal cannot be combined with --shot, --headless or --load-save")
		}
		if opts.MetalModelCapture != "" && opts.MetalReport == "" {
			return fmt.Errorf("nanolathe: --metal-model-capture requires --metal-report")
		}
	}
	if (opts.MetalQuads != 1 || opts.MetalTextures != 1) && !metalBenchmark {
		return fmt.Errorf("nanolathe: --metal-quads and --metal-textures apply only to the Metal battle benchmark")
	}
	if (opts.MetalQuads != 1 && opts.MetalQuads != 2 && opts.MetalQuads != 5) || (opts.MetalTextures != 1 && opts.MetalTextures != 2) {
		return fmt.Errorf("nanolathe: Metal benchmark requires geometry 1, 2 or 5 and textures 1 or 2")
	}
	return nil
}
