//go:build darwin && !ebitenginevmguest

package ebitenapp

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

// qosClassUserInteractive is QOS_CLASS_USER_INTERACTIVE from <sys/qos.h>:
// the class macOS gives work the user is waiting on, which the scheduler
// prefers to place on performance cores.
const qosClassUserInteractive = 0x21

var (
	threadQoSOnce          sync.Once
	pthreadSetQoSClassSelf func(class uint32, relativePriority int32) int32
)

func loadThreadQoS() {
	defer func() { _ = recover() }() // an OS without the call keeps the default class
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&pthreadSetQoSClassSelf, lib, "pthread_set_qos_class_self_np")
}

// RaiseCurrentThread pins the calling goroutine to its OS thread for the rest
// of its life and asks macOS to schedule that thread as user-interactive work.
// On a performance/efficiency core split (the M-series parts) the class is
// what keeps the frame's critical threads — the Cocoa/render thread, the game
// loop, the simulation and the pre-record — off the efficiency cores while
// other processes load the machine. It is host scheduling policy and never
// reaches the client or the simulation [I6].
func RaiseCurrentThread() {
	runtime.LockOSThread()
	threadQoSOnce.Do(loadThreadQoS)
	if pthreadSetQoSClassSelf != nil {
		pthreadSetQoSClassSelf(qosClassUserInteractive, 0)
	}
}
