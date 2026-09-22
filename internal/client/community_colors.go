package client

// Community nanolathe colours are host presentation state. They remap only
// immutable colour operands already carried by a committed frame and never
// draw from either random stream (community-patch-engine.md "Team-coloured
// nanolathe and nanoframe colours") [I6].

import "strconv"

const (
	communityPlayerColors    = 10
	communityMaxStreamColors = 15
	communityFrameColors     = 16
)

// CommunityColorOptions controls the optional team-coloured nanolathe. Empty
// per-player strings select the source defaults. A non-empty invalid string
// also falls back to that player's corresponding default list, matching the
// source preference contract.
type CommunityColorOptions struct {
	TeamColorNanolathe bool
	PlayerStreamColors [communityPlayerColors]string
	PlayerFrameColors  [communityPlayerColors]string
}

type communityPlayerColorConfig struct {
	stream      [communityMaxStreamColors]uint8
	streamCount uint8
	frame       [communityFrameColors]uint8
}

type communityColorState struct {
	options CommunityColorOptions
	players [communityPlayerColors]communityPlayerColorConfig
}

var communityColorDefaults = [communityPlayerColors]communityPlayerColorConfig{
	{stream: [communityMaxStreamColors]uint8{224, 225, 226, 227, 228, 229}, streamCount: 6, frame: [communityFrameColors]uint8{224, 224, 225, 225, 226, 226, 227, 227, 228, 228, 229, 229, 230, 230, 231, 231}},
	{stream: [communityMaxStreamColors]uint8{249, 201, 202, 203, 204, 205}, streamCount: 6, frame: [communityFrameColors]uint8{201, 201, 201, 202, 202, 203, 203, 204, 204, 205, 205, 206, 206, 207, 207, 207}},
	{stream: [communityMaxStreamColors]uint8{81, 82, 83, 84, 85, 86, 87}, streamCount: 7, frame: [communityFrameColors]uint8{80, 80, 81, 81, 82, 82, 83, 83, 84, 84, 85, 85, 86, 87, 88, 89}},
	{stream: [communityMaxStreamColors]uint8{233, 234, 235, 236, 237, 238}, streamCount: 6, frame: [communityFrameColors]uint8{232, 232, 233, 233, 234, 234, 235, 235, 236, 236, 237, 237, 238, 238, 239, 239}},
	{stream: [communityMaxStreamColors]uint8{103, 104, 105, 106, 107, 108, 109}, streamCount: 7, frame: [communityFrameColors]uint8{103, 103, 104, 104, 105, 105, 106, 106, 107, 107, 108, 108, 109, 109, 110, 111}},
	{stream: [communityMaxStreamColors]uint8{217, 218, 219, 220, 221, 222}, streamCount: 6, frame: [communityFrameColors]uint8{216, 216, 217, 217, 218, 218, 219, 219, 220, 220, 221, 221, 222, 222, 223, 223}},
	{stream: [communityMaxStreamColors]uint8{208, 193, 194, 195, 196, 197}, streamCount: 6, frame: [communityFrameColors]uint8{192, 192, 193, 193, 194, 194, 195, 195, 196, 196, 197, 197, 198, 198, 199, 199}},
	{stream: [communityMaxStreamColors]uint8{89, 90, 91, 92, 93, 94, 95}, streamCount: 7, frame: [communityFrameColors]uint8{88, 88, 89, 89, 90, 90, 91, 91, 92, 92, 93, 93, 94, 94, 95, 95}},
	{stream: [communityMaxStreamColors]uint8{129, 130, 131, 132, 133, 134, 135}, streamCount: 7, frame: [communityFrameColors]uint8{128, 128, 129, 129, 130, 130, 131, 131, 132, 132, 133, 133, 134, 134, 135, 135}},
	{stream: [communityMaxStreamColors]uint8{65, 66, 67, 68, 69, 70, 71}, streamCount: 7, frame: [communityFrameColors]uint8{64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79}},
}

// SetCommunityColorOptions replaces the presentation-only colour preferences.
func (c *Client) SetCommunityColorOptions(options CommunityColorOptions) {
	if c == nil || c.communityColors.options == options {
		return
	}
	c.JoinPreRecord()
	c.communityColors = compileCommunityColors(options)
	c.pausedWorldRevision++
	c.BumpPresentationEpoch()
}

func compileCommunityColors(options CommunityColorOptions) communityColorState {
	state := communityColorState{options: options, players: communityColorDefaults}
	for player := range state.players {
		if text := options.PlayerStreamColors[player]; text != "" {
			if values, ok := parseCommunityColorList(text, communityMaxStreamColors, 0); ok {
				copy(state.players[player].stream[:], values)
				state.players[player].streamCount = uint8(len(values))
			}
		}
		if text := options.PlayerFrameColors[player]; text != "" {
			if values, ok := parseCommunityColorList(text, communityFrameColors, communityFrameColors); ok {
				copy(state.players[player].frame[:], values)
			}
		}
	}
	return state
}

// parseCommunityColorList follows the patch preference grammar: decimal bytes
// separated by commas, spaces and tabs around tokens, and an optional semicolon
// beginning a comment. required is zero for any non-empty count.
func parseCommunityColorList(text string, capacity, required int) ([]uint8, bool) {
	values := make([]uint8, 0, capacity)
	pos := 0
	for {
		for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
			pos++
		}
		if pos == len(text) || text[pos] == ';' {
			break
		}
		if len(values) >= capacity {
			return nil, false
		}
		// The source calls decimal strtol after its explicit space/tab skip;
		// strtol itself accepts the remaining C whitespace before the sign.
		for pos < len(text) && communityDecimalSpace(text[pos]) {
			pos++
		}
		start := pos
		if pos == len(text) {
			return nil, false
		}
		if text[pos] == '+' || text[pos] == '-' {
			pos++
		}
		digits := pos
		for pos < len(text) && text[pos] >= '0' && text[pos] <= '9' {
			pos++
		}
		if digits == pos {
			return nil, false
		}
		value, err := strconv.ParseInt(text[start:pos], 10, 32)
		if err != nil || value < 0 || value > 255 {
			return nil, false
		}
		values = append(values, uint8(value))
		for pos < len(text) && (text[pos] == ' ' || text[pos] == '\t') {
			pos++
		}
		if pos == len(text) || text[pos] == ';' {
			break
		}
		if text[pos] != ',' {
			return nil, false
		}
		pos++
	}
	return values, len(values) > 0 && (required == 0 || len(values) == required)
}

func communityDecimalSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func (c *Client) communityFrameColor(owner uint8, known bool, stock uint8) uint8 {
	if c == nil || !c.communityColors.options.TeamColorNanolathe || !known || owner >= communityPlayerColors || stock < 0xa0 || stock > 0xaf {
		return stock
	}
	return c.communityColors.players[owner].frame[stock-0xa0]
}

func (c *Client) communityStreamColor(owner uint8, known bool, stock, sample uint8, sequence uint32) uint8 {
	if c == nil || !c.communityColors.options.TeamColorNanolathe || !known || owner >= communityPlayerColors {
		return stock
	}
	config := &c.communityColors.players[owner]
	return config.stream[(uint32(sample)+sequence)%uint32(config.streamCount)]
}
