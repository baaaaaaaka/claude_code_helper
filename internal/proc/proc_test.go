//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"testing"
)

func TestIsAlive(t *testing.T) {
	if IsAlive(0) {
		t.Fatalf("expected pid 0 to be dead")
	}
	if IsAlive(-1) {
		t.Fatalf("expected negative pid to be dead")
	}
	if !IsAlive(os.Getpid()) {
		t.Fatalf("expected current pid to be alive")
	}
}

func TestIsAliveAfterProcessExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if IsAlive(pid) {
		t.Fatalf("expected exited process to be dead")
	}
}
