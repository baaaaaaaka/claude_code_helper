//go:build darwin

package cli

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func adhocCodesign(path string, log io.Writer) error {
	cmd := exec.Command("codesign", "--force", "--sign", "-", "--timestamp=none", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("codesign %s: %s", path, msg)
	}
	if log != nil {
		_, _ = fmt.Fprintf(log, "exe-patch: ad-hoc signed %s\n", path)
	}
	return nil
}
