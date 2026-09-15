//go:build retail

package main

import (
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestRetailSpawnChatCreatesUsableUnits(t *testing.T) {
	b, cs, cam, pal := footerShotSession(t)
	defer cs.Close()
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b.cl = cl
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(cam)
	cl.SetPalette(pal)
	cl.SetFNT(b.hud.console)
	cl.SetModelFS(cs.fs)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	for _, name := range []string{"armck", "armsolar"} {
		var created *units.Unit
		var sx, sy int32
		before := len(b.sess.Units.Iter())
		for y := int32(100); y < 400 && created == nil; y += 48 {
			for x := int32(240); x < 600 && created == nil; x += 48 {
				cl.Input().Mouse.SetPosition(float32(x), float32(y))
				b.commitLocalChat("+spawn " + name)
				applyPendingBattleCommands(b)
				if len(b.sess.Units.Iter()) <= before {
					continue
				}
				for _, u := range b.sess.Units.Iter() {
					if u.Def.CanonicalKey == name && u.Owner == b.sess.LocalOwner {
						created = u
						sx, sy = x, y
						break
					}
				}
			}
		}
		if created == nil {
			t.Fatalf("no %s spawned on visible terrain", name)
		}
		if created.Script == nil || created.ScriptState == nil || created.Remaining != 0 || created.Health != created.MaxHealth {
			t.Fatalf("%s not initialized as built", name)
		}
		if name == "armsolar" {
			if _, ok := b.sess.Build.PlacementForProduct(created.Handle); !ok {
				t.Fatal("building yard was not registered")
			}
		} else {
			wx, _, wz := b.cursorWorld(sx, sy)
			if created.X != wx || created.Z != wz {
				t.Fatal("mobile spawn missed the submission point")
			}
		}
		before = len(b.sess.Units.Iter())
		b.commitLocalChat("+spawn " + name)
		applyPendingBattleCommands(b)
		if len(b.sess.Units.Iter()) != before {
			t.Fatal("repeat spawn overwrote occupied site")
		}
		if err := b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{created.Handle}}}); err != nil {
			t.Fatal(err)
		}
		applyPendingBattleCommands(b)
	}
	if path := os.Getenv("NANOLATHE_SPAWN_SHOT"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, cl.ComposeFrame()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
