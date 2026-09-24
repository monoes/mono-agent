//go:build windows

package health

import "golang.org/x/sys/windows"

// FreeBytes returns the bytes available to the current user on the volume
// holding path.
func FreeBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, err
	}
	return avail, nil
}
