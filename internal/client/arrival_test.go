package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

func TestArrivalChangesOnlyMatchingDisplayedUnitAndRetires(t *testing.T) {
	c, buffer := pipelineClient(t)
	c.SetEnhanced(true)
	original := buffer.Current().Units[0]
	c.StartArrival(original)
	if !c.arrivalHidesUnit(original) {
		t.Fatal("commander appeared before the map reveal")
	}
	c.SetArrivalSeconds(drawlist.ArrivalDropSeconds)
	if c.arrivalHidesUnit(original) {
		t.Fatal("commander remained hidden during descent")
	}
	raised := c.arrivalUnit(original)
	if raised.Y <= original.Y {
		t.Fatal("commander did not descend from above")
	}
	other := original
	other.InstanceID++
	if !reflect.DeepEqual(c.arrivalUnit(other), other) {
		t.Fatal("intro followed a reused slot")
	}
	c.SetEnhanced(false)
	if !reflect.DeepEqual(c.arrivalUnit(original), original) {
		t.Fatal("classic pose changed")
	}
	c.SetEnhanced(true)
	c.SetArrivalSeconds(drawlist.ArrivalImpactSeconds)
	if !reflect.DeepEqual(c.arrivalUnit(original), original) {
		t.Fatal("landing did not restore exact committed pose")
	}
	if !reflect.DeepEqual(buffer.Current().Units[0], original) {
		t.Fatal("intro mutated committed unit")
	}
	c.SetSnapshot(frame.NewBuffer())
	if c.ArrivalActive() {
		t.Fatal("arrival survived battle replacement")
	}
}

func TestArrivalTimeInvalidatesSpeculativeRecord(t *testing.T) {
	c, _ := pipelineClient(t)
	c.StartArrival(frame.UnitView{Slot: 1})
	c.StartPreRecord(0, 0, false)
	c.JoinPreRecord()
	c.SetArrivalSeconds(.5)
	if _, hit := c.TakePreRecord(c.PresentationDigest(), 0); hit {
		t.Fatal("reused a record from another intro stage")
	}
}
