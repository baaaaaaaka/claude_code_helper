package cli

import (
	"errors"
	"testing"
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/manager"
)

func TestRecoveryBudgetIsBoundedAndResets(t *testing.T) {
	var b recoveryBudget
	now := time.Now()
	if !b.Allow(manager.ErrRecovering, now) || !b.Allow(manager.ErrRecovering, now.Add(89*time.Second)) {
		t.Fatal("recovery budget not honored")
	}
	if b.Allow(manager.ErrRecovering, now.Add(90*time.Second)) {
		t.Fatal("recovery budget unbounded")
	}
	if b.Allow(errors.New("dead"), now) {
		t.Fatal("ordinary failure treated as recovery")
	}
	if b.Allow(manager.ErrRecovering, now.Add(time.Hour)) {
		t.Fatal("intermittent errors extended recovery budget")
	}
	b = recoveryBudget{} // A successful health check resets the budget.
	if !b.Allow(manager.ErrRecovering, now.Add(time.Hour)) {
		t.Fatal("new recovery did not get fresh budget")
	}
}
