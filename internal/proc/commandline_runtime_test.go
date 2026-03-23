//go:build linux || darwin || windows

package proc

import (
	"os"
	"strings"
	"testing"
)

func TestCommandLineCurrentProcess(t *testing.T) {
	args, err := CommandLine(os.Getpid())
	if err != nil {
		t.Fatalf("CommandLine(current pid): %v", err)
	}
	if len(args) == 0 {
		t.Fatalf("expected current process args")
	}
	if strings.TrimSpace(args[0]) == "" {
		t.Fatalf("expected non-empty executable path in args: %#v", args)
	}
	foundTestFlag := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "-test.") {
			foundTestFlag = true
			break
		}
	}
	if !foundTestFlag {
		t.Fatalf("expected testing flags in current process args, got %#v", args)
	}
	if LooksLikeProxyDaemon(args) {
		t.Fatalf("current test process should not look like a proxy daemon: %#v", args)
	}
}
