//go:build windows

package cli

import "testing"

func withPseudoTTY(t *testing.T, fn func()) {
	t.Helper()
	t.Skip("pseudo tty not supported on windows")
}
