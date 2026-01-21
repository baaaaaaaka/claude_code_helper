package ssh

import (
	"reflect"
	"testing"
)

func TestBuildArgs_IncludesRequiredOptions(t *testing.T) {
	cfg := TunnelConfig{
		Host:      "example.com",
		Port:      2222,
		User:      "alice",
		SocksPort: 12345,
		ExtraArgs: []string{"-i", "/tmp/key"},
		BatchMode: true,
	}

	args, err := BuildArgs(cfg)
	if err != nil {
		t.Fatalf("BuildArgs error: %v", err)
	}

	wantPrefix := []string{
		"-N",
		"-D", "127.0.0.1:12345",
		"-p", "2222",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "TCPKeepAlive=yes",
		"-o", "ClearAllForwardings=yes",
		"-o", "BatchMode=yes",
		"-i", "/tmp/key",
		"alice@example.com",
	}

	if !reflect.DeepEqual(args, wantPrefix) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", args, wantPrefix)
	}
}

func TestBuildArgs_ValidatesPorts(t *testing.T) {
	_, err := BuildArgs(TunnelConfig{
		Host:      "h",
		Port:      0,
		User:      "u",
		SocksPort: 1,
	})
	if err == nil {
		t.Fatalf("expected error for invalid ssh port")
	}

	_, err = BuildArgs(TunnelConfig{
		Host:      "h",
		Port:      22,
		User:      "u",
		SocksPort: 0,
	})
	if err == nil {
		t.Fatalf("expected error for invalid socks port")
	}
}
