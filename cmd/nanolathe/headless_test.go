package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHeadlessSkirmishStepsAndReports(t *testing.T) {
	root := probeRetail(t)
	opts := Options{
		Root:     root,
		Map:      "ashap plateau",
		Seed:     1,
		Headless: true,
		Ticks:    300,
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "stdout.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	errOut, err := os.Create(filepath.Join(t.TempDir(), "stderr.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = errOut.Close() })
	if code := runOptions(opts, out, errOut); code != 2 {
		t.Fatalf("headless exit code = %d, want 2", code)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	var report headlessReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if report.Tick != 300 {
		t.Fatalf("report tick = %d, want 300", report.Tick)
	}
	if !report.Players[1].AIManagerBound {
		t.Fatal("computer player's AI manager was not reported as bound")
	}
}
