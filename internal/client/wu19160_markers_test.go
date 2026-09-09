package client

import (
	"testing"

	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
)

// The shading display option has one consumer that matters — the model
// composer's choice between the shaded and unshaded piece renderers, which
// retail takes only for a structure-class unit with the option on
// [03 R-RND-02A]. The renderer keeps that bit in its own package, so the two
// copies can drift apart silently: before this wiring the `VISUALS` `SHADING`
// button reached the client's bit and never the renderer's, and every
// structure stayed shaded with the option off. Nothing else fails if the
// assignment is dropped, which is why it is pinned here.
func TestSetShadowOptionsCarriesShadingToTheModelComposer(t *testing.T) {
	previous := presentationrender.Shading
	t.Cleanup(func() { presentationrender.Shading = previous })

	c := &Client{}
	c.SetShadowOptions(true, true, false)
	if presentationrender.Shading {
		t.Fatal("shading off did not reach the model composer's option bit")
	}
	if _, _, shading := c.ShadowOptions(); shading {
		t.Fatal("shading off did not reach the client's own bit")
	}

	c.SetShadowOptions(true, true, true)
	if !presentationrender.Shading {
		t.Fatal("shading on did not reach the model composer's option bit")
	}
}
