//go:build !windows

package cli

import (
	"fmt"
	"os"
)

// defaultHistoryRequireTTY checks whether tcell can actually attach to a
// terminal. tcell's Unix backend opens /dev/tty directly, so that is the
// real requirement — not that stdin or stdout individually be TTYs. This
// keeps the TUI usable under wrappers that pipe stdio but leave the
// controlling terminal intact.
func defaultHistoryRequireTTY() error {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("clp tui requires a terminal (could not open /dev/tty: %w); use `clp history list` for non-interactive inspection", err)
	}
	_ = f.Close()
	return nil
}
