//go:build retail

package content

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// BenchmarkCompileSimArtRetail measures the content-boundary workload without
// catalog construction or simulation time. It deliberately keeps one mounted
// reference install and one immutable catalog, then rebuilds only the
// battle-local SimArt table each iteration.
func BenchmarkCompileSimArtRetail(b *testing.B) {
	root := os.Getenv(testsupport.RetailAssetsEnv)
	if root == "" {
		root = os.Getenv(testsupport.RetailAssetsEnvLegacy)
	}
	if root == "" {
		b.Skipf("retail assets are opt-in: set %s", testsupport.RetailAssetsEnv)
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		b.Fatalf("mount: %v", err)
	}
	defer fs.Close()
	cat, err := Compile(fs)
	if err != nil {
		b.Fatalf("compile catalog: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if art := CompileSimArt(fs, cat); art == nil {
			b.Fatal("nil SimArt")
		}
	}
}
