//go:build unix && !darwin && !android && !ebitenginevmguest

package ebitenapp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// shellHelper stands in for a clipboard program, so these tests never touch
// the session's clipboard and need no display server.
func shellHelper(script string, emptyIsNone bool) clipboardCommand {
	return clipboardCommand{path: "/bin/sh", args: []string{"-c", script}, emptyIsNone: emptyIsNone}
}

func TestPasteWithHelpersTakesTheFirstProgramThatPrints(t *testing.T) {
	for _, tc := range []struct {
		name    string
		helpers []clipboardCommand
		want    input.ClipboardText
	}{
		{"no program", nil, input.ClipboardText{}},
		{"text", []clipboardCommand{shellHelper("printf 'ABC234'", false)}, input.ClipboardText{Text: "ABC234", Available: true}},
		{"a failure falls through", []clipboardCommand{shellHelper("exit 1", false), shellHelper("printf 'two'", false)},
			input.ClipboardText{Text: "two", Available: true}},
		{"successful empty text", []clipboardCommand{shellHelper("true", false)}, input.ClipboardText{Available: true}},
		{"empty that cannot be told from none", []clipboardCommand{shellHelper("true", true)}, input.ClipboardText{}},
		{"every program fails", []clipboardCommand{shellHelper("exit 1", false), shellHelper("exit 2", true)}, input.ClipboardText{}},
	} {
		if got := pasteWithHelpers(tc.helpers); got != tc.want {
			t.Errorf("%s: paste = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestPasteWithHelpersBoundsOutputAndTime(t *testing.T) {
	got := pasteWithHelpers([]clipboardCommand{shellHelper("yes | head -c 200000", false)})
	if !got.Available || len(got.Text) != clipboardReadLimit {
		t.Fatalf("long output kept %d bytes (available %v), want %d", len(got.Text), got.Available, clipboardReadLimit)
	}
	// A program that never answers spends the shared wait; the next is not run.
	start := time.Now()
	got = pasteWithHelpers([]clipboardCommand{shellHelper("sleep 5", false), shellHelper("printf late", false)})
	if elapsed := time.Since(start); got.Available || elapsed > clipboardHelperWait+time.Second {
		t.Fatalf("stalled paste = %+v after %v, want unavailable within about %v", got, elapsed, clipboardHelperWait)
	}
}

func TestCopyWithHelpersWritesStdinAndLeavesTheOwnerRunning(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clipboard")
	// Like xclip, the program reads its input and leaves a background
	// process behind to own the selection; the Copy must not wait for it.
	owner := shellHelper(`cat > "$0"; sleep 3 & exit 0`, false)
	owner.args = append(owner.args, out)
	start := time.Now()
	if !copyWithHelpers([]clipboardCommand{shellHelper("exit 1", false), owner}, "ABC234") {
		t.Fatal("copy refused")
	}
	if elapsed := time.Since(start); elapsed > clipboardHelperWait {
		t.Fatalf("copy waited %v for the background owner", elapsed)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "ABC234" {
		t.Fatalf("program received %q (%v), want the room code", data, err)
	}
	if copyWithHelpers(nil, "x") || copyWithHelpers([]clipboardCommand{shellHelper("exit 1", false)}, "x") {
		t.Fatal("copy with no working program reported success")
	}
}
