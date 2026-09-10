package orders

import "testing"

func TestDebugCaptureQueueIsDetached(t *testing.T) {
	q := &Queue{primary: []*Node{{Param1: 17, Phase: 2}}, secondary: []*Node{{Param2: 3}}, diagnostics: []string{"pending"}, lastPumpTick: 31, secondaryTick: 29}
	d := q.DebugSnapshot(4)
	q.primary[0].Param1 = 19
	q.secondary[0].Param2 = 8
	q.diagnostics[0] = "changed"
	if d.Queue.Primary[0].Param1 != 17 || d.Queue.Secondary[0].Param2 != 3 || d.Diagnostics[0] != "pending" || d.LastPumpTick != 31 || d.SecondaryTick != 29 {
		t.Fatal("queue snapshot aliases live state")
	}
}
