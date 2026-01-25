//go:build !windows

package update

import (
	"os"
)

func replaceBinary(tmpPath, destPath string) (replaceResult, error) {
	if err := os.Rename(tmpPath, destPath); err != nil {
		return replaceResult{}, err
	}
	return replaceResult{restartRequired: false}, nil
}
