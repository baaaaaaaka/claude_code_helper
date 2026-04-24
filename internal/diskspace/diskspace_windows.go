//go:build windows

package diskspace

import (
	"errors"

	"golang.org/x/sys/windows"
)

func availableBytes(path string) (uint64, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}

func isNoSpaceError(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) ||
		errors.Is(err, windows.ERROR_HANDLE_DISK_FULL) ||
		errors.Is(err, windows.ERROR_DISK_TOO_FRAGMENTED)
}
