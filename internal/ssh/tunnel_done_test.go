package ssh

import "testing"

func TestTunnel_Done(t *testing.T) {
	ch := make(chan struct{})
	tun := &Tunnel{done: ch}

	if tun.Done() != ch {
		t.Fatalf("Done returned different channel")
	}

	close(ch)
	select {
	case <-tun.Done():
		// ok
	default:
		t.Fatalf("expected Done channel to be closed")
	}
}
