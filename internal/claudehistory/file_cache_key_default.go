//go:build !linux && !windows && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package claudehistory

import "os"

func populateFileCacheKeyPlatform(_ string, _ os.FileInfo, _ *fileCacheKey) {}
