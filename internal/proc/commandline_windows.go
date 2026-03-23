//go:build windows

package proc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func CommandLine(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)

	var pbi windows.PROCESS_BASIC_INFORMATION
	infoSize := uint32(unsafe.Sizeof(pbi))
	if err := windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&pbi), infoSize, &infoSize); err != nil {
		return nil, err
	}
	if pbi.PebBaseAddress == nil {
		return nil, fmt.Errorf("pid %d has no PEB", pid)
	}

	var (
		peb  windows.PEB
		read uintptr
	)
	if err := windows.ReadProcessMemory(handle, uintptr(unsafe.Pointer(pbi.PebBaseAddress)), (*byte)(unsafe.Pointer(&peb)), unsafe.Sizeof(peb), &read); err != nil {
		return nil, err
	}
	if read < unsafe.Sizeof(peb) {
		return nil, fmt.Errorf("short read for pid %d PEB", pid)
	}
	if peb.ProcessParameters == nil {
		return nil, fmt.Errorf("pid %d has no process parameters", pid)
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err := windows.ReadProcessMemory(handle, uintptr(unsafe.Pointer(peb.ProcessParameters)), (*byte)(unsafe.Pointer(&params)), unsafe.Sizeof(params), &read); err != nil {
		return nil, err
	}
	if read < unsafe.Sizeof(params) {
		return nil, fmt.Errorf("short read for pid %d process parameters", pid)
	}
	if params.CommandLine.Buffer == nil || params.CommandLine.Length == 0 {
		return nil, fmt.Errorf("empty command line for pid %d", pid)
	}
	if params.CommandLine.Length%2 != 0 {
		return nil, fmt.Errorf("invalid command line length for pid %d", pid)
	}

	raw := make([]uint16, params.CommandLine.Length/2)
	if err := windows.ReadProcessMemory(handle, uintptr(unsafe.Pointer(params.CommandLine.Buffer)), (*byte)(unsafe.Pointer(&raw[0])), uintptr(params.CommandLine.Length), &read); err != nil {
		return nil, err
	}
	if read < uintptr(params.CommandLine.Length) {
		return nil, fmt.Errorf("short read for pid %d command line", pid)
	}

	commandLine := windows.UTF16ToString(raw)
	if commandLine == "" {
		return nil, fmt.Errorf("empty command line for pid %d", pid)
	}
	return windows.DecomposeCommandLine(commandLine)
}
