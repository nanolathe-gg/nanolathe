package aikit

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// wanderBrain orders its first unit to a point drawn from the player's
// private generator on every think, so each batch carries a different
// command and the generator's position records every think.
type wanderBrain struct{ countBrain }

func (b *wanderBrain) Think(k *Kit, o *Obs) {
	b.thinks++
	if len(o.Own) == 0 {
		return
	}
	x, z := 64+int32(k.Rand.Uint32()%512), 64+int32(k.Rand.Uint32()%512)
	k.Move([]pool.Handle{o.Own[0].H}, x, z, false)
}

// holdingProbe holds every asynchronous think on the worker goroutine until
// the simulation thread has begun the step at which its batch is due (its
// StepBegin), and then for a further millisecond, so the worker is still
// thinking when the simulation thread reaches the join whatever the host's
// speed. It counts the joins that found the worker still busy (PartProbe's
// PartJoin). A think due after the run's last tick is released there, and
// one whose host stopped stepping after a long wait.
type holdingProbe struct {
	reaction, end uint32
	reached       atomic.Uint32
	waits         atomic.Uint32
}

func (p *holdingProbe) StepBegin(_ uint8, tick uint32) { p.reached.Store(tick) }

func (p *holdingProbe) ThinkBegin(_ uint8, tick uint32) {
	due := min(tick+p.reaction, p.end)
	for limit := time.Now().Add(30 * time.Second); p.reached.Load() < due && time.Now().Before(limit); {
		time.Sleep(50 * time.Microsecond)
	}
	time.Sleep(time.Millisecond)
}

func (p *holdingProbe) ThinkEnd(uint8, uint32)  {}
func (p *holdingProbe) StepEnd(uint8, uint32)   {}
func (p *holdingProbe) PrepBegin(uint8, uint32) {}
func (p *holdingProbe) PrepEnd(uint8, uint32)   {}
func (p *holdingProbe) Part(_ uint8, _ uint32, part StepPart) {
	if part == PartJoin {
		p.waits.Add(1)
	}
}

// lateTrace is what a tick left behind: the host's cumulative outcomes and
// the unit's head order goal, read on the simulation thread after the step.
type lateTrace struct {
	stats        ApplyStats
	order        orders.ID
	goalX, goalZ numeric.Fixed
}

// A due batch whose worker has not finished makes the simulation thread wait
// for it at the join (Host.joinStep) instead of going on without it, so a
// think held on the worker far past its reaction deadline lands on the same
// tick with the same command as the synchronous host's, whose thinks run on
// the simulation thread: the outcomes and the unit's head order agree tick by
// tick, the generators end at the same position, and the held host's
// simulation thread waited at every deadline it reached
// (docs/DESIGN_GAMEPLAY_RULES.md "The Modern AI controller", "Background
// thinking").
func TestLateWorkerBatchLandsOnItsDeadline(t *testing.T) {
	const ticks = 200
	run := func(probe *holdingProbe) ([]lateTrace, uint64, uint32) {
		t.Helper()
		persona := PersonaMax // thinks every 10 ticks and reacts in 3
		persona.Async = probe != nil
		b := &wanderBrain{}
		f := newGenFixture(t, b)
		f.h = NewHost(f.h.m, b, persona)
		defer f.h.Close()
		if probe != nil {
			f.h.Probe = probe
		}
		trace := make([]lateTrace, 0, ticks)
		for tick := uint32(1); tick <= ticks; tick++ {
			f.h.Step(tick, f.w, f.econ)
			tr := lateTrace{stats: f.h.Stats()}
			if q := orders.QueueOfUnit(f.w.Unit(f.own)); q != nil {
				if n := q.Head(); n != nil {
					tr.order, tr.goalX, tr.goalZ = n.ID, n.GoalX, n.GoalZ
				}
			}
			trace = append(trace, tr)
		}
		gen, _ := f.h.Generator()
		return trace, gen, f.h.Thinks
	}
	prompt, promptGen, thinks := run(nil) // synchronous
	probe := &holdingProbe{reaction: PersonaMax.Reaction, end: ticks}
	late, lateGen, lateThinks := run(probe)
	if thinks == 0 || prompt[ticks-1].stats.Applied == 0 || prompt[ticks-1].order == 0 {
		t.Fatalf("%d thinks applied %+v and left order %d: the fixture does not reach the executor", thinks, prompt[ticks-1].stats, prompt[ticks-1].order)
	}
	for i := range prompt {
		if prompt[i] != late[i] {
			t.Fatalf("tick %d: the late host left %+v, the synchronous one %+v", i+1, late[i], prompt[i])
		}
	}
	if promptGen != lateGen || thinks != lateThinks {
		t.Fatalf("generator %#x after %d synchronous thinks, %#x after %d late", promptGen, thinks, lateGen, lateThinks)
	}
	// The final think's batch may still be pending at the last tick; every
	// other think was joined at its deadline.
	waits := probe.waits.Load()
	if waits+1 < lateThinks {
		t.Fatalf("the simulation thread waited %d times for %d late thinks", waits, lateThinks)
	}
	t.Logf("%d thinks, %d waits, outcomes %+v", lateThinks, waits, late[ticks-1].stats)
}
