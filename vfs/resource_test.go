package vfs

import "testing"

func TestResourcePathReplacesLastPeriodAcrossWholePath(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"", "objects3d/.3do"},
		{"model.3do", "objects3d/model.3do"},
		{"model.other", "objects3d/model.3do"},
		{"folder.old/model", "objects3d/folder.3do"},
		{"folder.old/model.new", "objects3d/folder.old/model.3do"},
	} {
		if got := ResourcePath("objects3d", tc.name, "3do"); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.name, got, tc.want)
		}
	}
}
