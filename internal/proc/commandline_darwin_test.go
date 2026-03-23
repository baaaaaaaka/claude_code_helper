//go:build darwin

package proc

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestCommandLineDarwinUsesWidePS(t *testing.T) {
	prevExecCommand := commandLineExecCommand
	t.Cleanup(func() { commandLineExecCommand = prevExecCommand })

	var (
		gotName string
		gotArgs []string
	)
	commandLineExecCommand = func(name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return exec.Command("sh", "-c", "printf '/usr/bin/claude-proxy proxy daemon --instance-id inst-1\\n'")
	}

	args, err := CommandLine(123)
	if err != nil {
		t.Fatalf("CommandLine error: %v", err)
	}
	if gotName != "ps" {
		t.Fatalf("expected ps, got %q", gotName)
	}
	wantPSArgs := []string{"-ww", "-o", "command=", "-p", "123"}
	if !reflect.DeepEqual(gotArgs, wantPSArgs) {
		t.Fatalf("ps args = %v, want %v", gotArgs, wantPSArgs)
	}
	wantArgs := []string{"/usr/bin/claude-proxy", "proxy", "daemon", "--instance-id", "inst-1"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
}
