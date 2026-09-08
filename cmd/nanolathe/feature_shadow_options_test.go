package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/settings"
)

// The persisted feature-shadow bit crosses the options boundary independently
// of the model-shadow master, vehicle-shadow and Shading bits [03 §5.3]
// [R-REN-03D §4].
func TestApplyVisualOptionsRoutesFeatureShadowsIndependently(t *testing.T) {
	cl, err := client.New(client.Options{Width: 8, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.SetShadowOptions(true, true, true) })
	applyVisualOptions(cl, settings.Display{
		Shadows: 1, FeatureShadows: 0, VehicleShadows: 1, Shading: 1,
	})
	if cl.FeatureShadows() {
		t.Fatal("FeatureShadows=0 did not disable the client's direct feature-shadow gate")
	}
	master, vehicle, shading := cl.ShadowOptions()
	if !master || !vehicle || !shading {
		t.Fatalf("independent model options changed to master=%t vehicle=%t shading=%t", master, vehicle, shading)
	}

	applyVisualOptions(cl, settings.Display{
		Shadows: 0, FeatureShadows: 1, VehicleShadows: 0, Shading: 0,
	})
	if !cl.FeatureShadows() {
		t.Fatal("FeatureShadows=1 did not enable the client's direct feature-shadow gate")
	}
	master, vehicle, shading = cl.ShadowOptions()
	if master || vehicle || shading {
		t.Fatalf("independent model options changed to master=%t vehicle=%t shading=%t", master, vehicle, shading)
	}

}
