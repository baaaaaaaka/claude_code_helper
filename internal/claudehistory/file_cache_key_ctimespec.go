//go:build darwin || freebsd || netbsd

package claudehistory

import (
	"os"
	"syscall"
)

func populateFileCacheKeyPlatform(_ string, info os.FileInfo, key *fileCacheKey) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	key.Device = uint64(st.Dev)
	key.Inode = uint64(st.Ino)
	key.ChangeTimeUnixNano = st.Ctimespec.Sec*1e9 + st.Ctimespec.Nsec
	key.StrongIdentity = true
}
