package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestMainVersionExitZero(t *testing.T) {
	if os.Getenv("CLAUDE_PROXY_HELPER") == "1" {
		os.Args = []string{"claude-proxy", "--version"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainVersionExitZero")
	cmd.Env = append(os.Environ(), "CLAUDE_PROXY_HELPER=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0, got error: %v", err)
	}
}

func TestMainInvalidArgsExitOne(t *testing.T) {
	if os.Getenv("CLAUDE_PROXY_HELPER_INVALID") == "1" {
		os.Args = []string{"claude-proxy", "--not-a-flag"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainInvalidArgsExitOne")
	cmd.Env = append(os.Environ(), "CLAUDE_PROXY_HELPER_INVALID=1")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit, got nil error")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
	}
}
