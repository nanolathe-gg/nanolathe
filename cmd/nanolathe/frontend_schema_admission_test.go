package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/vfs"
)

// Browser admission shares the contiguous type projection with battle entry,
// but deliberately does not require StartPos records [02 R-MAP-01 §4, §9].
func TestMapBrowserSchemaAdmission(t *testing.T) {
	cases := []struct {
		name, schemas      string
		listed, selectable bool
	}{
		{"network1", "[Schema 0] { Type=Network 1; STARTS }", true, true},
		{"network2", "[Schema 0] { Type=Network 2; STARTS }", true, true},
		{"network3", "[Schema 0] { Type=nEtWoRk 3; STARTS }", true, true},
		{"network4", "[Schema 0] { Type=Network 4; STARTS }", true, true},
		{"unknown", "[Schema 0] { Type=Network 5; STARTS }", false, false},
		{"suffix", "[Schema 0] { Type=Networkjunk; STARTS }", false, false},
		{"gap", "[Schema 0] { Type=Easy; } [Schema 2] { Type=Network 1; STARTS }", false, false},
		{"duplicate", "[Schema 0] { Type=Easy; type=Network 2; STARTS }", true, true},
		{"duplicate_section", "[Schema 0] { Type=Easy; } [Schema 0] { Type=Network 1; STARTS }", false, false},
		{"no_starts", "[Schema 0] { Type=Network 1; }", true, false},
		{"extra_space", "[Schema 0] { Type=Network  1; STARTS }", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "maps"), 0700); err != nil {
				t.Fatal(err)
			}
			schemas := strings.ReplaceAll(tc.schemas, "STARTS", "[specials] { [special0] { specialwhat=StartPos1; } }")
			data := []byte(fmt.Sprintf("[GlobalHeader] { numplayers=0; SCHEMACOUNT=999; %s }", schemas))
			if err := os.WriteFile(filepath.Join(root, "maps", "fixture.ota"), data, 0600); err != nil {
				t.Fatal(err)
			}
			fs := vfs.New()
			if err := fs.MountDirectory(root, 0); err != nil {
				t.Fatal(err)
			}
			names, err := enumerateSkirmishMaps(fs)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(names) == 1; got != tc.listed {
				t.Fatalf("browser names %v; listed want %v", names, tc.listed)
			}
			ota, err := formats.LoadOTA(data)
			if err != nil {
				t.Fatal(err)
			}
			_, err = mission.SelectNetworkSchema(ota, 1)
			if got := err == nil; got != tc.selectable {
				t.Fatalf("runtime error %v; selectable want %v", err, tc.selectable)
			}
			if tc.name == "gap" && ota.Global.Section("Schema 2") == nil {
				t.Fatal("raw gapped section lost")
			}
		})
	}
}
