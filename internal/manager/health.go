package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

type HealthClient struct {
	Timeout time.Duration
}

type healthResponse struct {
	OK         bool   `json:"ok"`
	InstanceID string `json:"instanceId"`
}

func (c HealthClient) CheckHTTPProxy(port int, expectedInstanceID string) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid http port %d", port)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 1 * time.Second
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: timeout,
			}).DialContext,
		},
	}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/_claude_proxy/health", port))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	var hr healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return err
	}
	if !hr.OK {
		return errors.New("health check not ok")
	}
	if expectedInstanceID != "" && hr.InstanceID != expectedInstanceID {
		return fmt.Errorf("unexpected instance id %q", hr.InstanceID)
	}
	return nil
}
