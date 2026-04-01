//go:build linux

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
	key.ChangeTimeUnixNano = st.Ctim.Sec*1e9 + st.Ctim.Nsec
	key.StrongIdentity = true
}
