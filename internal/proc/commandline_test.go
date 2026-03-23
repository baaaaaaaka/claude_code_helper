package proc

import "testing"

func TestLooksLikeProxyDaemon(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "proxy daemon command",
			args: []string{"/usr/bin/claude-proxy", "proxy", "daemon", "--instance-id", "inst-1"},
			want: true,
		},
		{
			name: "proxy daemon with root flags",
			args: []string{"/usr/bin/claude-proxy", "--config", "/tmp/config.json", "proxy", "daemon", "--instance-id", "inst-1"},
			want: true,
		},
		{
			name: "foreground proxy start",
			args: []string{"/usr/bin/claude-proxy", "--config", "/tmp/config.json", "proxy", "start", "p1", "--foreground"},
			want: true,
		},
		{
			name: "foreground proxy start with equals syntax",
			args: []string{"/usr/bin/claude-proxy", "--config", "/tmp/config.json", "proxy", "start", "p1", "--foreground=true"},
			want: true,
		},
		{
			name: "background proxy start is not a daemon",
			args: []string{"/usr/bin/claude-proxy", "--config", "/tmp/config.json", "proxy", "start", "p1"},
			want: false,
		},
		{
			name: "run command target args do not count",
			args: []string{"/usr/bin/claude-proxy", "run", "p1", "--", "echo", "proxy", "daemon"},
			want: false,
		},
		{
			name: "regular run is not a daemon",
			args: []string{"/usr/bin/claude-proxy", "run", "p1"},
			want: false,
		},
		{
			name: "empty args",
			args: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeProxyDaemon(tt.args); got != tt.want {
				t.Fatalf("LooksLikeProxyDaemon(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
