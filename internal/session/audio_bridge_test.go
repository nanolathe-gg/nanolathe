package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// allVisibleAudioService supplies the explicit, valid visibility dependency
// needed by tests whose subject is audio playback rather than LOS. Mode zero
// initializes the in-map word mask with every player bit set.
func allVisibleAudioService() *visibility.Service {
	return visibility.New(&world.Terrain{CellW: 64, CellH: 64}, 0)
}

func TestInstallAudioBridge_MapsHitSound(t *testing.T) {
	s := &Session{}
	s.AudioQueue = audio.NewQueue()
	s.AudioQueue.Seed(1)
	s.AudioCache = audio.NewCache(nil)
	s.Vis = allVisibleAudioService()
	// preload a dummy sample for the hit alias
	_, _ = s.AudioCache.Put("hit_alias", []byte{128, 128})
	s.Combat = &combat.Service{}
	called := false
	s.Combat.Events = func(ev combat.Event) { called = true }
	// install bridge (wraps previous)
	s.InstallAudioBridge()
	if s.Combat.Events == nil {
		t.Fatal("bridge not installed")
	}
	// headless backend to record plays
	be := audio.NewBackend(true)
	old := audio.GlobalBackend()
	audio.SetGlobalBackend(be)
	defer audio.SetGlobalBackend(old)
	// emit hit sound via combat
	ev := combat.Event{Kind: combat.EventHitSound, Sound: "hit_alias", Position: combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}}
	s.Combat.Events(ev)
	if !called {
		t.Fatal("previous sink not called")
	}
	if be.PlayCount() != 1 {
		t.Fatalf("hit sound not played via bridge, count %d want 1 aliases %v", be.PlayCount(), be.PlayedAliases())
	}
	if be.PlayedAliases()[0] != "hit_alias" {
		t.Fatalf("alias %q want hit_alias", be.PlayedAliases()[0])
	}
	// also test water sound maps
	be.Reset()
	ev2 := combat.Event{Kind: combat.EventWaterSound, Sound: "hit_alias", Position: combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}}
	s.Combat.Events(ev2)
	if be.PlayCount() != 1 {
		t.Fatalf("water sound count %d want 1", be.PlayCount())
	}
}

func TestInstallAudioBridge_PreservesQueueArbitration(t *testing.T) {
	audio.ResetCooldowns()
	s := &Session{}
	s.AudioQueue = audio.NewQueue()
	s.AudioQueue.Seed(1)
	s.AudioQueue.Configure(10, 10, true, true)
	s.AudioCache = audio.NewCache(nil)
	s.Combat = &combat.Service{}
	s.InstallAudioBridge()
	// queue ordering should remain descending priority after bridge
	// Insert two cues via EmitSound helpers (which use InsertAt)
	s.EmitSound(audio.SlotWorking, 1, "") // pri 2
	s.EmitSound(audio.SlotCant, 2, "")    // pri 8
	if s.AudioQueue.Count != 2 {
		t.Fatalf("count %d want 2", s.AudioQueue.Count)
	}
	if s.AudioQueue.Entries[0].Slot != audio.SlotCant {
		t.Fatalf("high priority not first after bridge, got %d", s.AudioQueue.Entries[0].Slot)
	}
	// equal priority FIFO
	audio.ResetCooldowns()
	q2 := audio.NewQueue()
	s.AudioQueue = q2
	s.InstallAudioBridge() // re-wrap, should not break
	// need fresh combat service for second queue? just test queue directly
	q2.Seed(1)
	if !q2.InsertAt(0, audio.SlotActivate, 1, "") {
		t.Fatal("insert activate")
	}
	if !q2.InsertAt(0, audio.SlotDeactivate, 1, "") {
		t.Fatal("deactivate")
	}
	if q2.Entries[0].Slot != audio.SlotActivate && q2.Entries[1].Slot != audio.SlotDeactivate {
		// activate pri4, deactivate pri4 => FIFO after equals, so activate first, deactivate second
		// but our insert order was activate then deactivate, so expect activate first
		// if reversed, fail
	}
	if q2.Entries[0].Slot != audio.SlotActivate || q2.Entries[1].Slot != audio.SlotDeactivate {
		t.Fatalf("FIFO after equals broken %v", []audio.Slot{q2.Entries[0].Slot, q2.Entries[1].Slot})
	}
}

func TestEmitPositionalHeadlessNoDevice(t *testing.T) {
	s := &Session{}
	s.AudioQueue = audio.NewQueue()
	s.AudioCache = audio.NewCache(nil)
	s.Vis = allVisibleAudioService()
	_, _ = s.AudioCache.Put("pos_alias", []byte{128, 128})
	be := audio.NewBackend(true)
	old := audio.GlobalBackend()
	audio.SetGlobalBackend(be)
	defer audio.SetGlobalBackend(old)
	pos := [3]numeric.Fixed{0, 0, 0}
	pan, vol, ok := s.EmitPositional("pos_alias", pos)
	if !ok {
		t.Fatal("positional should be audible with explicit visibility")
	}
	if be.PlayCount() != 1 {
		t.Fatalf("positional play count %d want 1 pan %v vol %d aliases %v", be.PlayCount(), pan, vol, be.PlayedAliases())
	}
	// headless should not have context
	if be.IsHeadless() == false {
		t.Fatal("headless backend should be headless")
	}
}

func TestEmitPositionalRequiresVisibility(t *testing.T) {
	s := &Session{AudioCache: audio.NewCache(nil)}
	_, _ = s.AudioCache.Put("pos_alias", []byte{128, 128})
	be := audio.NewBackend(true)
	old := audio.GlobalBackend()
	audio.SetGlobalBackend(be)
	defer audio.SetGlobalBackend(old)

	if _, _, ok := s.EmitPositional("pos_alias", [3]numeric.Fixed{}); ok {
		t.Fatal("positional audio should fail closed without visibility")
	}
	if be.PlayCount() != 0 {
		t.Fatalf("positional audio without visibility played %d times", be.PlayCount())
	}

	// A zero-dimension service is not a valid audience grid and must also fail
	// closed rather than acting as an always-audible fixture.
	s.Vis = visibility.New(&world.Terrain{CellW: 1, CellH: 1}, 0)
	if _, _, ok := s.EmitPositional("pos_alias", [3]numeric.Fixed{}); ok {
		t.Fatal("positional audio should fail closed with invalid visibility dimensions")
	}
}

func TestClientTickAudioDrainsQueue(t *testing.T) {
	// Verify that presentation TickAudio drains once per frame and respects 30-frame window [03 §8.3] C18
	audio.ResetCooldowns()
	q := audio.NewQueue()
	q.Seed(1)
	q.Configure(10, 10, true, true)
	cat := &audio.Category{Name: "test"}
	cat.Rows[1].Variants = []string{"sel"}
	cat.Rows[7].Variants = []string{"cant"}
	// register dummy unit
	q.Register(1, cat, "U", true)
	q.Register(2, cat, "U", true)
	// Insert two cues at same frame, sorted sel (10) then cant (8)
	if !q.InsertAt(100, audio.SlotSelect, 1, "") {
		t.Fatal("insert select")
	}
	if !q.InsertAt(100, audio.SlotCant, 2, "") {
		t.Fatal("insert cant")
	}
	// headless backend to capture plays
	be := audio.NewBackend(true)
	audio.SetGlobalBackend(be)
	defer audio.SetGlobalBackend(nil)
	var plays []string
	q.OnPlay(func(alias string, slot audio.Slot, unit pool.Handle) {
		plays = append(plays, alias)
		_ = be.PlayAlias(alias, nil, 1.0, 0)
	})
	q.Drain(100) // audible sel: BaseTime 100, plays sel
	if len(plays) != 1 || plays[0] != "sel" {
		t.Fatalf("first drain plays %v want sel", plays)
	}
	if q.Count != 1 {
		t.Fatalf("after first drain count %d want 1", q.Count)
	}
	q.Drain(110) // within 30 of BaseTime (100+30=130), silent cant
	if len(plays) != 1 {
		t.Fatalf("silent drain should not play, plays %v", plays)
	}
	if q.Count != 0 {
		t.Fatalf("after silent drain count %d want 0", q.Count)
	}
	// Insert another cue after window, should be audible again
	if !q.InsertAt(200, audio.SlotCant, 2, "") {
		t.Fatal("insert cant after")
	}
	q.Drain(200) // 200 >=130 audible
	if len(plays) != 2 || plays[1] != "cant" {
		t.Fatalf("second audible plays %v want cant", plays)
	}
}
