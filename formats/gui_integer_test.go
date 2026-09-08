package formats

import "testing"

func TestGUIHeaderIntegersUseWrappedDecimalPrefix(t *testing.T) {
	panel, err := LoadGUI([]byte(`[GADGET0] {
        [COMMON] { id=0; }
        totalgadgets=18446744073709551617tail;
        [VERSION] { major=4294967299suffix; minor=-4294967297; revision=2147483648; }
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if panel.Header.TotalGadgets != 1 || panel.Header.VersionMajor != 3 ||
		panel.Header.VersionMinor != -1 || panel.Header.VersionRev != -2147483648 {
		t.Fatalf("GUI header did not preserve the TDF integer boundary: %+v", panel.Header)
	}
}
