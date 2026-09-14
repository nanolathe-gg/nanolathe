package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

func publishRateView(t *testing.T, buf *frame.Buffer, tick uint32, player uint8, deadline uint32, rate float32) {
	t.Helper()
	f := buf.BeginWrite()
	f.ViewingPlayer = player
	f.Economy = append(f.Economy, frame.EconomyView{
		Player: player, DisplayTimer: deadline,
		Energy: 80, Metal: 8, EnergyCapacity: 1000, MetalCapacity: 100,
		EnergyProduced: rate, EnergyConsumed: rate + 1,
		MetalProduced: rate + 2, MetalConsumed: rate + 3,
	})
	if err := buf.Publish(tick); err != nil {
		t.Fatal(err)
	}
}

func requireDisplayedRates(t *testing.T, c *Client, energy, requested, metal, metalRequested float32) {
	t.Helper()
	r := c.displayedResources
	if r.EnergyProduced != energy || r.EnergyConsumed != requested || r.MetalProduced != metal || r.MetalConsumed != metalRequested {
		t.Fatalf("latched rates=%+v, want %g/%g/%g/%g", r, energy, requested, metal, metalRequested)
	}
}

func TestResourceRateDeadlineStrictnessAndSingleAdvance(t *testing.T) {
	c, buf, _ := resourceDisplayClient(t)
	publishRateView(t, buf, 100, 0, 100, 11.25)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 0, 0, 0, 0) // No eager sample at equality [05 R-ECO-01 §1].
	publishRateView(t, buf, 101, 0, 100, 11.25)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 11.25, 12.25, 13.25, 14.25)
	if c.ResourceDisplayTimers()[0] != 130 {
		t.Fatal("deadline was reseeded from the sample tick")
	}
	publishRateView(t, buf, 130, 0, 100, 20)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 11.25, 12.25, 13.25, 14.25)
	publishRateView(t, buf, 131, 0, 100, 20)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 20, 21, 22, 23)

	// One prior-deadline increment per presented frame, including repeated
	// frames at one committed tick; no catch-up loop [05 R-ECO-01 §6].
	restored := frame.NewBuffer()
	publishRateView(t, restored, 100, 0, 10, 30)
	c.SetSnapshot(restored)
	for _, deadline := range []uint32{40, 70, 100, 100} {
		c.BeginPresentationFrame()
		if got := c.ResourceDisplayTimers()[0]; got != deadline {
			t.Fatalf("overdue deadline=%d, want %d", got, deadline)
		}
	}
	if row := restored.Current().Economy[0]; row.DisplayTimer != 10 || row.Energy != 80 || row.Metal != 8 {
		t.Fatal("presentation changed the committed economy row")
	}
	copy := c.ResourceDisplayTimers()
	copy[0] = 1
	if c.ResourceDisplayTimers()[0] != 100 {
		t.Fatal("save overlay aliases presentation state")
	}
}

func TestResourceRatesRetainLatchAcrossViewAndRestore(t *testing.T) {
	c, buf, _ := resourceDisplayClient(t)
	publishRateView(t, buf, 2, 0, 0, 10)
	c.BeginPresentationFrame()
	publishRateView(t, buf, 3, 1, 60, 40)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 10, 11, 12, 13)
	publishRateView(t, buf, 30, 0, 0, 20)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 10, 11, 12, 13)
	publishRateView(t, buf, 61, 1, 60, 40)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 40, 41, 42, 43)
	if got := c.ResourceDisplayTimers(); !reflect.DeepEqual(got, map[uint8]uint32{0: 30, 1: 90}) {
		t.Fatalf("View deadlines=%v", got)
	}
	// Entry clears displayed stocks but leaves rates intact; the restored
	// deadline, including a future one, replaces the former player's timer
	// [07 R-HUD-03 §4].
	restored := frame.NewBuffer()
	publishRateView(t, restored, 100, 0, 120, 70)
	c.SetSnapshot(restored)
	if c.displayedResources.Energy != 0 || c.displayedResources.Metal != 0 || len(c.ResourceDisplayTimers()) != 0 {
		t.Fatal("restore retained stock or deadline bindings")
	}
	requireDisplayedRates(t, c, 40, 41, 42, 43)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 40, 41, 42, 43)
	publishRateView(t, restored, 121, 0, 120, 70)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 70, 71, 72, 73)
}

func TestResourceRateDeadlineUsesUnsignedWrappedWord(t *testing.T) {
	c, buf, _ := resourceDisplayClient(t)
	publishRateView(t, buf, 2, 0, ^uint32(0)-10, 10)
	c.BeginPresentationFrame()
	requireDisplayedRates(t, c, 0, 0, 0, 0)
	publishRateView(t, buf, ^uint32(0)-1, 0, ^uint32(0)-10, 10)
	c.BeginPresentationFrame()
	if got := c.ResourceDisplayTimers()[0]; got != 19 {
		t.Fatalf("wrapped deadline=%d, want 19", got)
	}
}

func TestResourceRatePredictionAndCompositionDoNotAdvance(t *testing.T) {
	c, buf, probe := resourceDisplayClient(t)
	publishRateView(t, buf, 100, 0, 10, 25)
	for range 2 {
		c.StartPreRecord(0, 0, false)
		c.JoinPreRecord()
		if len(c.ResourceDisplayTimers()) != 0 {
			t.Fatal("speculative recording bound or advanced a deadline")
		}
		requireDisplayedRates(t, c, 0, 0, 0, 0)
		if got := probe.samples[len(probe.samples)-1].EnergyProduced; got != 25 {
			t.Fatalf("prediction rate=%g, want 25", got)
		}
	}
	c.BeginPresentationFrame()
	if _, hit := c.TakePreRecord(c.PresentationDigest(), 0); !hit {
		t.Fatal("predicted rates did not match the presentation boundary")
	}
	for range 2 {
		c.ComposeFrameSnapshot()
		c.RecordModernFrame()
	}
	if got := c.ResourceDisplayTimers()[0]; got != 40 {
		t.Fatalf("composition advanced deadline=%d, want 40", got)
	}
}
