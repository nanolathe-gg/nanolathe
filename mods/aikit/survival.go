package aikit

import (
	"maps"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/survival"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// A Survival battle's computer buddies (docs/DESIGN_SURVIVAL.md "Computer
// survivors under the Modern AI"). The session tells every survivor's
// manager the scenario at battle entry (ai.Manager.Survival); when the
// manager is a Modern computer buddy's, the Modern AI's controller builds the
// survival brain — the util+tac layers with the survival defaults below,
// wrapped by internal/aikit/brains/survival — instead of the skirmish
// brain. Every other manager, and every battle that is not Survival, gets
// exactly the brain it got before. Configured parameters apply on top of
// the survival defaults, key by key, and survival=0 plays the skirmish
// brain in Survival too.

// survivalDefaults are the util+tac parameters a survivor plays before its
// configured ones: the utility towers answer danger only (the survival
// layer plans the ring), no scouts (there is no base to find), a compact
// base, the balanced style, an
// earlier tech-2 transition (the waves climb the build tree), and a
// tactics army with no raiding, harassing or probing.
var survivalDefaults = map[string]string{
	"def_plan":    "0",
	"w_scout":     "0",
	"tech_time":   "12",
	"wide_base":   "0",
	"style":       "balanced",
	"personality": "off",
	"raid":        "0",
	"harass":      "0",
	"probe":       "0",
	"tour":        "0",
}

// survivalPlay reports whether manager m's controller plays the survival
// brain, with the configured parameters it plays.
func survivalPlay(m *ai.Manager) (map[string]string, bool) {
	if m == nil || !m.Survival.ComputerSurvivor(m.Player) {
		return nil, false
	}
	kv, err := ValidateParamsText(m.ControllerParams)
	if err != nil {
		kv = nil
	}
	if !survival.Enabled(kv) {
		return nil, false
	}
	return kv, true
}

// newSurvivalHost builds a computer survivor's controller.
func newSurvivalHost(m *ai.Manager, configured map[string]string) *aikit.Host {
	return aikit.NewHost(m, survivalBrain(m.Survival, m.Player, configured), personaFor(m))
}

// survivalBrain composes the survival brain for player from the scenario
// and the configured parameters over the survival defaults.
func survivalBrain(info *ai.SurvivalInfo, player uint8, configured map[string]string) *core.Brain {
	kv := maps.Clone(survivalDefaults)
	maps.Copy(kv, configured)
	ut, err := NewUtilTac(kv)
	if err != nil {
		ut, _ = NewUtilTac(survivalDefaults)
	}
	sp, err := survival.ParamsFrom(kv)
	if err != nil {
		sp = survival.DefaultParams()
	}
	sc := survival.Scenario{CentreX: info.CentreX, CentreZ: info.CentreZ, Me: player, Feed: &survivalFeed{info: info}}
	sc.Team = append(sc.Team, info.Team...)
	sc.Computer = append(sc.Computer, info.Computer...)
	sc.Starts = append(sc.Starts, info.Starts...)
	br := ut.Brain
	return survival.New(sc, sp, survival.Layers{Strategy: br.Strategy, Economy: br.Economy, Army: br.Army, Production: br.Prod})
}

// survivalFeed hands the brain the warnings the director published at or
// before its observation's tick (ai.SurvivalInfo.Warnings). A published
// warning never changes, so each is converted once. One host's brain calls
// it from one think at a time, with ticks that never go back.
type survivalFeed struct {
	info *ai.SurvivalInfo
	buf  []ai.SurvivalWarning
	conv []survival.Warning
}

func (f *survivalFeed) Warnings(tick uint32, dst []survival.Warning) []survival.Warning {
	f.buf = f.info.Warnings(tick, f.buf[:0])
	for i := len(f.conv); i < len(f.buf); i++ {
		w := &f.buf[i]
		out := survival.Warning{Wave: w.Wave, Tick: w.Tick, Arrive: w.Arrive}
		for _, g := range w.Groups {
			a := survival.Approach{Angle: g.Angle, X: g.X, Z: g.Z}
			switch g.Domain {
			case "air":
				a.Air = true
			case "naval":
				a.Naval = true
			case "hover":
				a.Hover = true
			default:
				a.Other = true
			}
			out.Groups = append(out.Groups, a)
		}
		f.conv = append(f.conv, out)
	}
	return append(dst, f.conv[:len(f.buf)]...)
}
