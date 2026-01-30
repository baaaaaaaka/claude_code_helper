package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestPromptDefault(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	got := prompt(r, "Label", "default")
	if got != "default" {
		t.Fatalf("expected default, got %q", got)
	}
}

func TestPromptRequired(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\nvalue\n"))
	got := promptRequired(r, "Label")
	if got != "value" {
		t.Fatalf("expected value, got %q", got)
	}
}

func TestPromptInt(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("abc\n0\n70000\n42\n"))
	got := promptInt(r, "Port", 22)
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestPromptYesNoDefault(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	got := promptYesNo(r, "Confirm", true)
	if got != true {
		t.Fatalf("expected true, got %v", got)
	}
}

type stubSSHOps struct {
	probeCalls    []bool
	probeErrors   []error
	generateCalls int
	installCalls  int
	keyPath       string
	generateErr   error
	installErr    error
}

func (s *stubSSHOps) probe(_ context.Context, _ config.Profile, interactive bool) error {
	s.probeCalls = append(s.probeCalls, interactive)
	if len(s.probeErrors) == 0 {
		return nil
	}
	err := s.probeErrors[0]
	s.probeErrors = s.probeErrors[1:]
	return err
}

func (s *stubSSHOps) generateKeypair(_ context.Context, _ *config.Store, _ config.Profile) (string, error) {
	s.generateCalls++
	if s.generateErr != nil {
		return "", s.generateErr
	}
	if s.keyPath == "" {
		return "", fmt.Errorf("missing key path")
	}
	return s.keyPath, nil
}

func (s *stubSSHOps) installPublicKey(_ context.Context, _ config.Profile, _ string) error {
	s.installCalls++
	if s.installErr != nil {
		return s.installErr
	}
	return nil
}

func TestInitProfileInteractiveSkipsKeyWhenDirectAccessWorks(t *testing.T) {
	store := newTempStore(t)
	reader := bufio.NewReader(strings.NewReader("host.example\n22\nalice\n"))
	ops := &stubSSHOps{}

	prof, err := initProfileInteractiveWithDeps(context.Background(), store, reader, ops, io.Discard)
	if err != nil {
		t.Fatalf("init profile error: %v", err)
	}
	if prof.Name != "alice@host.example" {
		t.Fatalf("expected name alice@host.example, got %q", prof.Name)
	}
	if len(prof.SSHArgs) != 0 {
		t.Fatalf("expected no SSH args, got %v", prof.SSHArgs)
	}
	if ops.generateCalls != 0 || ops.installCalls != 0 {
		t.Fatalf("expected no key setup, got generate=%d install=%d", ops.generateCalls, ops.installCalls)
	}
	if len(ops.probeCalls) != 1 || ops.probeCalls[0] {
		t.Fatalf("expected single non-interactive probe, got %v", ops.probeCalls)
	}
}

func TestInitProfileInteractiveAutoKeyWhenProbeFails(t *testing.T) {
	store := newTempStore(t)
	reader := bufio.NewReader(strings.NewReader("host.example\n22\nalice\n"))
	ops := &stubSSHOps{
		probeErrors: []error{fmt.Errorf("no auth"), nil},
		keyPath:     "/tmp/claude-proxy-key",
	}

	prof, err := initProfileInteractiveWithDeps(context.Background(), store, reader, ops, io.Discard)
	if err != nil {
		t.Fatalf("init profile error: %v", err)
	}
	if prof.Name != "alice@host.example" {
		t.Fatalf("expected name alice@host.example, got %q", prof.Name)
	}
	if want := []string{"-i", ops.keyPath}; strings.Join(prof.SSHArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("expected SSH args %v, got %v", want, prof.SSHArgs)
	}
	if ops.generateCalls != 1 || ops.installCalls != 1 {
		t.Fatalf("expected key setup once, got generate=%d install=%d", ops.generateCalls, ops.installCalls)
	}
	for _, interactive := range ops.probeCalls {
		if interactive {
			t.Fatalf("expected non-interactive probes only")
		}
	}
}

func TestInitProfileInteractiveKeySetupErrors(t *testing.T) {
	t.Run("generateKeypair failure", func(t *testing.T) {
		store := newTempStore(t)
		reader := bufio.NewReader(strings.NewReader("host.example\n22\nalice\n"))
		ops := &stubSSHOps{
			probeErrors: []error{fmt.Errorf("no auth")},
			generateErr: fmt.Errorf("boom"),
		}

		if _, err := initProfileInteractiveWithDeps(context.Background(), store, reader, ops, io.Discard); err == nil {
			t.Fatalf("expected generate keypair error")
		}
		if ops.generateCalls != 1 {
			t.Fatalf("expected generate to be called once, got %d", ops.generateCalls)
		}
	})

	t.Run("installPublicKey failure", func(t *testing.T) {
		store := newTempStore(t)
		reader := bufio.NewReader(strings.NewReader("host.example\n22\nalice\n"))
		ops := &stubSSHOps{
			probeErrors: []error{fmt.Errorf("no auth")},
			keyPath:     "/tmp/claude-proxy-key",
			installErr:  fmt.Errorf("install failed"),
		}

		if _, err := initProfileInteractiveWithDeps(context.Background(), store, reader, ops, io.Discard); err == nil {
			t.Fatalf("expected install public key error")
		}
		if ops.installCalls != 1 {
			t.Fatalf("expected install to be called once, got %d", ops.installCalls)
		}
	})

	t.Run("probe fails after key install", func(t *testing.T) {
		store := newTempStore(t)
		reader := bufio.NewReader(strings.NewReader("host.example\n22\nalice\n"))
		ops := &stubSSHOps{
			probeErrors: []error{fmt.Errorf("no auth"), fmt.Errorf("still no auth")},
			keyPath:     "/tmp/claude-proxy-key",
		}

		if _, err := initProfileInteractiveWithDeps(context.Background(), store, reader, ops, io.Discard); err == nil {
			t.Fatalf("expected post-install probe error")
		}
	})
}

func TestNextAvailableKeyPath(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "id_ed25519_test")
	got, err := nextAvailableKeyPath(base)
	if err != nil {
		t.Fatalf("nextAvailableKeyPath error: %v", err)
	}
	if got != base {
		t.Fatalf("expected %s, got %s", base, got)
	}

	if err := os.WriteFile(base, []byte("x"), 0o600); err != nil {
		t.Fatalf("write base: %v", err)
	}
	got, err = nextAvailableKeyPath(base)
	if err != nil {
		t.Fatalf("nextAvailableKeyPath error: %v", err)
	}
	if got != base+"_1" {
		t.Fatalf("expected %s, got %s", base+"_1", got)
	}
	if err := os.WriteFile(base+"_1", []byte("y"), 0o600); err != nil {
		t.Fatalf("write base_1: %v", err)
	}
	got, err = nextAvailableKeyPath(base)
	if err != nil {
		t.Fatalf("nextAvailableKeyPath error: %v", err)
	}
	if got != base+"_2" {
		t.Fatalf("expected %s, got %s", base+"_2", got)
	}
}

func TestNewInitCmdFailsWhenConfigDirUnwritable(t *testing.T) {
	base := t.TempDir()
	blocked := filepath.Join(base, "config")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}

	root := &rootOptions{configPath: filepath.Join(blocked, "config.json")}
	cmd := newInitCmd(root)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected init to fail with unwritable config dir")
	}
}

func TestNewInitCmdSuccess(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "ssh", "#!/bin/sh\nexit 0\n", "@echo off\r\nexit /b 0\r\n")
	setStubPath(t, dir)

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = prevStdin })
	if _, err := writer.Write([]byte("host.example\n22\nalice\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = writer.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	cmd := newInitCmd(&rootOptions{configPath: configPath})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("newInitCmd error: %v", err)
	}
}
