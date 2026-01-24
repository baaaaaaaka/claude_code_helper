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
