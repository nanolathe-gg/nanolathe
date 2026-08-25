package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/vfs"
)

// mountRetail opens the reference install, or skips.
func mountRetail(t *testing.T) *vfs.FS {
	t.Helper()
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		root = filepath.Join(home, "TotalAnnihilation")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("retail data unavailable at %s: %v", root, err)
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount failed: %v", err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

// TestCursorsResolveAndSwap checks that every index in the table resolves from
// the retail cursor GAF and that the index writer diffs before swapping
// [07 §8]. Asset-guarded.
func TestCursorsResolveAndSwap(t *testing.T) {
	cs, err := LoadCursors(mountRetail(t))
	if err != nil {
		t.Fatalf("LoadCursors: %v", err)
	}
	for idx := 1; idx < render.CursorCount; idx++ {
		if cs.entries[idx] == nil {
			t.Errorf("cursor index %d (%q) did not resolve [07 §8]", idx, render.CursorName(idx))
		}
	}
	if cs.Index() != render.CursorNormal {
		t.Fatalf("initial index %d want cursornormal [07 §8]", cs.Index())
	}
	// A swap rebinds; re-selecting the same index keeps the playback position.
	cs.SetIndex(render.CursorAttack)
	if cs.Index() != render.CursorAttack {
		t.Fatalf("SetIndex(attack) left index %d", cs.Index())
	}
	cs.Step(7)
	before := cs.play.Idx
	cs.SetIndex(render.CursorAttack)
	if cs.play.Idx != before {
		t.Fatalf("re-selecting the shown index restarted playback: %d → %d [07 §8]", before, cs.play.Idx)
	}
	// An unresolvable index falls back to cursornormal rather than blanking.
	cs.SetIndex(render.CursorCount + 5)
	if cs.Index() != render.CursorNormal {
		t.Fatalf("out-of-range index gave %d want cursornormal", cs.Index())
	}
}

// TestCursorHotspotBlit checks the cursor is composed at the pointer with its
// authored hotspot honored [07 §8][fmt gaf]. Asset-guarded.
func TestCursorHotspotBlit(t *testing.T) {
	fs := mountRetail(t)
	cs, err := LoadCursors(fs)
	if err != nil {
		t.Fatalf("LoadCursors: %v", err)
	}
	c, err := New(Options{Width: 64, Height: 64, Headless: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.SetCursors(cs)
	// cursorattack is 33×33 with its hotspot at the centre, so blitting it at
	// (32,32) must paint pixels on every side of the pointer.
	cs.SetIndex(render.CursorAttack)
	f := cs.Frame()
	if f == nil {
		t.Fatal("no cursor frame")
	}
	if f.XOffset == 0 && f.YOffset == 0 {
		t.Fatalf("cursorattack has no authored hotspot; fixture assumption broken")
	}
	c.in.Mouse.SetPosition(32, 32)
	c.drawCursor()
	minX, minY, maxX, maxY := 64, 64, -1, -1
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if c.indexed[y*64+x] == 0 {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < 0 {
		t.Fatal("cursor painted nothing")
	}
	if minX >= 32 || minY >= 32 || maxX <= 32 || maxY <= 32 {
		t.Fatalf("hotspot not honored: painted box (%d,%d)-(%d,%d) does not surround the pointer [07 §8]",
			minX, minY, maxX, maxY)
	}
	// Hiding suppresses the blit without losing the installed shape.
	for i := range c.indexed {
		c.indexed[i] = 0
	}
	cs.Hidden = true
	c.drawCursor()
	for _, v := range c.indexed {
		if v != 0 {
			t.Fatal("hidden cursor still painted")
		}
	}
	if cs.Index() != render.CursorAttack {
		t.Fatalf("hiding changed the installed index to %d", cs.Index())
	}
}
