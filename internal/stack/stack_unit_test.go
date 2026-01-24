package stack

import (
	"context"
	"testing"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func TestStartValidationErrors(t *testing.T) {
	profile := config.Profile{Host: "", Port: 22, User: "u"}
	if _, err := Start(profile, "id", Options{}); err == nil {
		t.Fatalf("expected error for missing host")
	}

	profile = config.Profile{Host: "h", Port: 0, User: "u"}
	if _, err := Start(profile, "id", Options{}); err == nil {
		t.Fatalf("expected error for missing port")
	}

	profile = config.Profile{Host: "h", Port: 22, User: ""}
	if _, err := Start(profile, "id", Options{}); err == nil {
		t.Fatalf("expected error for missing user")
	}

	profile = config.Profile{Host: "h", Port: 22, User: "u"}
	if _, err := Start(profile, "", Options{}); err == nil {
		t.Fatalf("expected error for missing instance id")
	}
}

func TestStackHelpers(t *testing.T) {
	s := &Stack{
		HTTPPort: 1234,
		fatalCh:  make(chan error),
		stopCh:   make(chan struct{}),
	}
	if got := s.HTTPProxyURL(); got != "http://127.0.0.1:1234" {
		t.Fatalf("unexpected HTTPProxyURL: %q", got)
	}
	if s.Fatal() == nil {
		t.Fatalf("expected fatal channel")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	select {
	case <-s.stopCh:
	default:
		t.Fatalf("expected stopCh to be closed")
	}
}

func TestNewTunnel(t *testing.T) {
	profile := config.Profile{Host: "example.com", Port: 22, User: "alice"}
	if _, err := newTunnel(profile, 12345); err != nil {
		t.Fatalf("newTunnel error: %v", err)
	}
}
