package client

import (
	"errors"
	"testing"
)

func TestDebugCaptureDeviceJoinsAndPropagatesFailure(t *testing.T) {
	c, _ := pipelineClient(t)
	c.StartPreRecord(0, 0, false)
	if err := c.WriteDebugDeviceCapture(t.TempDir()); err == nil {
		t.Fatal("missing device accepted")
	}
	sentinel := errors.New("device refused")
	c.SetDebugDeviceCapture(func(string) error {
		if c.pre.pending && !c.pre.joined {
			t.Fatal("writer raced recorder")
		}
		return sentinel
	})
	c.StartPreRecord(0, 0, false)
	if !errors.Is(c.WriteDebugDeviceCapture(t.TempDir()), sentinel) {
		t.Fatal("writer error lost")
	}
}
