package architecture

import (
	"os/exec"
	"strings"
	"testing"
)

// platformOnly are the packages allowed to reach Ebitengine: the concrete
// window/loop/device adapter, the PCM device boundary, and the desktop binary
// that links them.
var platformOnly = map[string]bool{
	"github.com/nanolathe/nanolathe/internal/platform/ebitenapp": true,
	"github.com/nanolathe/nanolathe/internal/audiobackend":       true,
	"github.com/nanolathe/nanolathe/cmd/nanolathe":               true,
}

// TestOnlyThePlatformAdapterReachesEbitengine keeps the displayless boundary
// honest: every other package, including its test binary, must stand up with
// no window and no audio device.
//
// This is a dependency-closure guard rather than a count, so it does not
// obstruct cleanup: it fails only when a package acquires a device dependency
// it did not have.
func TestOnlyThePlatformAdapterReachesEbitengine(t *testing.T) {
	root := repositoryRoot(t)

	list := func(args ...string) []string {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.Fields(string(out))
	}

	for _, pkg := range list("list", "./...") {
		if platformOnly[pkg] {
			continue
		}
		// -test includes the package's own test dependencies, which is where
		// the leak into internal/session lived: session's tests imported the
		// client, and the client owned the window loop and built the audio
		// device.
		for _, dep := range list("list", "-deps", "-test", pkg) {
			if strings.HasPrefix(dep, "github.com/hajimehoshi/ebiten/v2") ||
				strings.HasPrefix(dep, "github.com/ebitengine/") {
				t.Errorf("%s reaches the platform dependency %s", pkg, dep)
				break
			}
		}
	}
}
