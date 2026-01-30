package cli

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestEnsureProfileNoProfilesNoAutoInit(t *testing.T) {
	store := newTempStore(t)
	if _, _, err := ensureProfile(context.Background(), store, "", false, io.Discard); err == nil {
		t.Fatalf("expected error when no profiles and autoInit disabled")
	}
}

func TestEnsureProfileSelectsProfile(t *testing.T) {
	store := newTempStore(t)
	cfg := config.Config{
		Version:  config.CurrentVersion,
		Profiles: []config.Profile{{ID: "p1", Name: "one"}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	prof, loaded, err := ensureProfile(context.Background(), store, "one", false, io.Discard)
	if err != nil {
		t.Fatalf("ensureProfile error: %v", err)
	}
	if prof.ID != "p1" {
		t.Fatalf("expected profile p1, got %q", prof.ID)
	}
	if len(loaded.Profiles) != 1 {
		t.Fatalf("expected loaded profiles, got %#v", loaded.Profiles)
	}
}

func TestEnsureProfileAutoInitReturnsCreated(t *testing.T) {
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

	store := newTempStore(t)
	profile, cfg, err := ensureProfile(context.Background(), store, "", true, io.Discard)
	if err != nil {
		t.Fatalf("ensureProfile error: %v", err)
	}
	if profile.Name != "alice@host.example" {
		t.Fatalf("unexpected profile name %q", profile.Name)
	}
	if len(cfg.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(cfg.Profiles))
	}
}
