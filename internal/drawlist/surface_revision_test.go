package drawlist

import "testing"

func TestClonePreservesSurfaceIdentityAndRevision(t *testing.T) {
	var l List
	l.RecordSurface(Surface{Pixels: []byte{7}, SrcW: 1, SrcH: 1, Identity: 23, Revision: 9})
	c := l.Clone()
	if got := c.surface[0]; got.Identity != 23 || got.Revision != 9 {
		t.Fatalf("cloned surface identity/revision = %d/%d, want 23/9", got.Identity, got.Revision)
	}
}
