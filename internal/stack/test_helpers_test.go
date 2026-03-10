package stack

import (
	"context"
	"testing"
)

func TestNewStackForTest(t *testing.T) {
	st := NewStackForTest(123, 456)
	if st.HTTPPort != 123 || st.SocksPort != 456 {
		t.Fatalf("unexpected ports: http=%d socks=%d", st.HTTPPort, st.SocksPort)
	}
	if st.Fatal() == nil {
		t.Fatalf("expected fatal channel")
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}

func TestNewStackWithFatalForTest(t *testing.T) {
	fatalCh := make(chan error, 1)
	st := NewStackWithFatalForTest(123, 456, fatalCh)
	if st.HTTPPort != 123 || st.SocksPort != 456 {
		t.Fatalf("unexpected ports: http=%d socks=%d", st.HTTPPort, st.SocksPort)
	}
	if st.Fatal() == nil {
		t.Fatalf("expected fatal channel")
	}
	if got := st.Fatal(); got != fatalCh {
		t.Fatalf("expected provided fatal channel to be reused")
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
}
