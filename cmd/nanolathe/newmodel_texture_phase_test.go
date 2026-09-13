package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type battlePhase7Counter struct{ calls int }

func (p *battlePhase7Counter) StepPhase7() { p.calls++ }

// The controller may receive nil while the shell changes renderers or clients.
// It must not replace the battle-installed phase-7 owner with that client
// value; every catch-up sub-tick still reaches the installed service.
func TestBattleControllerKeepsInstalledPhase7ServiceAcrossClientChanges(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.State = session.StateBattle
	probe := &battlePhase7Counter{}
	b.sess.SetPhase7Service(probe)
	source := &fakeMillisSource{}
	controller := NewBattleController(b, source)
	controller.Step(BattleInputFrame{}, nil) // establish the clock anchor
	source.ms = 1000
	controller.Step(BattleInputFrame{}, nil) // five runnable sub-ticks after clamp
	if got := probe.calls; got != 5 {
		t.Fatalf("phase-7 calls without client = %d, want 5", got)
	}
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	source.ms = 2000
	controller.Step(BattleInputFrame{}, cl)
	if got := probe.calls; got != 10 {
		t.Fatalf("phase-7 calls after client replacement = %d, want 10", got)
	}
}

func TestHeadlessTextureRegistryInstallsWithoutClient(t *testing.T) {
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	sess := &session.Session{Catalog: &content.Catalog{}}
	registry, err := installHeadlessModelTextureRegistry(sess, fs)
	if err != nil {
		t.Fatal(err)
	}
	if registry == nil {
		t.Fatal("headless setup did not construct the battle registry")
	}
	var _ session.Phase7Service = registry
}
