package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestParseCommunityColorList(t *testing.T) {
	for _, tc := range []struct {
		name               string
		text               string
		capacity, required int
		want               []uint8
		ok                 bool
	}{
		{"stream", " 1,\t22,+255 ; ignored,999", 15, 0, []uint8{1, 22, 255}, true},
		{"trailing comma", "4,5,", 15, 0, []uint8{4, 5}, true},
		{"exact frame", "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15", 16, 16, []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, true},
		{"empty", " ; comment", 15, 0, []uint8{}, false},
		{"missing comma", "1 2", 15, 0, nil, false},
		{"negative", "-1", 15, 0, nil, false},
		{"too large", "256", 15, 0, nil, false},
		{"too many", "1,2,3", 2, 0, nil, false},
		{"short frame", "1,2", 16, 16, []uint8{1, 2}, false},
		{"decimal parser whitespace", "1,\n2", 15, 0, []uint8{1, 2}, true},
		{"newline after value is not separator whitespace", "1\n,2", 15, 0, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCommunityColorList(tc.text, tc.capacity, tc.required)
			if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parse = (%v,%t), want (%v,%t)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCommunityColorDefaultsOverridesAndFallback(t *testing.T) {
	options := CommunityColorOptions{TeamColorNanolathe: true}
	options.PlayerStreamColors[0] = "9,10"
	options.PlayerFrameColors[0] = "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15"
	options.PlayerStreamColors[1] = "256" // invalid: retain player 2 default
	options.PlayerFrameColors[1] = "1,2"  // invalid: retain player 2 default
	state := compileCommunityColors(options)

	if got := state.players[0].stream[:state.players[0].streamCount]; !reflect.DeepEqual(got, []uint8{9, 10}) {
		t.Fatalf("stream override = %v", got)
	}
	for i, got := range state.players[0].frame {
		if got != uint8(i) {
			t.Fatalf("frame override[%d] = %d", i, got)
		}
	}
	if got := state.players[1]; got != communityColorDefaults[1] {
		t.Fatalf("invalid player 2 lists did not fall back independently: %+v", got)
	}
	if got := state.players[9].frame; got != ([16]uint8{64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79}) {
		t.Fatalf("player 10 frame default = %v", got)
	}
}

func TestCommunityColorMappingGatesAndSequence(t *testing.T) {
	c := &Client{}
	if got := c.communityFrameColor(0, true, 0xa4); got != 0xa4 {
		t.Fatalf("disabled frame = %#x", got)
	}
	if got := c.communityStreamColor(0, true, 0xa3, 2, 5); got != 0xa3 {
		t.Fatalf("disabled stream = %#x", got)
	}

	options := CommunityColorOptions{TeamColorNanolathe: true}
	options.PlayerStreamColors[0] = "40,41,42"
	options.PlayerFrameColors[0] = "80,81,82,83,84,85,86,87,88,89,90,91,92,93,94,95"
	c.communityColors = compileCommunityColors(options)
	if got := c.communityFrameColor(0, true, 0xa4); got != 84 {
		t.Fatalf("mapped frame = %d, want 84", got)
	}
	if got := c.communityStreamColor(0, true, 0xa3, 2, 5); got != 41 {
		t.Fatalf("mapped stream = %d, want (2+5) mod 3 = 1 => 41", got)
	}
	if got := c.communityStreamColor(0, true, 0xa3, 2, 5); got != 41 {
		t.Fatalf("mapping retained a draw-time cursor: second result = %d", got)
	}
	for _, tc := range []struct {
		name         string
		owner, stock uint8
		known        bool
	}{
		{"unknown owner", 0, 0xa6, false},
		{"owner outside ten colours", 10, 0xa6, true},
		{"byte below frame ramp", 0, 0x9f, true},
		{"byte above frame ramp", 0, 0xb0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.communityFrameColor(tc.owner, tc.known, tc.stock); got != tc.stock {
				t.Fatalf("frame = %#x, want unchanged %#x", got, tc.stock)
			}
		})
	}
}

// This windowless software capture verifies the visible switch and configured
// stream override at the shared draw-list boundary. Classic replay sees the
// exact Fill index Enhanced receives from the same command.
func TestCommunityStreamColorSoftwareCapture(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		want    uint8
	}{
		{"disabled stock", false, 0xa3},
		{"enabled override", true, 41},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := stripTestClient(t)
			options := CommunityColorOptions{TeamColorNanolathe: tc.enabled}
			options.PlayerStreamColors[0] = "40,41,42"
			c.communityColors = compileCommunityColors(options)
			v := frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa3,
				NanoOwnerColor: 0, NanoOwnerColorKnown: true, ColorSample: 2, ColorSequence: 5,
				X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20)}
			c.drawStripBarrier(stripTestFrame(true, v), 6)
			c.replayForTest()
			for y := 20; y < 22; y++ {
				for x := 20; x < 22; x++ {
					if got := c.indexed[y*c.width+x]; got != tc.want {
						t.Fatalf("capture pixel (%d,%d) = %d, want %d", x, y, got, tc.want)
					}
				}
			}
		})
	}
}

func TestUnitNanoframeMapsBeforeBothRenderers(t *testing.T) {
	v := frame.UnitView{Slot: 7, BuildRemaining: 0.5, OwnerColor: 0, OwnerColorKnown: true}
	plain := &Client{frameTick: 13}
	stock, stockOutline := plain.unitNanoframeReveal(v)
	if stock == nil {
		t.Fatal("unfinished unit has no stock reveal")
	}

	options := CommunityColorOptions{TeamColorNanolathe: true}
	options.PlayerFrameColors[0] = "80,81,82,83,84,85,86,87,88,89,90,91,92,93,94,95"
	coloured := &Client{frameTick: 13, communityColors: compileCommunityColors(options)}
	got, gotOutline := coloured.unitNanoframeReveal(v)
	mapVerdict := func(value int16) int16 {
		if value >= 0xa0 && value <= 0xaf {
			return value - 0xa0 + 80
		}
		return value
	}
	if got == nil || got.Line != stock.Line || got.Floor != stock.Floor ||
		got.Below != mapVerdict(stock.Below) || got.Band != mapVerdict(stock.Band) || got.Above != mapVerdict(stock.Above) ||
		gotOutline != uint8(mapVerdict(int16(stockOutline))) {
		t.Fatalf("mapped reveal = %+v/%d, stock = %+v/%d", got, gotOutline, stock, stockOutline)
	}
}

func TestCommunityNanoOverridesEnhancedRamp(t *testing.T) {
	c, v := teamNanoFixture()
	options := CommunityColorOptions{TeamColorNanolathe: true}
	options.PlayerStreamColors[0] = "90,91,92"
	c.SetCommunityColorOptions(options)
	v.ColorSample, v.ColorSequence = 2, 5
	index, ramp, colored := c.nanoParticleColor(v)
	if index != 91 || !colored || ramp != [7]uint8{91, 91, 91, 91, 91, 91, 91} {
		t.Fatalf("Community color/light=%d %v %v", index, ramp, colored)
	}
	c.SetCommunityColorOptions(CommunityColorOptions{})
	index, _, colored = c.nanoParticleColor(v)
	if index != 40 || !colored {
		t.Fatalf("Enhanced fallback=%d %v", index, colored)
	}
}
