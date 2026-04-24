//go:build !windows

package diskspace

import (
	"errors"
	"math"

	"golang.org/x/sys/unix"
)

func availableBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	blockSize := uint64(stat.Bsize)
	blocks := uint64(stat.Bavail)
	if blockSize == 0 || blocks == 0 {
		return 0, nil
	}
	if blocks > math.MaxUint64/blockSize {
		return math.MaxUint64, nil
	}
	return blocks * blockSize, nil
}

func isNoSpaceError(err error) bool {
	return errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EDQUOT)
}
