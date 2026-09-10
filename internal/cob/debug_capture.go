package cob

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/model"
)

// DebugState is detached engine runtime state, not COB bytecode or a save image.
type DebugState struct {
	Threads                         [8]Thread
	ThreadIdentities                [8]uint64
	CompletionReceiverPresent       [8]bool
	LastReturnValues                [8]int32
	LastReturnValid                 [8]bool
	LastReturnIdentities            [8]uint64
	LastStarted, LastQueryThread    int
	ActiveThreads                   uint32
	Statics                         []int32
	Pieces                          []model.PieceState
	Animations                      [][3]DebugAxis
	PieceBusy                       []bool
	PieceFlags                      []uint8
	Diagnostics                     []string
	ProgramFunctions, ProgramPieces []string
	ProgramCodeWords                int
	ProgramChecksum                 uint32
	ScriptEntryWords                []int
	TickDenominator                 int32
	Dirty                           bool
}
type DebugAxis struct {
	MoveTarget, MoveSpeed                   int32
	MoveBusy                                bool
	TurnTarget                              uint16
	TurnSpeed                               int32
	TurnBusy                                bool
	SpinSpeed, SpinTarget, SpinAcceleration int32
	SpinActive                              bool
}

func (v *VM) DebugSnapshot() *DebugState {
	if v == nil {
		return nil
	}
	d := &DebugState{Threads: v.Threads, ThreadIdentities: v.threadIdentity, LastReturnValues: v.lastReturnValue, LastReturnValid: v.lastReturnValid, LastReturnIdentities: v.lastReturnIdentity, LastStarted: v.lastStarted, LastQueryThread: v.lastQueryThread, ActiveThreads: v.activeThreadCount, Statics: append([]int32(nil), v.statics...), Pieces: append([]model.PieceState(nil), v.Pieces...), PieceBusy: append([]bool(nil), v.pieceBusy...), PieceFlags: v.SnapshotFlags(), Diagnostics: append([]string(nil), v.diagnostics...), TickDenominator: v.tickDenom, Dirty: v.dirty}
	for i := range v.onReturn {
		d.CompletionReceiverPresent[i] = v.onReturn[i] != nil
	}
	if v.prog != nil {
		d.ProgramPieces = append([]string(nil), v.prog.Pieces...)
		d.ProgramCodeWords = len(v.prog.Code)
		d.ProgramChecksum = v.prog.SourceChecksum
		d.ScriptEntryWords = append([]int(nil), v.prog.ScriptsByID...)
		for name := range v.prog.Scripts {
			d.ProgramFunctions = append(d.ProgramFunctions, name)
		}
		sort.Strings(d.ProgramFunctions)
	}
	for _, p := range v.anims {
		var a [3]DebugAxis
		for i, x := range p.axes {
			a[i] = DebugAxis{x.moveTarget, x.moveSpeed, x.moveBusy, x.turnTarget, x.turnSpeed, x.turnBusy, x.spinSpeed, x.spinTarget, x.spinAccel, x.spinActive}
		}
		d.Animations = append(d.Animations, a)
	}
	return d
}

// DebugCallbacks preserves the bridge's pending callback identities without
// retaining completion closures or invoking script execution.
type DebugCallbacks struct {
	CreateInvoked   bool
	LifecycleTick   uint32
	LifecycleSource uint16
	Pending         [8]DebugPendingCallback
}
type DebugPendingCallback struct {
	Active   bool
	Name     string
	Mode     CallbackMode
	Identity uint64
}

func (b *CallbackBridge) DebugSnapshot() *DebugCallbacks {
	if b == nil {
		return nil
	}
	d := &DebugCallbacks{CreateInvoked: b.createInvoked, LifecycleTick: b.lifecycleTick, LifecycleSource: b.lifecycleSrc}
	for i, p := range b.lifecyclePending {
		d.Pending[i] = DebugPendingCallback{p.active, p.name, p.mode, p.identity}
	}
	return d
}
