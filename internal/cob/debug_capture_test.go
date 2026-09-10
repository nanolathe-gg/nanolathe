package cob

import "testing"

func TestDebugCaptureDetachedSleepingScript(t *testing.T) {
	v := NewVM(&Program{Statics: 1, Pieces: []string{"base"}, Scripts: map[string]int{"Sleep": 0}, Code: []uint32{1}})
	v.statics[0] = 29
	v.Threads[0] = Thread{Status: ThreadSleeping, Sleep: 33, SP: 1}
	v.Threads[0].Stack[0] = 41
	v.anims[0].axes[0].moveTarget = 19
	d := v.DebugSnapshot()
	v.statics[0] = 99
	v.Threads[0].Stack[0] = 88
	v.anims[0].axes[0].moveTarget = 77
	v.prog.Pieces[0] = "changed"
	if d.Statics[0] != 29 || d.Threads[0].Stack[0] != 41 || d.Animations[0][0].MoveTarget != 19 || d.ProgramPieces[0] != "base" {
		t.Fatal("snapshot aliases live script")
	}
}
