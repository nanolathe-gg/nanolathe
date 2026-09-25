package client

import (
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Asynchronous simulation (docs/DESIGN_GPU_RENDERER.md §13.13).
//
// The window may run the session's sub-ticks on their own goroutine. The
// session then publishes committed frames while this client records and
// presents, so presentation stops reading "the committed frame" and reads a
// pinned pair instead: the host pins the pair a presented frame shows at the
// start of its Draw, and StartPreRecord pins the pair its pre-record will read
// before it wakes the worker. The buffer never refills a pinned slot, so one
// recording pass reads one publication throughout, whatever the simulation
// goroutine publishes meanwhile [I6].
//
// Which pair is presented is the host's decision, made on the game goroutine
// (Options.PresentationTick): the tick the simulation has already finished
// while it computes the next one. A tick that is not published yet — a
// catch-up batch still running — is never waited for; the newest publication
// is presented unblended instead.
//
// Everything here is presentation state. The synchronous path — classic,
// captures, benchmarks, headless — never enables it and reads the buffer as
// before.

// framePin is the committed pair the current presentation pass reads, and the
// buffer that holds it.
type framePin struct {
	buf       *frame.Buffer
	cur, prev *frame.Frame
}

// SetAsyncSimulation tells the client whether the session publishes from
// another goroutine. Enabling widens the committed buffer's rotation so pinned
// slots never block the writer. Disabling first has the host join and stop the
// simulation goroutine (Options.JoinSimulation), then drops the pin, and the
// client reads the buffer directly again.
func (c *Client) SetAsyncSimulation(on bool) {
	if c == nil || c.asyncSim == on {
		return
	}
	if on {
		c.asyncSim = true
		c.buffer.SetConcurrentReaders()
		return
	}
	if c.opts.JoinSimulation != nil {
		c.opts.JoinSimulation()
	}
	c.asyncSim = false
	c.releasePin()
}

// AsyncSimulation reports whether presentation pins what it reads because the
// session publishes from another goroutine.
func (c *Client) AsyncSimulation() bool { return c != nil && c.asyncSim }

// PinPresentation pins the committed pair the next recording pass or presented
// frame reads, replacing the previous pin. It is a no-op unless the
// asynchronous simulation is on.
//
// The pair is the one Options.PresentationTick names. When that tick is not
// held by the buffer — not yet published by a catch-up batch, or the producer
// declines — the newest publication is pinned; for a named tick that is not
// available it is pinned alone, so the pass presents it unblended rather than
// blending toward a tick it cannot see.
func (c *Client) PinPresentation() {
	if c == nil || !c.asyncSim || c.buffer == nil {
		return
	}
	next := framePin{buf: c.buffer}
	target, named := uint32(0), false
	if c.opts.PresentationTick != nil {
		target, named = c.opts.PresentationTick()
	}
	found := false
	if named {
		next.cur, next.prev, found = c.buffer.PinTick(target)
	}
	if !found {
		next.cur, next.prev = c.buffer.PinLatest()
		if named && next.prev != nil {
			c.buffer.Unpin(next.prev)
			next.prev = nil
		}
	}
	// Pin before release: a slot both pins name stays held throughout.
	c.releasePin()
	c.pin = next
}

// releasePin drops the current pin, if any.
func (c *Client) releasePin() {
	if c.pin.buf != nil {
		c.pin.buf.Unpin(c.pin.cur)
		c.pin.buf.Unpin(c.pin.prev)
	}
	c.pin = framePin{}
}

// committedFrame is the committed frame presentation reads: the pinned one
// under the asynchronous simulation, the buffer's current publication
// otherwise.
func (c *Client) committedFrame() *frame.Frame {
	if c.pin.buf != nil && c.pin.buf == c.buffer {
		return c.pin.cur
	}
	if c.buffer == nil {
		return nil
	}
	return c.buffer.Current()
}

// PresentedFrame is the committed frame the current presentation pass reads:
// the pinned publication under the asynchronous simulation, the buffer's
// current one otherwise. Host code that draws — the battle HUD's world
// overlays — reads this rather than the buffer, so it agrees with the world
// it is drawn over and never reads a publication the simulation goroutine is
// about to supersede.
func (c *Client) PresentedFrame() *frame.Frame {
	if c == nil {
		return nil
	}
	return c.committedFrame()
}

// observesInOrder reports that committed-frame observers are fed every
// publication in order by the host, so a recording pass that reads an older
// pinned tick must leave their state alone.
func (c *Client) observesInOrder() bool { return c.asyncSim }

// ObserveCommittedFrame lays what one publication adds to the Enhanced
// history layers — trails, scorch marks, hover wakes, water motion — exactly as
// ObserveCommittedTick does for the current one. The asynchronous host calls
// it for every publication the simulation goroutine made, oldest first, after
// joining it (§13.13).
func (c *Client) ObserveCommittedFrame(f *frame.Frame) {
	if c == nil || f == nil {
		return
	}
	if c.effects.Marks {
		c.placeTrails(f)
		c.observeScorchMarks(f)
	}
	if c.effects.Water {
		c.placeSurfaceWakes(f)
		c.observeWaterMotion(f)
	}
}

// deferredCaption is one caption held by DeferCaptions.
type deferredCaption struct {
	line string
	unit pool.Handle
	tick uint32
}

// DeferCaptions holds the captions the audio queue resolves while a batch runs
// on the simulation goroutine. A sub-tick's audio insert can resolve a full
// queue's last entry silently, and that resolve posts its caption to the
// message ring — which recording passes read meanwhile. The host turns the
// deferral on as it launches a batch and off when it joins it; turning it off
// appends the held captions to the ring in the order they were resolved,
// before anything else the host does at the join (§13.13).
func (c *Client) DeferCaptions(on bool) {
	if c == nil {
		return
	}
	c.captionsDeferred = on
	if on {
		return
	}
	for i, d := range c.deferredCaptions {
		c.messages.Append(d.line, 1, d.unit, 10, d.tick)
		c.deferredCaptions[i] = deferredCaption{}
	}
	c.deferredCaptions = c.deferredCaptions[:0]
}

// SetWorkerThreadSetup installs a function the pre-record goroutine runs once
// when it starts, before its first record. The window uses it to raise that
// thread's scheduling class; the client itself never touches the platform.
func (c *Client) SetWorkerThreadSetup(setup func()) {
	if c != nil {
		c.workerThreadSetup = setup
	}
}

// NoteSimulationTime records the wall time a batch spent on the simulation
// goroutine, for the host's frame graph; it runs beside presentation rather
// than inside any Draw.
func (c *Client) NoteSimulationTime(d time.Duration) {
	if c != nil {
		c.simulationTime += int64(d)
	}
}

// TakeSimulationTime returns the simulation time noted since the last call and
// resets it.
func (c *Client) TakeSimulationTime() time.Duration {
	if c == nil {
		return 0
	}
	d := time.Duration(c.simulationTime)
	c.simulationTime = 0
	return d
}
