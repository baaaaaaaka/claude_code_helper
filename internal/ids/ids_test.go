package ids

import (
	"encoding/hex"
	"testing"
)

func TestNew(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("expected 32-char hex id, got %d", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("expected hex id, got error: %v", err)
	}
}

func TestNewUniqueConcurrent(t *testing.T) {
	const n = 100
	ids := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			id, err := New()
			errs <- err
			ids <- id
		}()
	}
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("New error: %v", err)
		}
		id := <-ids
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
