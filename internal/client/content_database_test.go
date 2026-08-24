package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKaijuContentDatabaseMapsLogicalNames(t *testing.T) {
	root := t.TempDir()
	passDir := filepath.Join(root, "renderer", "passes")
	if err := os.MkdirAll(passDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(passDir, "swapchain.renderpass"), []byte("pass"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := newKaijuContentDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	data, err := db.Read("swapchain.renderpass")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pass" {
		t.Fatalf("data = %q, want pass", data)
	}
}
