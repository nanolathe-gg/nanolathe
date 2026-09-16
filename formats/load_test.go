package formats

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// readMountedFile is the byte read every production caller performs before it
// decodes: the format loaders take bytes, and only the VFS read stands between
// a mounted fixture and them.
func readMountedFile(t *testing.T, fs vfs.FSOps, name string) []byte {
	t.Helper()
	data, err := readVFS(fs, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}
