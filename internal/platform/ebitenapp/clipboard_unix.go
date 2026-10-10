//go:build unix && !darwin && !android && !ebitenginevmguest

package ebitenapp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Linux and the BSD desktops have no clipboard the standard library can
// reach without cgo, so the bridge runs the session's clipboard program
// (clipboardHelpers). A paste starts one only for a new paste key token, never
// per frame, so a process per paste is affordable. A missing program or a
// failed run reads as no text, which preserves the editor [07 §2].

// clipboardHelperWait bounds one paste or Copy across every program it tries,
// and clipboardHelperDrain how long a finished program's pipes may stay open.
// These are host bounds, not simulation values.
const (
	clipboardHelperWait  = 900 * time.Millisecond
	clipboardHelperDrain = 100 * time.Millisecond
)

// sessionClipboardCommands are the programs this process can run, looked up
// once: the display server and PATH do not change under a running game, and
// the lobby asks HostClipboardWritable on every refresh.
var sessionClipboardCommands = sync.OnceValues(func() (pasters, copiers []clipboardCommand) {
	return clipboardCommands(false, os.Getenv, exec.LookPath), clipboardCommands(true, os.Getenv, exec.LookPath)
})

// clipboardHelperCommand runs a program under the shared deadline, which kills
// it; the drain then ends a run whose pipes a leftover process holds open.
func clipboardHelperCommand(ctx context.Context, command clipboardCommand) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command.path, command.args...)
	cmd.WaitDelay = clipboardHelperDrain
	return cmd
}

func readHostClipboard() input.ClipboardText {
	pasters, _ := sessionClipboardCommands()
	return pasteWithHelpers(pasters)
}

// pasteWithHelpers takes the first program that prints the clipboard,
// keeping at most clipboardReadLimit bytes of its output.
func pasteWithHelpers(pasters []clipboardCommand) input.ClipboardText {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardHelperWait)
	defer cancel()
	for _, command := range pasters {
		output := cappedOutput{limit: clipboardReadLimit}
		cmd := clipboardHelperCommand(ctx, command)
		cmd.Stdout = &output
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if len(output.text) == 0 && command.emptyIsNone {
			continue
		}
		return input.ClipboardText{Text: string(output.text), Available: true}
	}
	return input.ClipboardText{}
}

// HostClipboardWritable reports whether this session has a program that
// copies to the clipboard.
func HostClipboardWritable() bool {
	_, copiers := sessionClipboardCommands()
	return len(copiers) > 0
}

// WriteHostClipboard replaces the clipboard's contents with text, for a
// front-end Copy button, and reports whether a program took it.
func WriteHostClipboard(text string) bool {
	_, copiers := sessionClipboardCommands()
	return copyWithHelpers(copiers, text)
}

// copyWithHelpers hands text on stdin to the first program that takes it.
// Each program leaves a background process that owns the clipboard until
// something else replaces it. That process's output goes to the null device,
// because a pipe it inherited would hold Wait open, and it starts in its own
// session, so a signal to the game's process group (Ctrl+C in the launching
// terminal) does not take the copied text with it.
func copyWithHelpers(copiers []clipboardCommand, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardHelperWait)
	defer cancel()
	for _, command := range copiers {
		cmd := clipboardHelperCommand(ctx, command)
		cmd.Stdin = strings.NewReader(text)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if cmd.Run() == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
	}
	return false
}
