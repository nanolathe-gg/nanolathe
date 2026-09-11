package ebitenapp

import (
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The deferred update may switch executors inside a modern Draw. No worker
// may then race the following classic Present (DESIGN_GPU_RENDERER §13.10).
func TestLifecycleF10TailStopsPreRecord(t *testing.T) {
	c, err := client.New(client.Options{Width: 32, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	a := &app{c: c, mode: RendererModern}
	now := time.Now()
	a.updatedAt = now
	a.pipe.hasTick = true
	a.pipe.tickAt = now.Add(-time.Second / 30)
	a.pipe.tick16 = 0
	c.RequestRendererToggle()
	a.serviceRendererRequest()
	a.launchPreRecord(now, now, time.Second/120, 0)
	c.JoinPreRecord()
	defer c.CancelPreRecord()
	_, _, launches := c.PreRecordCounts()
	if launches != 0 || a.pipe.armed {
		t.Fatalf("classic transition launched worker: launches=%d armed=%v", launches, a.pipe.armed)
	}
}

type lifecycleBlockingUI struct{ entered, release chan struct{} }

func (s lifecycleBlockingUI) DrawUI(*client.Client, client.UIFrame) {
	close(s.entered)
	<-s.release
}

func TestLifecycleClassicDrawJoinsRecorder(t *testing.T) {
	c, err := client.New(client.Options{Width: 32, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	stage := lifecycleBlockingUI{make(chan struct{}), make(chan struct{})}
	c.SetUIStage(stage)
	c.StartPreRecord(0, 0, false)
	<-stage.entered
	a := &app{c: c, mode: RendererClassic}
	done := make(chan struct{})
	go func() { a.beginDraw(); close(done) }()
	early := false
	select {
	case <-done:
		early = true
	case <-time.After(20 * time.Millisecond):
	}
	close(stage.release)
	<-done
	_, retained := c.TakePreRecord(c.PresentationDigest(), 0)
	c.CancelPreRecord()
	if retained {
		t.Error("classic Draw left a speculative record pending")
	}
	if early {
		t.Fatal("classic Draw passed the barrier while recording still owned client state")
	}
}

func TestLifecycleTerrainTransitionsInvalidatePausedSourcesOnce(t *testing.T) {
	c, err := client.New(client.Options{Width: 32, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	a := &app{c: c, mode: RendererClassic}
	for _, terrain := range []*world.Terrain{{}, nil, {}} {
		c.SetTerrain(terrain)
		a.paused.valid = true
		a.pipe.armed = true
		a.syncRendererSources()
		if a.sourceGeneration != c.TerrainGeneration() || a.paused.valid || a.pipe.armed {
			t.Fatal("terrain transition retained previous presentation")
		}
		a.paused.valid = true
		a.syncRendererSources()
		if !a.paused.valid {
			t.Fatal("unchanged terrain reset its warm presentation")
		}
	}
}

// Cancelling at the swap discards the saved speculative CRT snapshot before
// any classic frame can advance it (DESIGN_GPU_RENDERER §13.10).
func TestLifecycleExecutorSwapDiscardsPendingRecord(t *testing.T) {
	c, err := client.New(client.Options{Width: 32, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	c.StartPreRecord(0, 0, false)
	c.JoinPreRecord()
	a := &app{c: c, mode: RendererModern}
	c.RequestRendererToggle()
	a.serviceRendererRequest()
	if _, hit := c.TakePreRecord(c.PresentationDigest(), 0); hit {
		t.Fatal("executor swap retained speculative record")
	}
	_, misses, _ := c.PreRecordCounts()
	if misses != 0 {
		t.Fatal("executor swap deferred discard until a later digest miss")
	}
}
