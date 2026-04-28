package ssh

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
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
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "TCPKeepAlive=yes",
		"-p", "2222",
		"-D", "127.0.0.1:12345",
		"-o", "BatchMode=yes",
		"-i", "/tmp/key",
		"alice@example.com",
	}

	if !reflect.DeepEqual(args, wantPrefix) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", args, wantPrefix)
	}
}

func TestBuildArgs_NoUserAndBatchModeFalse(t *testing.T) {
	args, err := BuildArgs(TunnelConfig{
		Host:      " example.com ",
		Port:      22,
		User:      "  ",
		SocksPort: 1080,
		BatchMode: false,
	})
	if err != nil {
		t.Fatalf("BuildArgs error: %v", err)
	}

	want := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "TCPKeepAlive=yes",
		"-p", "22",
		"-D", "127.0.0.1:1080",
		"example.com",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch\n got: %#v\nwant: %#v", args, want)
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" && args[i+1] == "BatchMode=yes" {
			t.Fatalf("BatchMode option should be omitted when false: %#v", args)
		}
	}
}

func TestBuildArgs_ValidatesPorts(t *testing.T) {
	tests := []struct {
		name string
		cfg  TunnelConfig
	}{
		{name: "zero ssh port", cfg: TunnelConfig{Host: "h", Port: 0, User: "u", SocksPort: 1}},
		{name: "high ssh port", cfg: TunnelConfig{Host: "h", Port: 65536, User: "u", SocksPort: 1}},
		{name: "zero socks port", cfg: TunnelConfig{Host: "h", Port: 22, User: "u", SocksPort: 0}},
		{name: "high socks port", cfg: TunnelConfig{Host: "h", Port: 22, User: "u", SocksPort: 65536}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildArgs(tt.cfg); err == nil {
				t.Fatalf("expected error for invalid ports")
			}
		})
	}
}

func TestTunnelLifecycleFailures(t *testing.T) {
	t.Run("BuildArgs requires host", func(t *testing.T) {
		_, err := BuildArgs(TunnelConfig{
			Host:      "",
			Port:      22,
			User:      "u",
			SocksPort: 1,
		})
		if err == nil {
			t.Fatalf("expected error for missing host")
		}

		_, err = BuildArgs(TunnelConfig{
			Host:      "   ",
			Port:      22,
			User:      "alice",
			SocksPort: 1,
		})
		if err == nil {
			t.Fatalf("expected error for blank host")
		}
	})

	t.Run("Start fails when ssh missing", func(t *testing.T) {
		t.Setenv("PATH", "")
		cfg := TunnelConfig{
			Host:      "example.com",
			Port:      22,
			User:      "alice",
			SocksPort: 12345,
		}
		tun, err := NewTunnel(cfg)
		if err != nil {
			t.Fatalf("NewTunnel error: %v", err)
		}
		if err := tun.Start(); err == nil {
			t.Fatalf("expected Start to fail without ssh")
		}
	})

	t.Run("Stop before Start is a no-op", func(t *testing.T) {
		cfg := TunnelConfig{
			Host:      "example.com",
			Port:      22,
			User:      "alice",
			SocksPort: 12345,
		}
		tun, err := NewTunnel(cfg)
		if err != nil {
			t.Fatalf("NewTunnel error: %v", err)
		}
		if err := tun.Stop(10 * time.Millisecond); err != nil {
			t.Fatalf("expected Stop to return nil, got %v", err)
		}
	})

	t.Run("Start handles immediate exit", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skip shell script test on windows")
		}
		dir := t.TempDir()
		script := filepath.Join(dir, "ssh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
			t.Fatalf("write script: %v", err)
		}
		t.Setenv("PATH", dir)

		cfg := TunnelConfig{
			Host:      "example.com",
			Port:      22,
			User:      "alice",
			SocksPort: 12345,
		}
		tun, err := NewTunnel(cfg)
		if err != nil {
			t.Fatalf("NewTunnel error: %v", err)
		}
		if err := tun.Start(); err != nil {
			t.Fatalf("Start error: %v", err)
		}
		if err := tun.Wait(); err == nil {
			t.Fatalf("expected Wait to report exit error")
		}
	})

	t.Run("Stop forces kill after grace", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skip shell script test on windows")
		}
		dir := t.TempDir()
		script := filepath.Join(dir, "ssh")
		content := "#!/bin/sh\ntrap '' INT\nsleep 5\n"
		if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
			t.Fatalf("write script: %v", err)
		}
		t.Setenv("PATH", dir)

		cfg := TunnelConfig{
			Host:      "example.com",
			Port:      22,
			User:      "alice",
			SocksPort: 12345,
		}
		tun, err := NewTunnel(cfg)
		if err != nil {
			t.Fatalf("NewTunnel error: %v", err)
		}
		if err := tun.Start(); err != nil {
			t.Fatalf("Start error: %v", err)
		}
		if err := tun.Stop(50 * time.Millisecond); err == nil {
			t.Fatalf("expected Stop to report forced kill")
		}
	})
}
