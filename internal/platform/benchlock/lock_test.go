package benchlock

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockProcessHelper(t *testing.T) {
	path := os.Getenv("NANOLATHE_TEST_BENCH_LOCK")
	if path == "" {
		t.Skip("subprocess helper")
	}
	f, err := Acquire(path, func() { fmt.Println("waiting") })
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Println("acquired")
	io.Copy(io.Discard, os.Stdin)
}

// A waiting process must not enter until the holder closes; a killed holder
// must not leave a stale lock requiring manual deletion.
func TestWaitAndCrashRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benchmark.lock")
	holder, err := Acquire(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockProcessHelper$")
	cmd.Env = append(os.Environ(), "NANOLATHE_TEST_BENCH_LOCK="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	scanner := bufio.NewScanner(out)
	expect := func(want string) {
		t.Helper()
		if !scanner.Scan() || scanner.Text() != want {
			t.Fatalf("child message = %q (%v), want %q", scanner.Text(), scanner.Err(), want)
		}
	}
	expect("waiting")
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	expect("acquired")
	probe, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if acquired, err := lock(probe, false); err != nil || acquired {
		t.Fatalf("child failed to exclude another holder: %v, %v", acquired, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	if acquired, err := lock(probe, false); err != nil || !acquired {
		t.Fatalf("killed child left lock held: %v, %v", acquired, err)
	}
}
