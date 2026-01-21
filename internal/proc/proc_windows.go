//go:build windows

package proc

import "golang.org/x/sys/windows"

func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}

	// Windows STILL_ACTIVE (259) indicates the process has not terminated.
	const stillActive = 259
	return code == stillActive
}
