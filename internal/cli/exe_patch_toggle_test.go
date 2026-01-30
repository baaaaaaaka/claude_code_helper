package cli

import "testing"

func TestExePatchEnabledDefaultEnvParsing(t *testing.T) {
	t.Run("empty defaults true", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "")
		if !exePatchEnabledDefault() {
			t.Fatalf("expected default enabled when env empty")
		}
	})

	t.Run("invalid defaults false", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "not-a-bool")
		if exePatchEnabledDefault() {
			t.Fatalf("expected disabled when env invalid")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "false")
		if exePatchEnabledDefault() {
			t.Fatalf("expected disabled when env false")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "true")
		if !exePatchEnabledDefault() {
			t.Fatalf("expected enabled when env true")
		}
	})
}
