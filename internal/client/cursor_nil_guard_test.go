package client

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

// TestMissingCursorDoesNotPanicNilGuard verifies that when cursor assets are
// missing the client retains the OS cursor and every cursor call is nil-guarded
// [07 §8]. The frame loop previously dereferenced cl.Cursors() unconditionally.
func TestMissingCursorDoesNotPanicNilGuard(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil cursor panicked: %v", r)
		}
	}()
	cl, err := New(Options{Width: 64, Height: 64, Headless: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cl.Cursors() != nil {
		t.Fatalf("expected nil cursors on fresh client")
	}
	// ComposeFrame calls drawCursor internally; must not panic with nil cursors.
	_ = cl.ComposeFrame()
	// Direct nil-guarded Step pattern used by battle.go viewerStep.
	if cursors := cl.Cursors(); cursors != nil {
		cursors.Step(1)
	}
	// Also verify LoadCursors on empty FS fails with provider diagnostic, not panic.
	fs := vfs.New()
	_, err = LoadCursors(fs)
	if err == nil {
		t.Fatal("expected LoadCursors to fail on empty FS")
	}
	if !strings.Contains(err.Error(), "logical path anims/cursors.gaf") || !strings.Contains(err.Error(), "providers searched") {
		t.Fatalf("cursor error missing provider diagnostic: %v", err)
	}
	// Verify drawCursor is nil-guarded when called directly.
	cl.drawCursor() // should be no-op
	// Verify Cursors().Frame etc are nil-guarded.
	var cs *Cursors
	if cs.Frame() != nil {
		t.Fatal("nil Cursors Frame should be nil")
	}
	cs.Step(1)     // nil Step must not panic
	cs.SetIndex(1) // nil SetIndex must not panic
	if cs.Index() != 0 {
		t.Fatalf("nil Index got %d want 0", cs.Index())
	}
}
