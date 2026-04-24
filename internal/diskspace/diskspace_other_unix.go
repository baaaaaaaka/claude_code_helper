//go:build !windows && !linux

package diskspace

import "errors"

func availableBytes(path string) (uint64, error) {
	_ = path
	return 0, errors.New("disk space probe unsupported on this platform")
}

func isNoSpaceError(err error) bool {
	return false
}
