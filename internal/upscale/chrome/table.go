package chrome

import "strings"

// element draws one covered entry's frames. Specs are side-independent: an
// entry is matched by the longest suffix after its side prefix, so CORMOVE and
// ARMMOVE share MOVE.
type element struct {
	draw func(s *Style, entry string, frame, width int) *Layer
}

// The captions are the words the stock art shows; a frame whose art differs
// never reaches its spec (Bank2x), so a modded caption is never overwritten.

func button(caption string) element {
	return element{draw: func(s *Style, entry string, frame, width int) *Layer {
		return s.Button(width, 30, 10, caption, State(min(frame, 2)), entry)
	}}
}

func tab(caption string) element {
	return element{draw: func(s *Style, entry string, frame, width int) *Layer {
		return s.Button(width, 18, 5, caption, State(min(frame, 2)), entry)
	}}
}

func arrow(next bool) element {
	return element{draw: func(s *Style, entry string, frame, width int) *Layer {
		return s.PageArrow(next, State(min(frame, 2)))
	}}
}

// toggle is a selector whose first len(stages) frames light one stage each,
// then a mixed frame captioned summary with every light half lit, then blank
// pressed and greyed frames. A summary of "" means the art has no mixed frame.
func toggle(summary string, stages ...string) element {
	return element{draw: func(s *Style, entry string, frame, width int) *Layer {
		lights := make([]led, len(stages))
		text, st := "", Normal
		switch {
		case frame < len(stages):
			lights[frame], text = ledOn, stages[frame]
		case summary != "" && frame == len(stages):
			for i := range lights {
				lights[i] = ledMixed
			}
			text = summary
		default:
			rest := frame - len(stages)
			if summary != "" {
				rest--
			}
			st = Pressed
			if rest > 0 {
				st = Greyed
			}
		}
		return s.Toggle(width, entry, text, lights, st)
	}}
}

var topBar = element{draw: func(s *Style, entry string, frame, width int) *Layer { return s.TopBar() }}

// suffixes maps the part of a stock entry name after its side prefix to its
// element.
var suffixes = map[string]element{
	"MOVE":     button("MOVE"),
	"STOP":     button("STOP"),
	"ATTACK":   button("ATTACK"),
	"PATROL":   button("PATROL"),
	"DEFEND":   button("GUARD"),
	"REPAIR":   button("REPAIR"),
	"RECLAIM":  button("RECLAIM"),
	"CAPTURE":  button("CAPTURE"),
	"LOAD":     button("LOAD"),
	"UNLOAD":   button("UNLOAD"),
	"BLAST":    button("D-GUN"),
	"SPECIAL":  button("SPECIAL"),
	"BUILD":    tab("BUILD"),
	"ORDERS":   tab("ORDERS"),
	"FIREORD":  toggle("FIRE ORDERS", "HOLD FIRE", "RETURN FIRE", "FIRE AT WILL"),
	"MOVEORD":  toggle("MOVE ORDERS", "HOLD POSITION", "MANEUVER", "ROAM"),
	"ONOFF":    toggle("OFF/ON ORDERS", "OFF", "ON"),
	"CLOAK":    toggle("CLOAK ORDERS", "VISIBLE", "CLOAKED"),
	"ACTIVATE": toggle("", "ACTIVATE", "DEACTIVATE"),
	"PREV":     arrow(false),
	"NEXT":     arrow(true),
}

// lookup finds an entry's element: PANELTOP by name, buttons by the longest
// matching suffix behind a non-empty side prefix, so UNLOAD wins over LOAD.
func lookup(name string) (element, bool) {
	upper := strings.ToUpper(name)
	if upper == "PANELTOP" {
		return topBar, true
	}
	best, found := "", false
	for suffix := range suffixes { // longest match wins, so order is irrelevant
		if len(upper) > len(suffix) && strings.HasSuffix(upper, suffix) && len(suffix) > len(best) {
			best, found = suffix, true
		}
	}
	return suffixes[best], found
}
