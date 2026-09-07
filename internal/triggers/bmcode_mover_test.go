package triggers

import "testing"

func TestMobileTriggerCountUsesMoverByte(t *testing.T) {
	w := triggerWorld(t)
	for _, value := range []uint8{0, 1, 2, 255} {
		u := spawn(t, w, "fixture", 1, 0, 0)
		u.Def.BMCode = value
	}
	if got := countMatching(pollCtx(w, 0), 1, true, nil, false); got != 1 {
		t.Fatalf("mobile count=%d, want only byte one", got)
	}
}
