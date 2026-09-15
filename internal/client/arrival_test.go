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

func TestArrivalHeatCoolsDuringGameplayAndDoesNotFollowReusedSlot(t *testing.T) {
	c, buffer := pipelineClient(t)
	c.SetEnhanced(true)
	c.effects.Distortion = true
	c.SetFocused(true)
	u := buffer.Current().Units[0]
	c.StartArrival(u)
	c.SetArrivalSeconds(drawlist.ArrivalImpactSeconds)
	var g drawlist.ModelGeometry
	c.applyArrivalHeat(&g, u)
	hot := g.WreckEmission[0]
	if hot <= 0 || g.WreckHeatStrength <= 0 {
		t.Fatal("landing is cold")
	}
	c.SetArrivalSeconds(drawlist.ArrivalDurationSeconds)
	if c.ArrivalActive() {
		t.Fatal("cooling blocks gameplay")
	}
	c.applyArrivalHeat(&g, u)
	if g.WreckEmission[0] <= 0 || g.WreckEmission[0] >= hot {
		t.Fatal("handoff did not retain cooling glow")
	}
	before := c.ArrivalSeconds()
	c.StepArrivalCooling(1.0 / 60)
	if c.ArrivalSeconds() <= before {
		t.Fatal("gameplay does not advance cooling")
	}
	c.SetPresentationPaused(true)
	before = c.ArrivalSeconds()
	c.StepArrivalCooling(1)
	if c.ArrivalSeconds() != before {
		t.Fatal("paused commander cooled")
	}
	other := u
	other.InstanceID++
	c.applyArrivalHeat(&g, other)
	if g.WreckEmission != [3]float32{} || g.WreckHeatStrength != 0 {
		t.Fatal("heat followed a reused slot")
	}
	c.SetArrivalSeconds(drawlist.ArrivalCoolingEndSeconds)
	c.applyArrivalHeat(&g, u)
	if g.WreckEmission != [3]float32{} || g.WreckHeatStrength != 0 || c.arrival.cooling {
		t.Fatal("heat did not retire")
	}
}
