package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

// writeDropZip writes an authored mod zip holding its metadata and one file.
func writeDropZip(t *testing.T, meta modlibrary.Metadata) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), meta.ID+".zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{modlibrary.MetadataFile: string(data), "units/drop.fbi": "[UNITINFO] { }\n"} {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// A dropped zip installs through the local install path with the standard
// content check against the base install (§4.5): one that starts joins the
// library and is not selected, one that would not start is refused with its
// reason, and a drop of several items installs nothing.
func TestDroppedZipInstallsThroughTheContentCheck(t *testing.T) {
	base, lib := modFixture(t)
	var job modDownloadJob
	var state modDropState
	drop := func(paths ...string) (string, string) {
		t.Helper()
		notice := startModDrop(&job, &state, paths, []string{base}, openModLibrary)
		waitForJob(t, &job)
		outcome, _ := state.take(&job)
		return notice, outcome
	}

	good := writeDropZip(t, modlibrary.Metadata{Schema: 1, ID: "dropped", Name: "Dropped", Version: "3"})
	if notice, outcome := drop(good); notice != "Installing dropped.zip..." || outcome != "Dropped 3 installed" {
		t.Fatalf("drop notice %q, outcome %q", notice, outcome)
	}
	if mod, ok, err := lib.Lookup("dropped", "3"); err != nil || !ok || mod.Receipt.Source != "local:dropped.zip" {
		t.Fatalf("the dropped mod is not installed: %+v, %v, %v", mod, ok, err)
	}
	if v := job.view(); v.installs != 1 {
		t.Fatalf("the install count the Mods screen refreshes from = %d", v.installs)
	}

	broken := writeDropZip(t, modlibrary.Metadata{Schema: 1, ID: "broken", Name: "Broken", Version: "1", ContentProfile: "profiles/missing.json"})
	if _, outcome := drop(broken); !strings.HasPrefix(outcome, "Install failed: ") {
		t.Fatalf("a package that would not start: outcome %q", outcome)
	}
	if _, ok, _ := lib.Lookup("broken", ""); ok {
		t.Fatal("a package that would not start was installed")
	}

	if notice := startModDrop(&job, &state, []string{good, broken}, []string{base}, openModLibrary); !strings.Contains(notice, "one mod") || job.view().running {
		t.Fatalf("a drop of two items: notice %q, running %v", notice, job.view().running)
	}
}
