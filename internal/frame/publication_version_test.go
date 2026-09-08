package frame

import "testing"

func TestFrameResetRestoresImmutablePresentationRevision(t *testing.T) {
	f := Frame{
		Visibility: VisibilityView{W: 1, H: 1, Visible: []uint8{3}, WordVisible: []uint16{1}, Valid: true, MappingSource: 3, MappingVersion: 7},
		Fog:        FogView{W: 1, H: 1, Ch0: []uint8{15}, Ch1: []uint8{0}, Valid: true, Source: 4, Version: 11},
	}
	f.Reset()
	if len(f.Visibility.Visible) != 0 || len(f.Fog.Ch0) != 0 {
		t.Fatal("Reset did not return presentation slices to writer shape")
	}
	if !f.RestoreVisibility(3, 7) || !f.RestoreFog(4, 11) {
		t.Fatal("Reset lost a retained immutable presentation revision")
	}
	if f.Visibility.Visible[0] != 3 || f.Fog.Ch0[0] != 15 {
		t.Fatalf("restored presentation bytes = visibility %v fog %v", f.Visibility.Visible, f.Fog.Ch0)
	}
	if f.RestoreFog(4, 12) || f.RestoreVisibility(5, 7) {
		t.Fatal("restored fog for an unrelated revision")
	}
}
