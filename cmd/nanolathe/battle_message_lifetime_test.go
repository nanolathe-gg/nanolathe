package main

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Established: the common battle-entry clear also applies to loaded games;
// old source ids must not resolve to a new battle's reused slots
// [08 R-ENTRY-01 §3][07 R-HUD-03 §14.3].
func TestBattleInstallClearsOldMessageSpan(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "restored paused"}[paused], func(t *testing.T) {
			c, err := client.New(client.Options{Width: 160, Height: 80})
			if err != nil {
				t.Fatal(err)
			}
			ring := c.MessageRing()
			ring.Append("previous battle", 1, 7, 10, 48000)
			old := ring.Entries
			buffer := frame.NewBuffer()
			buffer.BeginWrite().Units = []frame.UnitView{{Slot: 7}}
			if err := buffer.Publish(1); err != nil {
				t.Fatal(err)
			}
			b := &battleSession{sess: &session.Session{Snapshot: buffer, Clock: &clock.State{Paused: paused}}, hud: &retailBattleHUD{}}
			installBattleClient(c, b)
			if len(ring.Visible()) != 0 || ring.Producer != 0 || ring.Display != 0 || ring.Entries != old {
				t.Fatal("battle installation did not perform the cursor-only reset")
			}
			alive := func(h pool.Handle) bool {
				_, ok := snapshotUnitByHandle(buffer.Current(), h)
				return ok
			}
			if !alive(7) {
				t.Fatal("fixture did not reuse the prior battle's source handle")
			}
			if _, ok := ring.NextUnvisitedSource(alive); ok {
				t.Fatal("F3 offered an old caption's reused source handle")
			}
			ring.Append("current battle", 1, 7, 10, 1)
			if h, ok := ring.NextUnvisitedSource(alive); !ok || h != 7 {
				t.Fatal("new battle caption could not reach its current source")
			}
		})
	}
}
