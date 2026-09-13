package install

import (
	"encoding/binary"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

// Registry values are hints, not trusted installations. Resolve applies the
// same archive-marker check as it does to conventional directories.
func registryLocations() (roots, steam []string) {
	for _, hive := range []syscall.Handle{syscall.HKEY_CURRENT_USER, syscall.HKEY_LOCAL_MACHINE} {
		for _, view := range []uint32{syscall.KEY_WOW64_32KEY, syscall.KEY_WOW64_64KEY} {
			if key, ok := openKey(hive, `SOFTWARE\Valve\Steam`, view); ok {
				for _, name := range []string{"SteamPath", "InstallPath"} {
					if p := registryString(key, name); p != "" {
						steam = append(steam, p)
					}
				}
				syscall.RegCloseKey(key)
			}
			for _, path := range []string{`SOFTWARE\Cavedog Entertainment\Total Annihilation`, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, `SOFTWARE\GOG.com\Games`} {
				key, ok := openKey(hive, path, view)
				if !ok {
					continue
				}
				if strings.HasSuffix(path, "Uninstall") || strings.HasSuffix(path, `GOG.com\Games`) {
					var names []string
					for index := uint32(0); ; index++ {
						var name [256]uint16
						length := uint32(len(name))
						if err := syscall.RegEnumKeyEx(key, index, &name[0], &length, nil, nil, nil, nil); err != nil {
							break
						}
						names = append(names, syscall.UTF16ToString(name[:length]))
					}
					sort.Strings(names)
					for _, name := range names {
						child, ok := openKey(key, name, view)
						if !ok {
							continue
						}
						if strings.HasSuffix(path, `GOG.com\Games`) {
							if p := registryString(child, "path"); p != "" {
								roots = append(roots, p)
							}
						} else if strings.Contains(strings.ToLower(registryString(child, "DisplayName")), "total annihilation") {
							if p := registryString(child, "InstallLocation"); p != "" {
								roots = append(roots, p)
							}
						}
						syscall.RegCloseKey(child)
					}
				} else {
					for _, name := range []string{"InstallPath", "InstallDir", "Path"} {
						if p := registryString(key, name); p != "" {
							roots = append(roots, p)
						}
					}
				}
				syscall.RegCloseKey(key)
			}
		}
	}
	return roots, steam
}

func openKey(parent syscall.Handle, path string, view uint32) (syscall.Handle, bool) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var key syscall.Handle
	err = syscall.RegOpenKeyEx(parent, name, 0, syscall.KEY_READ|view, &key)
	return key, err == nil
}

func registryString(key syscall.Handle, name string) string {
	pointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}
	var kind uint32
	var buffer [65536]byte
	size := uint32(len(buffer))
	err = syscall.RegQueryValueEx(key, pointer, nil, &kind, &buffer[0], &size)
	if err != nil || kind != syscall.REG_SZ || size%2 != 0 || size > uint32(len(buffer)) {
		return ""
	}
	value := make([]uint16, size/2)
	for i := range value {
		value[i] = binary.LittleEndian.Uint16(buffer[i*2:])
	}
	return syscall.UTF16ToString(value)
}

// Fixed drives avoid probing offline network mappings and removable media at
// startup. Explicit --root still permits either kind of location.
func systemLocations() []string {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	logical := kernel.NewProc("GetLogicalDrives")
	driveType := kernel.NewProc("GetDriveTypeW")
	mask, _, _ := logical.Call()
	var paths []string
	for letter := byte('C'); letter <= 'Z'; letter++ {
		if mask&(1<<(letter-'A')) == 0 {
			continue
		}
		path := string([]byte{letter, ':', '\\'})
		pointer, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			continue
		}
		kind, _, _ := driveType.Call(uintptr(unsafe.Pointer(pointer)))
		const fixedDrive = 3
		if kind == fixedDrive {
			paths = append(paths, path)
		}
	}
	return paths
}
