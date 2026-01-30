//go:build !darwin

package cli

import (
	"bytes"
	"testing"
)

func TestAdhocCodesignNoop(t *testing.T) {
	var log bytes.Buffer
	if err := adhocCodesign("/tmp/claude", &log); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
