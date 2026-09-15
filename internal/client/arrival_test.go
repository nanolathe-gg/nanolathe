package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
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

func TestArrivalRevealMeasuresExploredTilesInsteadOfBlackViewport(t *testing.T) {
	c, buffer := pipelineClient(t)
	c.SetEnhanced(true)
	c.width, c.height = 640, 480
	c.cam = &camera.Camera{}
	fog := frame.FogView{W: 40, H: 30, Valid: true, Ch0: make([]byte, 40*30)}
	for i := range fog.Ch0 {
		fog.Ch0[i] = 15
	}
	for z := 6; z <= 8; z++ {
		for x := 9; x <= 11; x++ {
			fog.Ch0[z*40+x] = 0
		}
	}
	buffer.Current().Fog = fog
	u := buffer.Current().Units[0]
	u.X, u.Z = wu(320), wu(240)
	c.StartArrival(u)
	small := c.arrivalPacket().RevealRadius
	if small <= 32 || small >= 160 {
		t.Fatalf("small explored patch radius = %v", small)
	}
	c.width, c.height = 1024, 768
	if got := c.arrivalPacket().RevealRadius; got != small {
		t.Fatalf("black viewport padding changed radius: %v -> %v", small, got)
	}
	fog.Ch0[6*40+38] = 0 // Explored, but beyond the right edge.
	if got := c.arrivalPacket().RevealRadius; got != small {
		t.Fatalf("off-screen tile changed radius: %v", got)
	}
	fog.Ch0[6*40+17] = 1 // A partially exposed distant tile still matters.
	if got := c.arrivalPacket().RevealRadius; got <= small {
		t.Fatal("partial explored edge did not extend the reveal")
	}
	fog.Ch0[6*40+17] = 15
	c.SetArrivalSeconds(drawlist.ArrivalDropSeconds)
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		c.cam.Scale = scale
		raised := c.arrivalUnit(u)
		_, sy := c.cam.WorldToScreen(raised.X, raised.Y, raised.Z)
		if y := sy - camera.OriginY; y < camera.OriginY || y > camera.OriginY+scale.Px(32) {
			t.Fatalf("drop starts outside upper viewport edge at scale %v: y=%v", scale, y)
		}
	}
	if got := c.arrivalPacket().RevealRadius; got != small {
		t.Fatalf("zoom changed world-space radius: %v -> %v", small, got)
	}
}

func TestArrivalScarStaysAtLandingAfterCoolingAndResetsWithBattle(t *testing.T) {
	c, f := scorchScene(t)
	f.Effects = nil
	u := frame.UnitView{Slot: 1, X: wu(32), Z: wu(32)}
	c.StartArrival(u)
	if marks := recordScorch(c, f); len(marks) != 0 {
		t.Fatal("scar appeared before impact")
	}
	c.SetArrivalSeconds(drawlist.ArrivalImpactSeconds)
	marks := recordScorch(c, f)
	if len(marks) != 1 || !marks[0].Landing {
		t.Fatalf("landing mark = %+v", marks)
	}
	first := marks[0]
	c.SetArrivalSeconds(drawlist.ArrivalCoolingEndSeconds)
	u.X += wu(64)
	f.Units = []frame.UnitView{u}
	f.Tick += 10000
	marks = recordScorch(c, f)
	if len(marks) != 1 || !marks[0].Landing || marks[0].X != first.X || marks[0].Y != first.Y {
		t.Fatalf("scar expired or followed commander: %+v", marks)
	}
	c.SetSnapshot(frame.NewBuffer())
	if marks := recordScorch(c, f); len(marks) != 0 {
		t.Fatal("scar survived battle replacement")
	}
}

func TestMapRevealPreservesCameraAndUnitsWithoutLanding(t *testing.T) {
	c, buffer := pipelineClient(t)
	c.SetEnhanced(true)
	c.width, c.height = 640, 480
	c.cam = &camera.Camera{X: 400, Z: 700, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	beforeCamera := *c.cam
	u := buffer.Current().Units[0]
	c.StartMapReveal()
	if !c.ArrivalActive() || c.ArrivalHasDrop() || c.ArrivalDuration() != drawlist.ArrivalRevealSeconds {
		t.Fatal("scene reveal selected landing choreography")
	}
	if *c.cam != beforeCamera {
		t.Fatal("reveal changed the saved camera")
	}
	for _, age := range []float32{0, drawlist.ArrivalDropSeconds, drawlist.ArrivalRevealSeconds} {
		c.SetArrivalSeconds(age)
		if c.arrivalHidesUnit(u) || !reflect.DeepEqual(c.arrivalUnit(u), u) {
			t.Fatal("reveal altered saved unit pose")
		}
		var g drawlist.ModelGeometry
		c.applyArrivalHeat(&g, u)
		if g.WreckHeatStrength != 0 || g.WreckEmission != [3]float32{} || c.arrival.cooling || c.arrival.landed {
			t.Fatal("reveal emitted landing heat or scar")
		}
	}
	if c.ArrivalActive() {
		t.Fatal("reveal held gameplay after tiles settled")
	}
	c.StartMapReveal()
	if !c.worldSpace(true).Arrival.RevealOnly {
		t.Fatal("missing reveal-only packet")
	}
	c.BeginWorldOverlay()
	if c.worldSpace(true).Arrival.Active {
		t.Fatal("overlay replayed the scene reveal")
	}
	c.EndWorldOverlay()
}
