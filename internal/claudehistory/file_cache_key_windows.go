//go:build windows

package claudehistory

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsFileBasicInfo struct {
	CreationTime   windows.Filetime
	LastAccessTime windows.Filetime
	LastWriteTime  windows.Filetime
	ChangeTime     windows.Filetime
	FileAttributes uint32
	_              uint32
}

func populateFileCacheKeyPlatform(path string, _ os.FileInfo, key *fileCacheKey) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return
	}
	key.Device = uint64(info.VolumeSerialNumber)
	key.Inode = (uint64(info.FileIndexHigh) << 32) | uint64(info.FileIndexLow)

	var basic windowsFileBasicInfo
	if err := windows.GetFileInformationByHandleEx(
		windows.Handle(f.Fd()),
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)),
		uint32(unsafe.Sizeof(basic)),
	); err != nil {
		return
	}
	key.ChangeTimeUnixNano = basic.ChangeTime.Nanoseconds()
	key.StrongIdentity = true
}
