package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// skirmishConfigFor maps CLI options to a SkirmishConfig via the canonical
// DirectSkirmishConfig normalization [08 "Skirmish configuration"] [GAP T14].
func skirmishConfigFor(opts Options) session.SkirmishConfig {
	if opts.Map == "" {
		cfg := session.SkirmishConfig{MapName: opts.Map}
		_ = cfg.Normalize()
		return cfg
	}
	cfg := session.DirectSkirmishConfig(opts.Map)
	// Ensure map name from opts overrides any normalized default.
	cfg.MapName = opts.Map
	_ = cfg.Normalize()
	return cfg
}

// newSessionFor compiles the catalog once and builds the skirmish session.
func newSessionFor(opts Options, cs *contentSet) (*session.Session, *content.Catalog, error) {
	cat, err := content.Compile(cs.fs)
	if err != nil {
		return nil, nil, fmt.Errorf("catalog: %w", err)
	}
	// --mission routes to the campaign/mission session; without it the
	// command is a skirmish setup. An unrecognized mission reference fails
	// the run explicitly rather than silently degrading to skirmish.
	if opts.Mission != "" {
		sess, err := session.NewMissionWithFS(cs.fs, cat, opts.Mission, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("mission setup: %w", err)
		}
		return sess, cat, nil
	}
	sess, err := session.NewSkirmishWithFS(cs.fs, cat, skirmishConfigFor(opts))
	if err != nil {
		return nil, nil, fmt.Errorf("skirmish setup: %w", err)
	}
	return sess, cat, nil
}

// stepTicks advances the session one sub-tick per call; Step anchors the SP
// clock on the first call so scaledNow = i yields exactly one tick each.
func stepTicks(sess *session.Session, ticks int) {
	for i := 0; i < ticks; i++ {
		sess.Step(int32(i))
	}
}

// printSessionSummary reports final unit slots and both stream states + draw
// counts so two seeded runs are byte-comparable (PLAN_00 C4 finish).
func printSessionSummary(sess *session.Session, out *os.File) {
	for _, u := range sess.Units.Iter() {
		fmt.Fprintf(out, "skirmish unit %d x=%d z=%d health=%d remaining=%.4f\n",
			u.Handle, int64(u.X), int64(u.Z), u.Health, u.Remaining)
	}
	if rng.Global.Sim != nil {
		fmt.Fprintf(out, "rng sim: state=0x%08x draws=%d\n", rng.Global.Sim.State, rng.Global.Sim.Draws())
	}
	if rng.Global.Crt != nil {
		fmt.Fprintf(out, "rng crt: state=0x%08x draws=%d\n", rng.Global.Crt.State, rng.Global.Crt.Draws())
	}
}

// headlessJSON is the structured JSON result for --until-result [ON-07].
type headlessJSON struct {
	WinnerTeam *int              `json:"winner_team"`
	Reason     string            `json:"reason"`
	Tick       uint32            `json:"tick"`
	Draw       bool              `json:"draw"`
	ArmedTick  *uint32           `json:"armed_tick,omitempty"`
	Milestones map[string]uint32 `json:"milestones,omitempty"`
}

// runSessionHeadless runs the real integrated session for --ticks sub-ticks,
// prints the deterministic summary, and honors --save. When --until-result is
// set it runs until the terminal skirmish result becomes visible via EndLatch
// [P1-01 §2.2] and emits structured JSON [ON-07].
func runSessionHeadless(opts Options, cs *contentSet, out *os.File) error {
	sess, _, err := newSessionFor(opts, cs)
	if err != nil {
		return err
	}
	if opts.UntilResult {
		return runUntilResult(sess, opts, out)
	}
	stepTicks(sess, opts.Ticks)
	printSessionSummary(sess, out)
	if opts.Save != "" {
		if err := saveSession(sess, opts.Save, opts.Map); err != nil {
			return fmt.Errorf("save %s: %w", opts.Save, err)
		}
		fmt.Fprintf(out, "saved: %s\n", opts.Save)
	}
	return nil
}

// runUntilResult runs until the authoritative result is visible or MaxTick guard fires [ON-07].
func runUntilResult(sess *session.Session, opts Options, out *os.File) error {
	maxTick := opts.MaxTick
	// When MaxTick is 0 and Ticks is set, use Ticks as guard for convenience.
	if maxTick <= 0 && opts.Ticks > 0 {
		maxTick = opts.Ticks
	}
	// Hard safety cap when no guard is set to avoid infinite loop in CI.
	const hardCap = 100000
	if maxTick <= 0 {
		maxTick = hardCap
	}
	// Loop driving the SP clock. Step anchors on first call so scaledNow=i gives ~1 tick.
	for iter := 0; iter < maxTick+5000; iter++ {
		// Check guard before stepping: GlobalTick is authoritative [01 §4.4].
		if sess.Clock != nil && int(sess.Clock.GlobalTick) >= maxTick {
			res := sess.GetResult()
			// Emit JSON timeout
			j := headlessJSON{
				Reason: "max_tick",
				Tick:   sess.Clock.GlobalTick,
				Draw:   false,
			}
			if res.Ended {
				// If result already ended but we hit guard exactly, treat as success.
				return emitResultJSON(res, out, sess)
			}
			b, _ := json.Marshal(j)
			fmt.Fprintln(out, string(b))
			return fmt.Errorf("headless: max-tick %d reached without result (tick %d)", maxTick, sess.Clock.GlobalTick)
		}
		sess.Step(int32(iter))
		// Check for terminal result latched exactly once [08][P1-01].
		res := sess.GetResult()
		if res.Ended {
			if err := emitResultJSON(res, out, sess); err != nil {
				return err
			}
			printSessionSummary(sess, out)
			if opts.Save != "" {
				if err := saveSession(sess, opts.Save, opts.Map); err != nil {
					return fmt.Errorf("save %s: %w", opts.Save, err)
				}
				fmt.Fprintf(out, "saved: %s\n", opts.Save)
			}
			return nil
		}
		// Fallback for campaign latch (no skirmish result) – still emit something.
		if sess.Latch.IsEnding() {
			// Derive winner from latch bits for non-skirmish sessions.
			win := sess.Latch.IsWin()
			draw := !win && !sess.Latch.IsLose()
			var winner *int
			if !draw {
				w := 0
				if !win {
					w = 1
				}
				winner = &w
			}
			j := headlessJSON{
				WinnerTeam: winner,
				Reason:     "latch",
				Tick:       sess.Clock.GlobalTick,
				Draw:       draw,
			}
			if sess.Clock != nil {
				armed := sess.GetResultArmedTick()
				if armed != 0 {
					j.ArmedTick = &armed
					j.Milestones = map[string]uint32{"armed": armed, "visible": j.Tick}
				}
			}
			b, _ := json.Marshal(j)
			fmt.Fprintln(out, string(b))
			printSessionSummary(sess, out)
			if opts.Save != "" {
				if err := saveSession(sess, opts.Save, opts.Map); err != nil {
					return fmt.Errorf("save %s: %w", opts.Save, err)
				}
				fmt.Fprintf(out, "saved: %s\n", opts.Save)
			}
			return nil
		}
		// Safety: if iter exceeds hardCap+maxTick, bail.
		if iter > maxTick+1000 && maxTick == hardCap {
			j := headlessJSON{Reason: "no_result", Tick: sess.Clock.GlobalTick}
			b, _ := json.Marshal(j)
			fmt.Fprintln(out, string(b))
			return fmt.Errorf("headless: no result after %d ticks", iter)
		}
	}
	// If loop exhausted, timeout.
	j := headlessJSON{Reason: "max_tick", Tick: sess.Clock.GlobalTick}
	b, _ := json.Marshal(j)
	fmt.Fprintln(out, string(b))
	return fmt.Errorf("headless: max-tick %d reached without result", maxTick)
}

func emitResultJSON(res session.Result, out *os.File, sess *session.Session) error {
	var winner *int
	if !res.Draw {
		w := res.WinnerTeam
		winner = &w
	}
	j := headlessJSON{
		WinnerTeam: winner,
		Reason:     res.Reason,
		Tick:       res.Tick,
		Draw:       res.Draw,
	}
	if res.ArmedTick != 0 {
		j.ArmedTick = &res.ArmedTick
		j.Milestones = map[string]uint32{"armed": res.ArmedTick, "visible": res.Tick}
	} else {
		armed := sess.GetResultArmedTick()
		if armed != 0 {
			j.ArmedTick = &armed
			if j.Milestones == nil {
				j.Milestones = map[string]uint32{}
			}
			j.Milestones["armed"] = armed
			j.Milestones["visible"] = j.Tick
		}
	}
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, string(b))
	return nil
}

// runLoadAndContinue restores a native StateV1 save through published APIs,
// steps --ticks more sub-ticks continuing the clock, prints the summary, and
// honors --save for re-saving (PLAN_14 C18).
func runLoadAndContinue(opts Options, cs *contentSet, out *os.File) error {
	bank, err := save.OpenBankFile(opts.Load)
	if err != nil {
		return fmt.Errorf("load %s: %w", opts.Load, err)
	}
	cat, err := content.Compile(cs.fs)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	state, err := save.ReadStateV1(bank, cat.Hash, cat.Manifest)
	if err != nil {
		return fmt.Errorf("load %s: %w", opts.Load, err)
	}
	// The saved summary carries the map selection and the game type; --map is
	// optional on --load.
	cfg := skirmishConfigFor(opts)
	sum, haveSummary := save.ReadSummary(bank)
	if cfg.MapName == "" && haveSummary {
		cfg.MapName = sum.MapName
	}
	// This path composes a skirmish. A campaign save restored into one would
	// silently continue with the wrong mode, topology and result ownership, so
	// say so instead of guessing [08 "Summary"] [GAP T9].
	// TODO(question): mission composition from a save header is W5-2/W5-5; the
	// Summary already carries Campaign and Mission names, but the mission path
	// and index needed to recompose the session are not established.
	if haveSummary && sum.Gametype == 1 {
		return fmt.Errorf("nanolathe: cannot resume a campaign save through the skirmish path: campaign %q mission %q, expected a skirmish save",
			sum.Campaign, sum.Mission)
	}
	sess, err := session.NewSkirmishWithFS(cs.fs, cat, cfg)
	if err != nil {
		return err
	}
	if err := restoreSessionState(sess, state); err != nil {
		return fmt.Errorf("load %s: %w", opts.Load, err)
	}
	start := state.Clock.GlobalTick + 1
	stepFrom(sess, start, opts.Ticks)
	printSessionSummary(sess, out)
	if opts.Save != "" {
		// Preserve the identity we loaded: --map is optional on --load, so
		// resaving with opts.Map alone dropped the map name from the new file.
		resaveMap := opts.Map
		if resaveMap == "" {
			resaveMap = cfg.MapName
		}
		if err := saveSession(sess, opts.Save, resaveMap); err != nil {
			return fmt.Errorf("save %s: %w", opts.Save, err)
		}
		fmt.Fprintf(out, "saved: %s\n", opts.Save)
	}
	return nil
}

// stepFrom advances exactly ticks sub-ticks with a monotonically increasing
// scaled-now continuing from the restored global tick.
func stepFrom(sess *session.Session, start uint32, ticks int) {
	for i := 0; i < ticks; i++ {
		sess.Step(int32(start) + int32(i))
	}
}

// saveSession writes a native HAPIBANK: established metadata boxes plus the
// StateV1 continuation box via the sole codec internal/save [P0-I11][PLAN_14 C18].
// Reconstruction uses published APIs only and forced slot identity [01 §6.1][P0-I11].
func saveSession(sess *session.Session, path, mapName string) error {
	b := save.NewBuilder("Total Annihilation 3.0")
	save.WriteSummary(b, sessionSummary(sess, mapName))
	if sess.Clock != nil {
		save.WriteGameTime(b, sess.Clock)
	}
	selfSlot, alliances := sessionAlliances(sess)
	save.WriteAlliances(b, selfSlot, alliances)
	// Sole codec: session.CaptureStateV1 via internal/save [P0-I11]
	if sess != nil {
		st := sess.CaptureStateV1()
		if st != nil {
			save.WriteStateV1(b, st)
		}
	}
	return save.WriteBankFile(path, b.Bytes())
}

// restoreSessionState restores via the sole codec with forced slot identity
// [P0-I11][01 §6.1][PLAN_14 C18].
//
// The restore error used to be discarded, so a failed or partial restore was
// followed by a perfectly normal-looking continuation summary. A restore that
// did not succeed must not reach simulation.
func restoreSessionState(sess *session.Session, st *save.StateV1) error {
	if sess == nil || st == nil {
		return fmt.Errorf("nanolathe: restore: no session or no saved state")
	}
	// Delegate to session's sole restore path which handles RNG, clock, forced slots,
	// orders, movement routes, economy, features, projectiles, AI, visibility, latch, wind [P0-I11].
	if err := sess.RestoreStateV1(st); err != nil {
		return fmt.Errorf("nanolathe: restore saved state: %w", err)
	}
	sess.RecalcLocalOwner()
	return nil
}

// sessionSummary fills the established Summary provenance from the live
// session instead of writing a lone map name. Without it a resave lost the
// map, the game type, the player count and every selected rule, so the saved
// file could not say what it was a save OF [08 "Summary"].
func sessionSummary(sess *session.Session, mapName string) save.Summary {
	sum := save.Summary{MapName: mapName}
	if sess == nil {
		return sum
	}
	if sum.MapName == "" {
		sum.MapName = sess.Skirmish.MapName
	}
	cfg := sess.Skirmish
	sum.Players = int32(cfg.NumPlayers)
	sum.Side = int32(cfg.Players[sess.LocalOwner].Side)
	sum.Difficulty = int32(cfg.Difficulty)
	sum.CommanderDeath = int32(cfg.CommanderDeath)
	sum.Location = int32(cfg.Location)
	sum.Mapping = int32(cfg.Mapping)
	sum.LineOfSight = int32(cfg.LineOfSight)
	sum.LineOfSightType = int32(cfg.LOSType)
	if sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign {
		sum.Gametype = 1 // campaign [08 "Summary"] [GAP T9]
	}
	return sum
}

// sessionAlliances reports the local slot and the per-slot alliance byte row
// the save should carry. The writer used to hand in a hardcoded empty row and
// slot zero, which described a save with no alliances at all regardless of
// what the battle actually had [08 "Player records"].
func sessionAlliances(sess *session.Session) (int, [11]byte) {
	var row [11]byte
	if sess == nil {
		return 0, row
	}
	local := int(sess.LocalOwner)
	cfg := sess.Skirmish
	for i := 0; i < cfg.NumPlayers && i < len(row); i++ {
		if cfg.Players[i].AllyGroup == cfg.Players[sess.LocalOwner].AllyGroup {
			row[i] = 1
		}
	}
	return local, row
}
