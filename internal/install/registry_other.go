//go:build !windows

package install

import "runtime"

func registryLocations() (roots, steam []string) { return nil, nil }

func systemLocations() []string {
	if runtime.GOOS == "darwin" {
		return []string{"/Applications"}
	}
	return nil
}
