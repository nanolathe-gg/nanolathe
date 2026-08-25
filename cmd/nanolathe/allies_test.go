package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/session"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// six symbols as twelve frames, the odd frame split in half and the even frame
// whole, so the count of configured rows sharing an alliance decides which of
// the pair a row shows.
func TestRetailAllyIconFrame(t *testing.T) {
	g := &gameShell{}
	g.setup.NumPlayers = 4
	g.retailControllersSet = true
	g.retailControllers = [session.SkirmishMaxPlayers]int{1, 2, 2, 0}
	groups := []int{0, 0, 3, 4}
	for i, group := range groups {
		g.setup.Players[i].AllyGroup = group
	}

	cases := []struct {
		slot int
		want int
	}{
		// Rows 0 and 1 are both configured in alliance 0: the joined symbol.
		{0, 0},
		{1, 0},
		// Row 2 is the only configured row in alliance 3: the split symbol.
		{2, 7},
		// Row 3 is Open and alone in alliance 4, so nothing is counted for
		// it at all and it gets the blank frame.
		{3, 10},
	}
	for _, c := range cases {
		if got := g.retailAllyIconFrame(c.slot); got != c.want {
			t.Errorf("retailAllyIconFrame(%d) = %d, want %d", c.slot, got, c.want)
		}
	}

	// The count is over the alliance, not over the row being drawn: an Open
	// row whose alliance a configured row shares still resolves to that
	// alliance's symbol. Its gadget is hidden, so this is never on screen.
	g.setup.Players[3].AllyGroup = 3
	if got := g.retailAllyIconFrame(3); got != 7 {
		t.Errorf("open row in a live alliance = %d, want 7", got)
	}

	// Alliance 5 resolves to frames 10 and 11, both of which are blank.
	g.setup.Players[0].AllyGroup = 5
	g.setup.Players[1].AllyGroup = 5
	if got := g.retailAllyIconFrame(0); got != 10 {
		t.Errorf("shared alliance 5 = %d, want 10", got)
	}
	g.setup.Players[1].AllyGroup = 0
	if got := g.retailAllyIconFrame(0); got != 11 {
		t.Errorf("solo alliance 5 = %d, want 11", got)
	}
}
