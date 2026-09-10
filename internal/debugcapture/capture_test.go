package debugcapture

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugCapturePartialFailureAndUniqueDirectory(t *testing.T) {
	base := t.TempDir()
	c, err := Begin(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.JSON("bad.json", math.NaN())
	c.JSON("good.json", struct{ Tick int }{7})
	c.Write("failed.pprof", func(w io.Writer) error { _, _ = io.WriteString(w, "partial"); return errors.New("profile failure") })
	c.Device(func(dir string) error { return errors.New("device failed") })
	if c.Finish() == nil {
		t.Fatal("partial failures must reach caller")
	}
	bytes, err := os.ReadFile(filepath.Join(c.Directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err = json.Unmarshal(bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Complete {
		t.Fatal("manifest claims completeness")
	}
	for _, name := range []string{"bad.json", "failed.pprof", "last-frame.png", "device"} {
		found := false
		for _, f := range manifest.Files {
			if f.Name == name && f.Error != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing failure for %s", name)
		}
	}
	if _, err = os.Stat(filepath.Join(c.Directory, "good.json")); err != nil {
		t.Fatal(err)
	}
	next, err := Begin(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Directory == c.Directory {
		t.Fatal("capture directory reused")
	}
	c.JSON("good.json", 0)
	if c.Manifest.Files[len(c.Manifest.Files)-1].Error == "" {
		t.Fatal("existing file overwritten")
	}
}
func TestDebugCaptureDirectoryFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Begin(path, nil); err == nil {
		t.Fatal("file treated as directory")
	}
}
