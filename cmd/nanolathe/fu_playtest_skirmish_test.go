//go:build retail

package main

// Scenario 1 of the fu-playtest acceptance round: a complete skirmish on
// "ashap plateau", seed 7, observed through periodic censuses of the unit
// pool, the factory queues and the AI managers, plus composed frames of the
// busy moments. The census runs reproduce the displayless runner's stepping
// exactly. The frame runs come in two rule pairs: the default Unmapped + True
// pair, where the fight reaches the local commander and is drawn, and the
// Mapped + Permanent pair, where the enemy base is drawn rather than fogged
// [08 "Skirmish configuration"] [03 R-VIS-01 §1].

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

const fuSkirmishMap = "ashap plateau"

type fuSkirmishReport struct {
	Map       string          `json:"map"`
	Seed      int64           `json:"seed"`
	Players   int             `json:"players"`
	Mapping   int             `json:"mapping"`
	LOS       int             `json:"lineofsight"`
	Tick      uint32          `json:"tick"`
	State     string          `json:"state"`
	StateHash string          `json:"state_hash"`
	Result    session.Result  `json:"result"`
	Censuses  []fuCensus      `json:"censuses"`
	Final     fuCensus        `json:"final"`
	AirEver   map[int]uint32  `json:"air_first_seen_tick"`
	FirstKill uint32          `json:"first_kill_or_loss_tick"`
	Errors    []string        `json:"errors,omitempty"`
	Composite map[string]bool `json:"composite"`
}

func fuMappedRules(cfg session.SkirmishConfig) session.SkirmishConfig {
	cfg.ApplyDefaults()
	// A deliberately chosen zero survives once the defaults have been installed
	// [08 "Skirmish configuration"] C8.
	cfg.Mapping, cfg.LineOfSight = 0, 0
	return cfg
}

func fuThreePlayerConfig() session.SkirmishConfig {
	cfg := session.DirectSkirmishConfig(fuSkirmishMap)
	cfg.NumPlayers = 3
	cfg.Players[2] = session.SkirmishPlayer{Controller: session.SkirmishControllerComputer, Side: 0, Color: 2, AllyGroup: 4, Metal: session.SkirmishDefaultMetal, Energy: session.SkirmishDefaultEnergy}
	return cfg
}

func fuEvents(sess *session.Session) int16 {
	var events int16
	for i := 0; i < 10; i++ {
		if sess.Econ.Players[i].Exists {
			events += sess.Econ.Players[i].Kills + sess.Econ.Players[i].Losses
		}
	}
	return events
}

func fuRunSkirmishCensus(t *testing.T, name string, cfg session.SkirmishConfig, seed int64, limit uint32) fuSkirmishReport {
	t.Helper()
	root := probeRetail(t)
	opts := Options{Root: root, Map: cfg.MapName, Seed: seed}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(uint32(seed), uint32(seed))
	sess, _, err := newBattleSessionWithConfig(opts, cs, cfg)
	if err != nil {
		t.Fatalf("%s: compose: %v", name, err)
	}
	report := fuSkirmishReport{Map: cfg.MapName, Seed: seed, Players: cfg.NumPlayers, Mapping: sess.Skirmish.Mapping, LOS: sess.Skirmish.LineOfSight, AirEver: map[int]uint32{}, Composite: map[string]bool{}}
	fuAdvance(sess, limit, func(tick uint32) {
		if report.FirstKill == 0 && fuEvents(sess) > 0 {
			report.FirstKill = tick
		}
		if tick%3000 != 0 {
			return
		}
		c := fuTakeCensus(sess)
		report.Censuses = append(report.Censuses, c)
		for _, s := range c.Sides {
			if s.Air > 0 {
				if _, seen := report.AirEver[s.Player]; !seen {
					report.AirEver[s.Player] = tick
				}
			}
		}
	})
	report.Final = fuTakeCensus(sess)
	report.Tick = sess.Clock.GlobalTick
	report.State = sess.State.String()
	report.Result = sess.GetResult()
	if hash, err := sess.PartialStateFingerprint(); err == nil {
		report.StateHash = hash
	} else {
		report.Errors = append(report.Errors, "hash: "+err.Error())
	}
	for _, s := range report.Final.Sides {
		if s.Air > 0 {
			if _, seen := report.AirEver[s.Player]; !seen {
				report.AirEver[s.Player] = report.Tick
			}
		}
		report.Composite[fmt.Sprintf("player%d_expanded", s.Player)] = s.Created > 1
		report.Composite[fmt.Sprintf("player%d_ground", s.Player)] = s.Ground > 0
		report.Composite[fmt.Sprintf("player%d_lost_units", s.Player)] = s.Losses > 0
		report.Composite[fmt.Sprintf("player%d_killed_units", s.Player)] = s.Kills > 0
	}
	fuWriteJSON(t, fuOutDir(t), name+".json", report)
	t.Logf("%s: state %s at tick %d, result %s (%s), hash %s, first kill/loss tick %d, air first seen %v",
		name, report.State, report.Tick, report.Result.Kind, report.Result.Reason, report.StateHash, report.FirstKill, report.AirEver)
	for _, s := range report.Final.Sides {
		t.Logf("%s: player %d: live %d created %d kills %d losses %d air %d ground %d structures %d factories %d",
			name, s.Player, s.Live, s.Created, s.Kills, s.Losses, s.Air, s.Ground, s.Structures, len(s.Factories))
	}
	return report
}

// TestFUPlaytestSkirmishSeed7Census is the displayless run the reviewer asked
// about, with the per-tick detail the JSON report does not carry: unit class
// counts, factory queues and AI group membership every 3000 ticks.
//
// The 108000-tick budget was raised from 54000 with the class-vector correction
// of [08 R-P0-05 §5]: that routine folds a clamped copy of the definition's
// passive `energymake` into the other-mix coefficient, which the previous
// arithmetic omitted altogether, so a passive energy producer now carries a
// non-zero other-mix weight instead of zero. The computer player builds economy
// before it builds an army, and the measured elimination on this map and seed
// moved from tick 32640 to 79650 by the same commander-death path. The cap is a
// test budget, not a contract, and the two assertions below are unchanged. The
// sibling three-player and mapped runs keep their 54000 budget: they assert no
// terminal state, so the shift does not reach them.
func TestFUPlaytestSkirmishSeed7Census(t *testing.T) {
	if testing.Short() {
		t.Skip("long acceptance trajectory: run tools/check-retail --full")
	}
	report := fuRunSkirmishCensus(t, "skirmish-seed7-census", session.DirectSkirmishConfig(fuSkirmishMap), 7, 108000)
	if report.State != session.StatePostBattle.String() {
		t.Fatalf("seed 7 did not reach the post-battle state by tick %d: state %s", report.Tick, report.State)
	}
	if !report.Result.Ended {
		t.Fatalf("post-battle state without an ended result: %+v", report.Result)
	}
}

// TestFUPlaytestSkirmishTwoComputerSides adds a second computer opponent in
// its own alliance so two expanding sides fight each other while the local
// commander idles; the two-player run has only one side that expands.
func TestFUPlaytestSkirmishTwoComputerSides(t *testing.T) {
	if testing.Short() {
		t.Skip("long acceptance trajectory: run tools/check-retail --full")
	}
	cfg := fuThreePlayerConfig()
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	fuRunSkirmishCensus(t, "skirmish-seed7-three-players", cfg, 7, 54000)
}

// TestFUPlaytestSkirmishMappedRules repeats both censuses under the
// Mapped + Permanent rule pair, the pair the frame captures use, so the
// frames can be read against a run that records whether an attack ever came.
func TestFUPlaytestSkirmishMappedRules(t *testing.T) {
	if testing.Short() {
		t.Skip("long acceptance trajectory: run tools/check-retail --full")
	}
	fuRunSkirmishCensus(t, "skirmish-seed7-mapped-census", fuMappedRules(session.DirectSkirmishConfig(fuSkirmishMap)), 7, 54000)
	cfg := fuMappedRules(fuThreePlayerConfig())
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	fuRunSkirmishCensus(t, "skirmish-seed7-mapped-three-players", cfg, 7, 54000)
}

type fuFrameNote struct {
	Tick        uint32         `json:"tick"`
	Label       string         `json:"label"`
	View        string         `json:"view"`
	File        string         `json:"file"`
	CameraX     int32          `json:"camera_x"`
	CameraZ     int32          `json:"camera_z"`
	UnitsInView map[string]int `json:"units_in_view"` // "owner<n>:<class>"
}

// fuCaptureViews composes frames of the same committed tick: the local
// commander's ground, each computer side's base, and the enemy mobile unit
// nearest a local unit. The camera is presentation state; moving it draws no
// random number and changes no authoritative field [I6].
func fuCaptureViews(t *testing.T, dir, prefix string, b *battleSession, cl *client.Client, tick uint32, label string) []fuFrameNote {
	sess := b.sess
	local := sess.LocalOwner
	px := func(u *units.Unit) (int32, int32) { return int32(u.X >> 16), int32(u.Z >> 16) }
	type view struct {
		name string
		unit *units.Unit
	}
	var views []view
	home := fuFindUnit(sess, local, func(u *units.Unit) bool { return u.Def.Commander })
	if home == nil {
		home = fuFindUnit(sess, local, func(*units.Unit) bool { return true })
	}
	views = append(views, view{"home", home})
	locals := fuUnitsOwnedBy(sess, local)
	var nearest *units.Unit
	var best int64 = -1
	for i := 0; i < 10; i++ {
		if !sess.Econ.Players[i].Exists || uint8(i) == local {
			continue
		}
		owner := uint8(i)
		base := fuFindUnit(sess, owner, func(u *units.Unit) bool {
			return u.Def.Builder && !fuMobile(sess, u) && !u.Def.CanFly && !u.Def.Commander
		})
		if base == nil {
			base = fuFindUnit(sess, owner, func(u *units.Unit) bool { return !fuMobile(sess, u) && !u.Def.CanFly })
		}
		views = append(views, view{fmt.Sprintf("base%d", i), base})
		for _, e := range fuUnitsOwnedBy(sess, owner) {
			if !fuMobile(sess, e) && !e.Def.CanFly {
				continue
			}
			ex, ez := px(e)
			for _, l := range locals {
				lx, lz := px(l)
				d := int64(ex-lx)*int64(ex-lx) + int64(ez-lz)*int64(ez-lz)
				if best < 0 || d < best {
					best, nearest = d, e
				}
			}
		}
	}
	views = append(views, view{"front", nearest})
	var notes []fuFrameNote
	for _, v := range views {
		if v.unit == nil {
			continue
		}
		x, z := px(v.unit)
		b.cam.JumpToBattleViewCenter(x, z)
		img := cl.ComposeFrame()
		name := fmt.Sprintf("%s-t%05d-%s-%s.png", prefix, tick, label, v.name)
		fuWritePNG(t, dir, name, img)
		note := fuFrameNote{Tick: tick, Label: label, View: v.name, File: name, CameraX: b.cam.X, CameraZ: b.cam.Z, UnitsInView: map[string]int{}}
		for _, w := range sess.Units.Iter() {
			if w == nil || !w.Alive || w.Def == nil {
				continue
			}
			sx, sy := b.cam.WorldToScreen(w.X, w.Y, w.Z)
			if sx < 0 || sy < 0 || sx >= int32(retailScreenW) || sy >= int32(retailScreenH) {
				continue
			}
			class := "structure"
			switch {
			case w.Def.CanFly:
				class = "air"
			case fuMobile(sess, w):
				class = "ground"
			}
			note.UnitsInView[fmt.Sprintf("owner%d:%s", w.Owner, class)]++
		}
		notes = append(notes, note)
	}
	return notes
}

// fuFrameRun drives a skirmish through the viewer step one tick at a time and
// composes frames whenever the kill or loss counters move (the first eight
// events) and every 6000 ticks.
func fuFrameRun(t *testing.T, prefix string, cfg session.SkirmishConfig, seed int64, limit uint32) {
	t.Helper()
	root := probeRetail(t)
	opts := Options{Root: root, Map: cfg.MapName, Seed: seed}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(uint32(seed), uint32(seed))
	sess, cat, err := newBattleSessionWithConfig(opts, cs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetModelFS(cs.unmappedMount)
	b, err := composeBattleEntry(sess, cat, cs, cl, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.teardown(cl)
	b.cam.ViewW, b.cam.ViewH = retailScreenW, retailScreenH
	b.cam.Clamp()
	millis := &shotMillisSource{}
	b.millisSource = millis

	dir := fuOutDir(t)
	var notes []fuFrameNote
	var censuses []fuCensus
	lastEvents, eventShots, lastPeriodic := int16(0), 0, uint32(0)
	for step := uint32(1); step <= limit && sess.State != session.StatePostBattle; step++ {
		millis.step = step
		b.viewerStep(1.0/30.0, cl)
		tick := sess.Clock.GlobalTick
		events := fuEvents(sess)
		label := ""
		if events != lastEvents && eventShots < 8 {
			label = fmt.Sprintf("event%02d", events)
			eventShots++
		}
		lastEvents = events
		if period := tick / 6000; period != lastPeriodic {
			lastPeriodic = period
			if label == "" {
				label = "periodic"
			}
		}
		if label == "" {
			continue
		}
		censuses = append(censuses, fuTakeCensus(sess))
		notes = append(notes, fuCaptureViews(t, dir, prefix, b, cl, tick, label)...)
	}
	final := fuTakeCensus(sess)
	fuWriteJSON(t, dir, prefix+".json", map[string]any{
		"mapping": sess.Skirmish.Mapping, "lineofsight": sess.Skirmish.LineOfSight, "players": cfg.NumPlayers,
		"tick": sess.Clock.GlobalTick, "state": sess.State.String(), "result": sess.GetResult(),
		"frames": notes, "censuses": censuses, "final": final,
	})
	t.Logf("%s: ended at tick %d state %s result %q with %d frames (%d event captures)", prefix, sess.Clock.GlobalTick, sess.State.String(), sess.GetResult().Kind, len(notes), eventShots)
	if len(notes) == 0 {
		t.Fatalf("%s: no frame was composed", prefix)
	}
}

// TestFUPlaytestSkirmishBusyFrames composes the busy frames: the default rule
// pair, where the attack wave reaches the local commander, and the Mapped
// pair with two computer sides, where the bases are drawn.
func TestFUPlaytestSkirmishBusyFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("long acceptance trajectory: run tools/check-retail --full")
	}
	fuFrameRun(t, "skirmish-frames-default", session.DirectSkirmishConfig(fuSkirmishMap), 7, 27000)
	cfg := fuMappedRules(fuThreePlayerConfig())
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	fuFrameRun(t, "skirmish-frames-mapped-3p", cfg, 7, 30000)
}
