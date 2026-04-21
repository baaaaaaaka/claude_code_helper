//go:build windows

package cli

import (
	"errors"
	"os"

	"golang.org/x/term"
)

// defaultHistoryRequireTTY checks whether tcell can attach to a console on
// Windows. tcell's Windows backend uses the console handle directly; there
// is no /dev/tty. IsTerminal on stdout is the closest cross-platform proxy
// for "is there a usable console attached".
func defaultHistoryRequireTTY() error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("clp tui requires a console; use `clp history list` for non-interactive inspection")
	}
	return nil
}
