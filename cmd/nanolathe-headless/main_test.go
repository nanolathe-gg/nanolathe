package main

import (
	"bytes"
	"testing"
)

func TestParseBuildsExplicitSeedPair(t *testing.T) {
	request, _, err := parse([]string{"-root", "/tmp/assets", "-map", "test", "-seed", "23", "-ticks", "7"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if request.SimulationSeed != 23 || request.CRTSeed != 23 || request.TickLimit != 7 {
		t.Fatalf("request = %+v", request)
	}
}
