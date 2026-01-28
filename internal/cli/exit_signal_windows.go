//go:build windows

package cli

func exitDueToFatalSignal(err error) bool {
	return false
}
