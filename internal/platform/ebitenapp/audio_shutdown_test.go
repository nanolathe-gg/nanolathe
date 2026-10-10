package ebitenapp

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
)

type shutdownOutput struct{ shutdowns int }

func (*shutdownOutput) PlaySample(*audio.Sample, float64, float64) error { return nil }
func (o *shutdownOutput) Shutdown()                                      { o.shutdowns++ }

func TestWindowTerminationShutsDownAudio(t *testing.T) {
	previous := audio.GlobalOutput()
	defer audio.SetGlobalOutput(previous)
	output := &shutdownOutput{}
	audio.SetGlobalOutput(output)
	// No client is needed for this exit boundary's pointer cleanup.
	a := &app{}
	_ = a.terminate()
	if output.shutdowns != 1 {
		t.Fatalf("audio shutdowns=%d, want one before termination", output.shutdowns)
	}
}
