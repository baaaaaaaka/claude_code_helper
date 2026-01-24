package cli

import (
	"bufio"
	"strings"
	"testing"
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
