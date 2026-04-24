//go:build !windows

package update

import (
	"fmt"
	"os"

	"github.com/baaaaaaaka/claude_code_helper/internal/diskspace"
)

func replaceBinary(tmpPath, destPath string) (replaceResult, error) {
	if err := os.Rename(tmpPath, destPath); err != nil {
		return replaceResult{}, fmt.Errorf("replace binary: %w", diskspace.AnnotateWriteError(destPath, err))
	}
	return replaceResult{restartRequired: false}, nil
}
