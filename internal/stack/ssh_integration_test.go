package stack

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/ssh"
)

func integrationProfile(t *testing.T) config.Profile {
	t.Helper()
	if os.Getenv("SSH_TEST_ENABLED") != "1" {
		t.Skip("isolated SSH fixture not enabled")
	}
	port, err := strconv.Atoi(os.Getenv("SSH_TEST_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	return config.Profile{Host: os.Getenv("SSH_TEST_HOST"), Port: port, User: os.Getenv("SSH_TEST_USER"), SSHArgs: []string{"-i", os.Getenv("SSH_TEST_KEY"), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}}
}
func TestStackSSHIntegrationPoolsHTTPAndCloses(t *testing.T) {
	profile := integrationProfile(t)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer origin.Close()
	st, err := Start(profile, "integration", Options{SocksReadyTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	u, _ := url.Parse(st.HTTPProxyURL())
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	for i := 0; i < 100; i++ {
		r, err := client.Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if string(b) != "ok" {
			t.Fatalf("response %q", b)
		}
	}
	if err = st.Close(context.Background()); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
	}
}
func TestStackSSHIntegrationRejectsForeignListener(t *testing.T) {
	profile := integrationProfile(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, err = Start(profile, "conflict", Options{SocksPort: ln.Addr().(*net.TCPAddr).Port, SocksReadyTimeout: 10 * time.Second})
	if !errors.Is(err, ssh.ErrBindConflict) {
		t.Fatalf("foreign listener accepted or misclassified: %v", err)
	}
}
