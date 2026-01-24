//go:build !windows

package proc

import (
	"os"
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
