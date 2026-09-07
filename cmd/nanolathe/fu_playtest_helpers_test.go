//go:build retail

package main

// Shared drivers for the fu-playtest acceptance probes. They read committed
// state and drive the shell through the same seams the windowed loop uses;
// nothing here writes an authoritative field except through the ordinary
// human-command queue [I6].

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/units"
)

// fuOutDirEnv names the directory the probes write their reports and frames
// to. Unset, they write into the test's temporary directory and only log.
const fuOutDirEnv = "NANOLATHE_FU_PLAYTEST_DIR"

func fuOutDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv(fuOutDirEnv); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	return t.TempDir()
}

func fuWriteJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
	return path
}

func fuWritePNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
	return path
}

type fuQueueItem struct {
	Intent   string `json:"intent"`
	Product  string `json:"product,omitempty"`
	Count    uint32 `json:"count,omitempty"`
	Progress uint32 `json:"progress,omitempty"`
	Phase    uint8  `json:"phase"`
}

type fuFactoryRow struct {
	Handle    uint32        `json:"handle"`
	Def       string        `json:"def"`
	Remaining float32       `json:"remaining"`
	Queue     []fuQueueItem `json:"queue"`
}

type fuAIState struct {
	Bound        bool     `json:"bound"`
	Groups       [10]int  `json:"groups"` // 1 resource, 2 wave A, 3 regroup A, 4 construction, 5 null, 6 wave B, 7 regroup B, 8 explore, 9 rally
	Deadlines    []uint32 `json:"deadlines"`
	BuildCapable int32    `json:"build_capable"`
	CountKeys    int      `json:"count_keys"`
	Radius       int32    `json:"radius"`
	CenterX      int32    `json:"center_x"`
	CenterZ      int32    `json:"center_z"`
}

type fuSideCensus struct {
	Player       int            `json:"player"`
	Live         int            `json:"live"`
	Created      uint32         `json:"created"`
	Kills        int16          `json:"kills"`
	Losses       int16          `json:"losses"`
	Air          int            `json:"air"`
	Ground       int            `json:"ground"`
	Structures   int            `json:"structures"`
	Nanoframes   int            `json:"nanoframes"`
	Defs         map[string]int `json:"defs"`
	IntentCounts map[string]int `json:"intent_counts"`
	Factories    []fuFactoryRow `json:"factories"`
	AI           *fuAIState     `json:"ai,omitempty"`
}

type fuCensus struct {
	Tick  uint32         `json:"tick"`
	State string         `json:"state"`
	Sides []fuSideCensus `json:"sides"`
}

// fuMobile reports whether the movement system holds a mover for the unit.
// The unit record's own HasMover word is a save-image mirror, written only by
// the save projection and the restore, so the live query is the one to ask;
// and every stock definition — factories included — compiles canmove as set,
// so the definition word cannot separate a tank from a plant.
func fuMobile(sess *session.Session, u *units.Unit) bool {
	return sess.Movement != nil && sess.Movement.HasMover(u.Handle)
}

// fuTakeCensus reads the committed unit pool, the live order queues and the
// AI managers. Air follows the unit record's canfly word [02 "Unit record"];
// ground versus structure follows the live mover; a factory is a builder
// without a mover; a nanoframe is a unit whose build remainder is still above
// zero [04 §2.3].
func fuTakeCensus(sess *session.Session) fuCensus {
	c := fuCensus{Tick: sess.Clock.GlobalTick, State: sess.State.String()}
	var sides [10]fuSideCensus
	for i := range sides {
		sides[i].Player = i
		sides[i].Defs = map[string]int{}
		sides[i].IntentCounts = map[string]int{}
	}
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Def == nil || int(u.Owner) >= len(sides) {
			continue
		}
		s := &sides[u.Owner]
		s.Live++
		s.Defs[u.Def.UnitName]++
		switch {
		case u.Def.CanFly:
			s.Air++
		case fuMobile(sess, u):
			s.Ground++
		default:
			s.Structures++
		}
		if u.Remaining > 0 {
			s.Nanoframes++
		}
		var items []fuQueueItem
		if q := orders.QueueOfUnit(u); q != nil {
			for _, n := range q.Primary() {
				if n == nil {
					continue
				}
				name := orders.DescriptorFor(n.ID).Name
				s.IntentCounts[name]++
				items = append(items, fuQueueItem{Intent: name, Product: n.BuildDefKey, Count: n.Param2, Progress: n.Param3, Phase: n.Phase})
			}
		}
		if u.Def.Builder && !fuMobile(sess, u) && !u.Def.CanFly && !u.Def.Commander && u.Remaining == 0 {
			s.Factories = append(s.Factories, fuFactoryRow{Handle: uint32(u.Handle), Def: u.Def.UnitName, Remaining: u.Remaining, Queue: items})
		}
	}
	for i := range sides {
		p := &sess.Econ.Players[i]
		if !p.Exists {
			continue
		}
		sides[i].Created = sess.Units.CreatedCountForPlayer(i)
		sides[i].Kills, sides[i].Losses = p.Kills, p.Losses
		if m := sess.AI[i]; m != nil {
			st := &fuAIState{
				Bound:        true,
				Deadlines:    append([]uint32(nil), m.Deadlines[:]...),
				BuildCapable: m.Strategic.BuildCapable,
				CountKeys:    len(m.Strategic.Counts),
				Radius:       m.Strategic.Radius,
				CenterX:      int32(m.Strategic.CenterX >> 16),
				CenterZ:      int32(m.Strategic.CenterZ >> 16),
			}
			st.Groups = [10]int{0, len(m.GroupResource), len(m.GroupWaveA), len(m.GroupRegroupA), len(m.GroupConstruction), len(m.GroupNull), len(m.GroupWaveB), len(m.GroupRegroupB), len(m.GroupExplore), len(m.GroupRally)}
			sides[i].AI = st
		}
		c.Sides = append(c.Sides, sides[i])
	}
	return c
}

// fuAdvance is the displayless stepping loop: the same five-tick budget the
// headless runner hands Session.Step [01 §4.2].
func fuAdvance(sess *session.Session, limit uint32, each func(tick uint32)) {
	scaledNow := sess.Clock.ScaledAnchor
	for sess.State != session.StatePostBattle && sess.Clock.GlobalTick < limit {
		remaining := limit - sess.Clock.GlobalTick
		delta := int32(5)
		if remaining < uint32(delta) {
			delta = int32(remaining)
		}
		scaledNow += delta
		sess.Step(scaledNow)
		if each != nil {
			each(sess.Clock.GlobalTick)
		}
	}
}

func fuUnitsOwnedBy(sess *session.Session, owner uint8) []*units.Unit {
	var out []*units.Unit
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner {
			out = append(out, u)
		}
	}
	return out
}

func fuFindUnit(sess *session.Session, owner uint8, match func(*units.Unit) bool) *units.Unit {
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner && u.Def != nil && match(u) {
			return u
		}
	}
	return nil
}

func fuIsDef(name string) func(*units.Unit) bool {
	return func(u *units.Unit) bool { return strings.EqualFold(u.Def.UnitName, name) }
}

// fuShell drives the retail front end and its battles in-process, the way
// loadgame_test.go does, with a pinned host clock so every viewer step is one
// authoritative tick [01 §4.1].
type fuShell struct {
	t       *testing.T
	shell   *gameShell
	cl      *client.Client
	saveDir string
	millis  *shotMillisSource
}

func fuNewShell(t *testing.T, seed int64) *fuShell {
	t.Helper()
	resetSaveLoadScreenState(t)
	shell, saveDir := retailShellForTest(t)
	shell.opts.Seed = seed
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() {
		if shell.battle != nil {
			shell.teardownBattle(cl)
		}
		clPtr = previous
	})
	return &fuShell{t: t, shell: shell, cl: cl, saveDir: saveDir, millis: &shotMillisSource{}}
}

// waitBattle pumps the loading screen until the composed battle is installed
// or the screen reports a refusal.
func (f *fuShell) waitBattle(what string) *battleSession {
	f.t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		if f.shell.battle != nil {
			f.bind()
			return f.shell.battle
		}
		if modal := f.shell.frontend.Panels.Modal(); modal != nil {
			f.t.Fatalf("%s: refused: %q", what, modal.Message())
		}
		if f.shell.frontend.Mode != modeLoading {
			f.t.Fatalf("%s: left the loading screen for mode %v with no battle installed", what, f.shell.frontend.Mode)
		}
		f.shell.stepLoading(1.0 / 30.0)
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("%s: timed out waiting for the battle", what)
	return nil
}

// bind installs the pinned clock on the active battle before its controller
// is built; the controller captures the source on its first step.
func (f *fuShell) bind() {
	if b := f.shell.battle; b != nil && b.millisSource != f.millis {
		b.millisSource = f.millis
	}
}

func (f *fuShell) step(n int) {
	f.bind()
	for i := 0; i < n; i++ {
		b := f.shell.battle
		if b == nil {
			return
		}
		f.millis.step++
		b.viewerStep(1.0/30.0, f.cl)
	}
}

func (f *fuShell) stepUntil(limit int, cond func(b *battleSession) bool) bool {
	f.bind()
	for i := 0; i < limit; i++ {
		b := f.shell.battle
		if b == nil {
			return false
		}
		if cond(b) {
			return true
		}
		f.millis.step++
		b.viewerStep(1.0/30.0, f.cl)
	}
	b := f.shell.battle
	return b != nil && cond(b)
}

// finishToEndMission pumps the post-battle sequence: the glamour screen waits
// for a key once its fade is done, and ENDMSN is the state the result routes
// from [08 R-CAMP-01 §6].
func (f *fuShell) finishToEndMission(b *battleSession) bool {
	for i := 0; i < 3000; i++ {
		if b.postBattle != nil && b.postBattle.State() == session.PostBattleEndMission {
			return true
		}
		f.millis.step++
		b.viewerStep(1.0/30.0, f.cl)
		if b.postBattle != nil && b.postBattle.State() == session.PostBattleGlamour {
			if b.postBattle.Handle(session.PostBattleControlKey, b.postBattleNow()) {
				b.consumePostBattleEffects(b.postBattleNow(), f.cl)
			}
		}
	}
	return b.postBattle != nil && b.postBattle.State() == session.PostBattleEndMission
}

// loadViaDialog restores a listed slot through the load dialog. It returns
// the refusal message when the load was rejected and the dialog's owner put
// up the authored box; an empty string means the battle was replaced.
func (f *fuShell) loadViaDialog(description string) (string, error) {
	f.t.Helper()
	if err := f.shell.openSaveLoadScreen(loadScreenMode, saveLoadFromBattle); err != nil {
		return "", err
	}
	if saveLoadUI == nil || saveLoadUI.Mode() != loadScreenMode {
		return "", fmt.Errorf("load dialog did not open")
	}
	row := -1
	for i, entry := range saveLoadUI.Entries() {
		if entry.Description == description {
			row = i
			break
		}
	}
	if row < 0 {
		f.shell.activateSaveLoadGadget("CANCEL")
		return "", fmt.Errorf("slot %q is not listed", description)
	}
	f.shell.selectSaveLoadRow(row)
	f.shell.activateSaveLoadGadget("LOAD")
	if modal := f.shell.frontend.Panels.Modal(); modal != nil {
		msg := modal.Message()
		f.shell.frontend.Panels.CloseModal()
		return msg, nil
	}
	f.bind()
	return "", nil
}
