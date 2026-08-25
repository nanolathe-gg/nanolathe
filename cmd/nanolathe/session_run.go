package main

import (
	"fmt"
	"os"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// skirmishConfigFor maps CLI options to a SkirmishConfig
// [08 "Skirmish configuration"]. One human slot 0 plus one computer slot 1 is
// enough for a headless gate run; NumPlayers keeps its validated default when
// unset.
func skirmishConfigFor(opts Options) session.SkirmishConfig {
	cfg := session.SkirmishConfig{MapName: opts.Map}
	cfg.ApplyDefaults()
	if cfg.NumPlayers < 2 {
		cfg.NumPlayers = 2
	}
	cfg.Players[0].Controller = 0 // human
	if cfg.NumPlayers > 1 {
		cfg.Players[1].Controller = 1 // computer; economy maps to controller state 2 [PLAN_11 C1]
	}
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

// runSessionHeadless runs the real integrated session for --ticks sub-ticks,
// prints the deterministic summary, and honors --save.
func runSessionHeadless(opts Options, cs *contentSet, out *os.File) error {
	sess, _, err := newSessionFor(opts, cs)
	if err != nil {
		return err
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
	// The saved summary carries the map selection; --map is optional on --load.
	cfg := skirmishConfigFor(opts)
	if cfg.MapName == "" {
		if sum, ok := save.ReadSummary(bank); ok {
			cfg.MapName = sum.MapName
		}
	}
	sess, err := session.NewSkirmishWithFS(cs.fs, cat, cfg)
	if err != nil {
		return err
	}
	restoreSessionState(sess, cat, state)
	start := state.Clock.GlobalTick + 1
	stepFrom(sess, start, opts.Ticks)
	printSessionSummary(sess, out)
	if opts.Save != "" {
		if err := saveSession(sess, opts.Save, opts.Map); err != nil {
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
	save.WriteSummary(b, save.Summary{MapName: mapName})
	if sess.Clock != nil {
		save.WriteGameTime(b, sess.Clock)
	}
	save.WriteAlliances(b, 0, [11]byte{})
	// Sole codec: session.CaptureStateV1 via internal/save [P0-I11]
	if sess != nil {
		st := sess.CaptureStateV1()
		if st != nil {
			save.WriteStateV1(b, st)
		}
	}
	return save.WriteBankFile(path, b.Bytes())
}

// restoreSessionState restores via the sole codec with forced slot identity [P0-I11][01 §6.1][PLAN_14 C18].
func restoreSessionState(sess *session.Session, cat *content.Catalog, st *save.StateV1) {
	if sess == nil || st == nil {
		return
	}
	// Delegate to session's sole restore path which handles RNG, clock, forced slots,
	// orders, movement routes, economy, features, projectiles, AI, visibility, latch, wind [P0-I11].
	_ = cat
	_ = sess.RestoreStateV1(st)
}
