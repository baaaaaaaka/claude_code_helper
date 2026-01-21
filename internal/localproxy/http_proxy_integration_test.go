package localproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPProxy_CONNECT_GoesThroughSOCKS(t *testing.T) {
	echoAddr, closeEcho := startTCPEchoServer(t)
	defer closeEcho()

	socks := startSOCKS5Server(t)
	defer socks.Close()

	dialer, err := NewSOCKS5Dialer(socks.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("NewSOCKS5Dialer: %v", err)
	}

	p := NewHTTPProxy(dialer, Options{InstanceID: "test-instance"})
	httpAddr, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start HTTP proxy: %v", err)
	}
	defer func() { _ = p.Close(context.Background()) }()

	c, err := net.DialTimeout("tcp", httpAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial http proxy: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))

	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echoAddr, echoAddr)

	br := bufio.NewReader(c)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("unexpected status line: %q", statusLine)
	}
	// Drain headers.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	payload := []byte("ping-123")
	if _, err := c.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo mismatch got=%q want=%q", string(got), string(payload))
	}

	if socks.ConnectCount() != 1 {
		t.Fatalf("expected 1 SOCKS CONNECT, got %d", socks.ConnectCount())
	}
	if socks.LastDest() != echoAddr {
		t.Fatalf("SOCKS last dest got=%q want=%q", socks.LastDest(), echoAddr)
	}
}

func startTCPEchoServer(t *testing.T) (addr string, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()

	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

type socks5Server struct {
	ln net.Listener

	connects atomic.Int64
	lastDest atomic.Value // string

	wg sync.WaitGroup
}

func startSOCKS5Server(t *testing.T) *socks5Server {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks: %v", err)
	}

	s := &socks5Server{ln: ln}
	s.lastDest.Store("")

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func(conn net.Conn) {
				defer s.wg.Done()
				_ = s.handleConn(conn)
			}(c)
		}
	}()

	return s
}

func (s *socks5Server) Addr() string { return s.ln.Addr().String() }

func (s *socks5Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *socks5Server) ConnectCount() int64 { return s.connects.Load() }

func (s *socks5Server) LastDest() string {
	v, _ := s.lastDest.Load().(string)
	return v
}

func (s *socks5Server) handleConn(c net.Conn) error {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	// Greeting: VER, NMETHODS, METHODS...
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return err
	}
	if hdr[0] != 0x05 {
		return fmt.Errorf("unexpected ver %d", hdr[0])
	}
	nm := int(hdr[1])
	methods := make([]byte, nm)
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	// Choose "no auth" (0x00) if offered.
	chosen := byte(0xFF)
	for _, m := range methods {
		if m == 0x00 {
			chosen = 0x00
			break
		}
	}
	if _, err := c.Write([]byte{0x05, chosen}); err != nil {
		return err
	}
	if chosen == 0xFF {
		return fmt.Errorf("no acceptable auth methods")
	}

	// Request: VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT
	reqHdr := make([]byte, 4)
	if _, err := io.ReadFull(c, reqHdr); err != nil {
		return err
	}
	if reqHdr[0] != 0x05 {
		return fmt.Errorf("unexpected req ver %d", reqHdr[0])
	}
	if reqHdr[1] != 0x01 { // CONNECT
		return fmt.Errorf("unsupported cmd %d", reqHdr[1])
	}
	atyp := reqHdr[3]

	var host string
	switch atyp {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		host = net.IP(b).String()
	case 0x03: // DOMAIN
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return err
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		host = string(b)
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		host = net.IP(b).String()
	default:
		return fmt.Errorf("unsupported atyp %d", atyp)
	}

	var portBuf [2]byte
	if _, err := io.ReadFull(c, portBuf[:]); err != nil {
		return err
	}
	port := int(binary.BigEndian.Uint16(portBuf[:]))
	dest := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	s.connects.Add(1)
	s.lastDest.Store(dest)

	up, err := net.DialTimeout("tcp", dest, 2*time.Second)
	if err != nil {
		// General failure.
		_, _ = c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return err
	}
	defer up.Close()

	// Success reply (bound addr/port set to 0.0.0.0:0).
	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(up, c)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(c, up)
		errCh <- err
	}()

	// Wait for one direction to finish.
	<-errCh
	return nil
}
