package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type HealthClient struct {
	Timeout   time.Duration
	transport *http.Transport
}

func healthTransport() *http.Transport {
	return &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
		MaxIdleConns: 16, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second,
	}
}

// The fallback keeps legacy value-initialized clients safe; command owners use NewHealthClient.
var defaultHealthTransport = healthTransport()

func NewHealthClient(timeout time.Duration) HealthClient {
	return HealthClient{Timeout: timeout, transport: healthTransport()}
}
func (c HealthClient) Close() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

var ErrRecovering = errors.New("proxy backend recovering")

func (c HealthClient) WaitReady(ctx context.Context, port int, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		err := c.CheckHTTPProxyContext(ctx, port, id)
		if !errors.Is(err, ErrRecovering) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

type healthResponse struct {
	BackendState string `json:"backendState"`
	OK           bool   `json:"ok"`
	InstanceID   string `json:"instanceId"`
}

func (c HealthClient) CheckHTTPProxy(port int, expectedInstanceID string) error {
	return c.CheckHTTPProxyContext(context.Background(), port, expectedInstanceID)
}

func (c HealthClient) CheckHTTPProxyContext(ctx context.Context, port int, expectedInstanceID string) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid http port %d", port)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 1 * time.Second
	}

	tr := c.transport
	if tr == nil {
		tr = defaultHealthTransport
	}
	client := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/_claude_proxy/health", port), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	var hr healthResponse
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil {
		return err
	}
	if len(data) > 8192 {
		return errors.New("health response too large")
	}
	if err := json.Unmarshal(data, &hr); err != nil {
		return err
	}
	if !hr.OK {
		return errors.New("health check not ok")
	}
	if expectedInstanceID != "" && hr.InstanceID != expectedInstanceID {
		return fmt.Errorf("unexpected instance id %q", hr.InstanceID)
	}
	if hr.BackendState == "recovering" {
		return ErrRecovering
	}
	if hr.BackendState == "failed" {
		return errors.New("proxy backend failed")
	}
	return nil
}
