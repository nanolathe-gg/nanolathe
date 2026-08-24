package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// findRetailRoot tries defaultRoot and fallback paths for tests.
func findRetailRoot() string {
	if root := os.Getenv("NANOLATHE_TA_ROOT"); root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := home + "/TotalAnnihilation"
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
	}
	if info, err := os.Stat("/path/to/home/TotalAnnihilation"); err == nil && info.IsDir() {
		return "/path/to/home/TotalAnnihilation"
	}
	return ""
}

func probeRetail(t *testing.T) string {
	t.Helper()
	if root := findRetailRoot(); root != "" {
		if _, err := os.Stat(root + "/totala1.hpi"); err == nil {
			return root
		}
	}
	t.Skip("retail not available for gate2 route determinism")
	return ""
}

// TestGate2RouteDeterminism locks that same seed ⇒ identical route dump
// (points ≤20, encLen ≤13) and final position are deterministic [I4].
func TestGate2RouteDeterminism(t *testing.T) {
	root := probeRetail(t)
	optsProbe := Options{Root: root, Map: "ashap plateau"}
	cs, err := openContent(optsProbe)
	if err != nil {
		t.Skipf("retail not available for gate2 route determinism: %v", err)
	}
	cs.Close()

	capture := func(seed int64) string {
		opts := Options{Root: root, Map: "ashap plateau", Ticks: 100, Seed: seed, Dump: "route"}
		cs2, err := openContent(opts)
		if err != nil {
			t.Fatalf("openContent: %v", err)
		}
		defer cs2.Close()
		sim, crt := seedsFor(opts)
		rng.SeedGlobal(sim, crt)
		tmp, err := os.CreateTemp("", "nanolathe-route-*.txt")
		if err != nil {
			t.Fatalf("tmp: %v", err)
		}
		name := tmp.Name()
		defer os.Remove(name)
		if err := runGate2RouteDump(opts, cs2, tmp); err != nil {
			tmp.Close()
			t.Fatalf("runGate2RouteDump: %v", err)
		}
		tmp.Close()
		f, err := os.Open(name)
		if err != nil {
			t.Fatalf("open tmp: %v", err)
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return string(data)
	}

	a := capture(42)
	b := capture(42)
	if a != b {
		t.Fatalf("determinism failed: same seed diverged:\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, "route points") {
		t.Fatalf("route dump missing points: %q", a)
	}
	if !strings.Contains(a, "route encLen") {
		t.Fatalf("route dump missing encLen: %q", a)
	}
	// Check caps are respected (parse quickly)
	if strings.Contains(a, "route violation") {
		t.Fatalf("route violation in dump: %q", a)
	}
	// Different seed should not panic; output may coincidentally be same because route is deterministic and not RNG-dependent, but it must be valid
	c := capture(43)
	if !strings.Contains(c, "route points") {
		t.Fatalf("second seed missing points: %q", c)
	}
	_ = b
	_ = c
}

// TestGate2RouteDumpCaps verifies that the route dump respects the ≤20 and ≤13 caps.
func TestGate2RouteDumpCaps(t *testing.T) {
	root := probeRetail(t)
	optsProbe := Options{Root: root, Map: "ashap plateau"}
	cs, err := openContent(optsProbe)
	if err != nil {
		t.Skipf("retail not available: %v", err)
	}
	cs.Close()
	opts := Options{Root: root, Map: "ashap plateau", Ticks: 100, Seed: 42, Dump: "route"}
	cs2, err := openContent(opts)
	if err != nil {
		t.Fatalf("openContent: %v", err)
	}
	defer cs2.Close()
	sim, crt := seedsFor(opts)
	rng.SeedGlobal(sim, crt)
	tmp, err := os.CreateTemp("", "nanolathe-route-caps-*.txt")
	if err != nil {
		t.Fatalf("tmp: %v", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := runGate2RouteDump(opts, cs2, tmp); err != nil {
		tmp.Close()
		t.Fatalf("runGate2RouteDump: %v", err)
	}
	tmp.Close()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "route violation") {
		t.Fatalf("caps violated: %q", s)
	}
	if !strings.Contains(s, "route points") || !strings.Contains(s, "route encLen") {
		t.Fatalf("dump missing fields: %q", s)
	}
}
