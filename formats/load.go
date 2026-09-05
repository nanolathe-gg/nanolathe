package formats

import (
	"io"

	"github.com/nanolathe/nanolathe/vfs"
)

func readVFS(fs vfs.FSOps, name string) ([]byte, error) {
	if fs == nil {
		return nil, io.ErrClosedPipe
	}
	return fs.ReadFileLimit(name, 1<<30)
}

func readVFSWithLimit(fs vfs.FSOps, name string, max int64) ([]byte, error) {
	if fs == nil {
		return nil, io.ErrClosedPipe
	}
	return fs.ReadFileLimit(name, max)
}
