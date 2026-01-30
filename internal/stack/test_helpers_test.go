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
