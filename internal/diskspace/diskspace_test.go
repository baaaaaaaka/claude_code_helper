package diskspace

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestEnsureAvailableReportsInsufficientSpace(t *testing.T) {
	prev := availableBytesFn
	availableBytesFn = func(path string) (uint64, error) {
		return 10, nil
	}
	t.Cleanup(func() { availableBytesFn = prev })

	err := EnsureAvailable(t.TempDir()+"/target", 11)
	if !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected insufficient disk space error, got %v", err)
	}
	if !strings.Contains(err.Error(), "insufficient disk space") {
		t.Fatalf("expected clear insufficient disk space message, got %q", err.Error())
	}
}

func TestEnsureAvailableIgnoresUnavailableProbe(t *testing.T) {
	prev := availableBytesFn
	availableBytesFn = func(path string) (uint64, error) {
		return 0, errors.New("statfs unavailable")
	}
	t.Cleanup(func() { availableBytesFn = prev })

	if err := EnsureAvailable(t.TempDir()+"/target", 1); err != nil {
		t.Fatalf("expected unavailable space probe to be non-fatal, got %v", err)
	}
}

func TestEnsureAvailableForWriteOnlyRequiresAdditionalBytes(t *testing.T) {
	prev := availableBytesFn
	availableBytesFn = func(path string) (uint64, error) {
		return 1, nil
	}
	t.Cleanup(func() { availableBytesFn = prev })

	target := t.TempDir() + "/target"
	if err := os.WriteFile(target, []byte("1234567890"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := EnsureAvailableForWrite(target, 10); err != nil {
		t.Fatalf("expected same-size overwrite to pass, got %v", err)
	}
	if err := EnsureAvailableForWrite(target, 11); err != nil {
		t.Fatalf("expected one-byte growth to pass, got %v", err)
	}
	err := EnsureAvailableForWrite(target, 12)
	if !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected insufficient disk space for two-byte growth, got %v", err)
	}
}

func TestAnnotateWriteErrorReportsNoSpaceText(t *testing.T) {
	err := AnnotateWriteError(t.TempDir()+"/target", errors.New("write: no space left on device"))
	if !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected insufficient disk space error, got %v", err)
	}
	if !strings.Contains(err.Error(), "insufficient disk space") {
		t.Fatalf("expected clear insufficient disk space message, got %q", err.Error())
	}
}
