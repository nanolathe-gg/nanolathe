package ebitenapp

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
)

func TestPipelineStatsRequireOptIn(t *testing.T) {
	c, err := client.New(client.Options{Width: 8, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "quiet", true: "enabled"}[enabled], func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "stats")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			original := os.Stderr
			os.Stderr = file
			defer func() { os.Stderr = original }()
			a := app{c: c, options: RunOptions{Stats: enabled}}
			a.pipe.synchronous = pipelineReportEvery
			a.paused.records, a.paused.reuses = 1, pipelineReportEvery-1
			a.reportPipelinePeriodically()
			a.pipe.synchronous++
			a.reportPipeline() // the same hook used at exit
			if _, err := file.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(file)
			if err != nil {
				t.Fatal(err)
			}
			if !enabled && len(out) != 0 {
				t.Fatalf("default session printed statistics: %s", out)
			}
			if enabled && (!strings.Contains(string(out), "record pipeline:") || !strings.Contains(string(out), "paused world:")) {
				t.Fatalf("opt-in statistics missing: %s", out)
			}
			if a.pipe.synchronous != pipelineReportEvery+1 || a.paused.reuses != pipelineReportEvery-1 {
				t.Fatal("reporting changed capture counters")
			}
		})
	}
}
