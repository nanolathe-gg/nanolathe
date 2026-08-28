package main

import "testing"

func TestPhysicalLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want int
	}{
		{name: "empty", data: "", want: 0},
		{name: "newline terminated", data: "a\nb\n", want: 2},
		{name: "unterminated final line", data: "a\nb", want: 2},
		{name: "crlf", data: "a\r\nb\r\n", want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := physicalLines([]byte(tc.data)); got != tc.want {
				t.Fatalf("physicalLines(%q) = %d, want %d", tc.data, got, tc.want)
			}
		})
	}
}

func TestPackagePath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "cmd/nanolathe/main.go", want: "cmd/nanolathe"},
		{path: "internal/sim/rng/rng.go", want: "internal/sim/rng"},
		{path: "formats/tdf.go", want: "formats"},
		{path: "main.go", want: "."},
	} {
		if got := packagePath(tc.path); got != tc.want {
			t.Errorf("packagePath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSummarizeSeparatesProductionAndTests(t *testing.T) {
	r := summarize(worktree, "", []inputFile{
		{path: "internal/demo/a.go", data: []byte("a\nb\n")},
		{path: "internal/demo/a_test.go", data: []byte("test")},
		{path: "formats/b.go", data: []byte("x\n")},
	})
	if r.files != 3 || r.productionFiles != 2 || r.productionLines != 3 || r.testFiles != 1 || r.testLines != 1 {
		t.Fatalf("unexpected totals: %+v", r)
	}
	if got := r.byPackagePath["internal/demo"]; got != (groupTotals{productionFiles: 1, productionLines: 2, testFiles: 1, testLines: 1}) {
		t.Fatalf("unexpected internal/demo totals: %+v", got)
	}
}
