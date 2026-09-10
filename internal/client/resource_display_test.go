package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// [05 R-ECO-01 §6]: truncate each operand before subtracting, divide toward
// zero, force progress for a nonzero integer gap, then cap the stored single.
func TestDisplayedStockIntegerStep(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		displayed, live, capacity, want float32
	}{
		{"rise", 0, 800, 1000, 100},
		{"live retains low word beyond signed range", 0, 4294967296, 1000, 0},
		{"displayed retains low word beyond signed range", 4294967296, 0, 1000, 0},
		{"fall truncates negative quotient", 100, 1, 1000, 88},
		{"small rise", 1, 8, 1000, 2},
		{"small fall", 8, 1, 1000, 7},
		{"fractional operands", 8.9, 1.1, 1000, 7},
		{"fractional equality loses remainder", 8.9, 8.1, 1000, 8},
		{"negative truncation", -8.9, -1.1, 1000, -7},
		{"cap after step retains fraction", 80, 80, 20.5, 20.5},
		{"equal cap", 20, 20, 20, 20},
		{"cap without lower clamp", 0, 0, -0.5, -0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := easeDisplayedStock(tc.displayed, tc.live, tc.capacity); got != tc.want {
				t.Fatalf("step(%g, %g, %g) = %g, want %g", tc.displayed, tc.live, tc.capacity, got, tc.want)
			}
		})
	}
}

type resourceUIProbe struct{ samples []DisplayedResources }

func (p *resourceUIProbe) DrawUI(c *Client, f UIFrame) {
	p.samples = append(p.samples, f.Resources)
	c.UIFillRect(0, 0, int(f.Resources.Energy), 1, 1)
}

func resourceDisplayClient(t *testing.T) (*Client, *frame.Buffer, *resourceUIProbe) {
	t.Helper()
	buf := frame.NewBuffer()
	publishResourceView(t, buf, 1, 0)
	c, err := New(Options{Width: 640, Height: 480, Buffer: buf})
	if err != nil {
		t.Fatal(err)
	}
	p := &resourceUIProbe{}
	c.SetUIStage(p)
	return c, buf, p
}

func publishResourceView(t *testing.T, buf *frame.Buffer, tick uint32, viewer uint8) {
	t.Helper()
	f := buf.BeginWrite()
	f.ViewingPlayer = viewer
	f.Economy = append(f.Economy,
		frame.EconomyView{Player: 0, Energy: 800, Metal: 80, EnergyCapacity: 1000, MetalCapacity: 1000},
		frame.EconomyView{Player: 1, Energy: 0, Metal: 0, EnergyCapacity: 1000, MetalCapacity: 1000})
	if err := buf.Publish(tick); err != nil {
		t.Fatal(err)
	}
}

func TestResourceDisplayHostCadenceAndBattleLifetime(t *testing.T) {
	c, buf, p := resourceDisplayClient(t)
	original := append([]frame.EconomyView(nil), buf.Current().Economy...)
	if c.displayedResources != (DisplayedResources{}) {
		t.Fatal("battle did not start at zero")
	}
	c.BeginPresentationFrame()
	if c.displayedResources != (DisplayedResources{Energy: 100, Metal: 10}) {
		t.Fatal(c.displayedResources)
	}
	c.ComposeFrame()
	c.ComposeFrameSnapshot()
	c.RecordModernFrame()
	c.RecordModernFrame()
	c.TickPresentationAudio() // An explicit audio-only drain must stay audio-only.
	for _, got := range p.samples {
		if got != (DisplayedResources{Energy: 100, Metal: 10}) {
			t.Fatalf("composition advanced display: %+v", got)
		}
	}
	c.BeginPresentationFrame()
	if c.displayedResources != (DisplayedResources{Energy: 187, Metal: 18}) {
		t.Fatal(c.displayedResources)
	}
	if buf.Current().Tick != 1 || !reflect.DeepEqual(original, buf.Current().Economy) {
		t.Fatal("presentation wrote authoritative state")
	}
	// View changes the publication, not the battle's buffer identity [07 R-HUD-03 §4].
	publishResourceView(t, buf, 2, 1)
	c.SetSnapshot(buf)
	c.BeginPresentationFrame()
	if c.displayedResources != (DisplayedResources{Energy: 164, Metal: 16}) {
		t.Fatalf("View reset the retained pair: %+v", c.displayedResources)
	}
	// An absent viewing row retains the pair, even though other owners exist.
	publishResourceView(t, buf, 3, 9)
	c.BeginPresentationFrame()
	if c.displayedResources != (DisplayedResources{Energy: 164, Metal: 16}) {
		t.Fatalf("absent viewer sampled another owner: %+v", c.displayedResources)
	}
	restored := frame.NewBuffer()
	publishResourceView(t, restored, 4, 0)
	c.SetSnapshot(restored)
	if c.displayedResources != (DisplayedResources{}) {
		t.Fatal("restore retained old display")
	}
	c.BeginPresentationFrame()
	if c.displayedResources != (DisplayedResources{Energy: 100, Metal: 10}) {
		t.Fatal("restore did not ease from zero")
	}
}

func TestResourceDisplayPreRecordPredictsWithoutAdvancing(t *testing.T) {
	c, _, p := resourceDisplayClient(t)
	c.BeginPresentationFrame() // 100 / 10
	for range 2 {              // The second launch discards the first without presenting it.
		c.StartPreRecord(0, 0, false)
		c.JoinPreRecord()
		if c.displayedResources != (DisplayedResources{Energy: 100, Metal: 10}) {
			t.Fatal("speculation advanced retained pair")
		}
		if got := p.samples[len(p.samples)-1]; got != (DisplayedResources{Energy: 187, Metal: 18}) {
			t.Fatalf("prediction = %+v", got)
		}
	}
	c.BeginPresentationFrame()
	list, hit := c.TakePreRecord(c.PresentationDigest(), 0)
	if !hit {
		t.Fatal("normal stock progress invalidated pre-record")
	}
	predicted := list.Clone()
	actual := c.RecordModernFrame().Clone()
	if !reflect.DeepEqual(predicted, actual) {
		t.Fatal("pre-record differs from synchronous presentation")
	}
	c.StartPreRecord(0, 0, false)
	c.JoinPreRecord()
	c.BeginPresentationFrame()
	c.BumpPresentationEpoch()
	if _, hit := c.TakePreRecord(c.PresentationDigest(), 0); hit {
		t.Fatal("forced miss hit")
	}
	c.RecordModernFrame()
	if got := p.samples[len(p.samples)-1]; got != (DisplayedResources{Energy: 263, Metal: 25}) {
		t.Fatalf("miss advanced twice: %+v", got)
	}
	// Consuming without a host advance must reject the next-state prediction.
	c.StartPreRecord(0, 0, false)
	c.JoinPreRecord()
	if _, hit := c.TakePreRecord(c.PresentationDigest(), 0); hit {
		t.Fatal("unadvanced pair accepted next-state list")
	}
	if c.displayedResources != (DisplayedResources{Energy: 263, Metal: 25}) {
		t.Fatal("discard changed pair")
	}
}
